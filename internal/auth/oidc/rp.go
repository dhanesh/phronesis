package oidc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Relying-party side of the OIDC authorization-code flow with PKCE, used
// for browser sign-in against a real IdP (Dex, Keycloak, Okta, ...).
//
// Back-channel URL: in Docker and Kubernetes the browser reaches the IdP at
// its public issuer URL while the server reaches it at an internal address
// (http://dex:5556/dex). Discover fetches the discovery document from the
// back-channel base, requires its "issuer" to equal the configured public
// issuer, and rewrites only the token and JWKS endpoints to the back-channel
// base. The authorization endpoint stays browser-facing.

// Discovery is the subset of the OpenID Provider metadata the RP uses.
type Discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

const discoveryMaxBytes = 64 * 1024

// Discover fetches <base>/.well-known/openid-configuration, where base is
// backchannel when set, else issuer.
func Discover(ctx context.Context, client *http.Client, issuer, backchannel string) (Discovery, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	issuer = strings.TrimRight(issuer, "/")
	base := issuer
	if backchannel != "" {
		base = strings.TrimRight(backchannel, "/")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/.well-known/openid-configuration", nil)
	if err != nil {
		return Discovery{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Discovery{}, fmt.Errorf("oidc: discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Discovery{}, fmt.Errorf("oidc: discovery returned HTTP %d", resp.StatusCode)
	}
	var d Discovery
	if err := json.NewDecoder(io.LimitReader(resp.Body, discoveryMaxBytes)).Decode(&d); err != nil {
		return Discovery{}, fmt.Errorf("oidc: discovery json: %w", err)
	}
	if strings.TrimRight(d.Issuer, "/") != issuer {
		return Discovery{}, fmt.Errorf("oidc: discovery issuer %q does not match configured issuer %q", d.Issuer, issuer)
	}
	if d.AuthorizationEndpoint == "" || d.TokenEndpoint == "" || d.JWKSURI == "" {
		return Discovery{}, errors.New("oidc: discovery document is missing endpoints")
	}
	if backchannel != "" {
		d.TokenEndpoint = rebase(d.TokenEndpoint, issuer, base)
		d.JWKSURI = rebase(d.JWKSURI, issuer, base)
	}
	return d, nil
}

func rebase(endpoint, from, to string) string {
	if strings.HasPrefix(endpoint, from) {
		return to + strings.TrimPrefix(endpoint, from)
	}
	return endpoint
}

// RP is a confidential OIDC client.
type RP struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string // default: openid profile email
	Discovery    Discovery
	HTTP         *http.Client
}

// FlowParams are the per-request secrets of one authorization request.
type FlowParams struct {
	State, Nonce, Verifier string
}

// NewFlowParams returns fresh random state, nonce and PKCE verifier.
func NewFlowParams() (FlowParams, error) {
	r := func() (string, error) {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		return base64.RawURLEncoding.EncodeToString(b), nil
	}
	var f FlowParams
	var err error
	if f.State, err = r(); err != nil {
		return f, err
	}
	if f.Nonce, err = r(); err != nil {
		return f, err
	}
	f.Verifier, err = r()
	return f, err
}

// AuthCodeURL builds the browser redirect to the IdP.
func (rp *RP) AuthCodeURL(f FlowParams) string {
	scopes := rp.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "profile", "email"}
	}
	sum := sha256.Sum256([]byte(f.Verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {rp.ClientID},
		"redirect_uri":          {rp.RedirectURL},
		"scope":                 {strings.Join(scopes, " ")},
		"state":                 {f.State},
		"nonce":                 {f.Nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(rp.Discovery.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return rp.Discovery.AuthorizationEndpoint + sep + q.Encode()
}

// Exchange trades an authorization code for the id_token
// (client_secret_basic + PKCE verifier).
func (rp *RP) Exchange(ctx context.Context, code, verifier string) (string, error) {
	client := rp.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {rp.RedirectURL},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rp.Discovery.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(url.QueryEscape(rp.ClientID), url.QueryEscape(rp.ClientSecret))
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("oidc: token exchange: %w", err)
	}
	defer resp.Body.Close()
	var body struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, discoveryMaxBytes)).Decode(&body); err != nil {
		return "", fmt.Errorf("oidc: token response (HTTP %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || body.IDToken == "" {
		return "", fmt.Errorf("oidc: token endpoint HTTP %d error=%q", resp.StatusCode, body.Error)
	}
	return body.IDToken, nil
}
