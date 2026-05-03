package oauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dhanesh/phronesis/internal/auth/oidc"
	"github.com/dhanesh/phronesis/internal/principal"
)

// Verifier validates a self-issued OAuth access token (the JWT minted
// by RS256Signer + handlers_oauth.go). It is the symmetric companion
// of RS256Signer — the public key published at /.well-known/jwks.json
// is the one this verifier uses.
//
// Why a bespoke verifier instead of reusing oidc.Adapter:
//
//	oidc.Adapter is shaped around id_tokens (TypeUser principals
//	derived via ClaimMapping). OAuth access tokens carry
//	{client_id, workspace, scope} and resolve to TypeServiceAccount
//	principals — a different shape. Inlining the JWT framing keeps
//	the principal construction declarative.
//
// Satisfies: RT-2 (OAuth resource-server can authenticate self-issued
//
//	access tokens), T1 (regulation — OAuth 2.1 + RFC 9068 access
//	token verification semantics).
type Verifier struct {
	Cache    *oidc.JWKSCache
	Inner    oidc.Verifier
	Issuer   string
	Audience string
	Now      func() time.Time
	Leeway   time.Duration
}

// ErrInvalidAccessToken is the umbrella error for any malformed or
// untrustworthy access token. Callers MUST NOT surface details to the
// requester — handlers translate this to 401 Unauthorized with no
// further hint at where validation failed (information leak).
var ErrInvalidAccessToken = errors.New("oauth: invalid access token")

// VerifyAccessToken parses, signature-verifies, and temporally-checks
// a JWT-shaped access token. On success returns a TypeServiceAccount
// Principal whose ID is the client_id claim, whose WorkspaceID is the
// workspace claim, and whose Role is derived from the scope claim
// ("write" => RoleEditor; otherwise RoleViewer).
//
// Returns ErrInvalidAccessToken (wrapped) on any validation failure.
func (v *Verifier) VerifyAccessToken(token string) (principal.Principal, error) {
	if v == nil || v.Inner == nil || v.Cache == nil {
		return principal.Principal{}, errors.New("oauth: Verifier missing Inner or Cache")
	}
	now := time.Now
	if v.Now != nil {
		now = v.Now
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return principal.Principal{}, fmt.Errorf("%w: not a compact JWT", ErrInvalidAccessToken)
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return principal.Principal{}, fmt.Errorf("%w: header decode: %v", ErrInvalidAccessToken, err)
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return principal.Principal{}, fmt.Errorf("%w: payload decode: %v", ErrInvalidAccessToken, err)
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return principal.Principal{}, fmt.Errorf("%w: signature decode: %v", ErrInvalidAccessToken, err)
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return principal.Principal{}, fmt.Errorf("%w: header json: %v", ErrInvalidAccessToken, err)
	}
	if header.Alg == "" || header.Alg == "none" {
		return principal.Principal{}, fmt.Errorf("%w: missing or 'none' alg", ErrInvalidAccessToken)
	}

	signingInput := []byte(parts[0] + "." + parts[1])
	if err := v.Inner.Verify(header.Alg, header.Kid, signingInput, sigBytes); err != nil {
		return principal.Principal{}, fmt.Errorf("%w: %v", ErrInvalidAccessToken, err)
	}

	var claims map[string]any
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return principal.Principal{}, fmt.Errorf("%w: payload json: %v", ErrInvalidAccessToken, err)
	}

	if iss, _ := claims["iss"].(string); iss != v.Issuer {
		return principal.Principal{}, fmt.Errorf("%w: iss mismatch", ErrInvalidAccessToken)
	}
	switch aud := claims["aud"].(type) {
	case string:
		if aud != v.Audience {
			return principal.Principal{}, fmt.Errorf("%w: aud mismatch", ErrInvalidAccessToken)
		}
	case []any:
		match := false
		for _, a := range aud {
			if s, ok := a.(string); ok && s == v.Audience {
				match = true
				break
			}
		}
		if !match {
			return principal.Principal{}, fmt.Errorf("%w: aud does not contain %q", ErrInvalidAccessToken, v.Audience)
		}
	default:
		return principal.Principal{}, fmt.Errorf("%w: aud missing or wrong type", ErrInvalidAccessToken)
	}

	expRaw, ok := claims["exp"]
	if !ok {
		return principal.Principal{}, fmt.Errorf("%w: exp claim missing", ErrInvalidAccessToken)
	}
	exp, ok := claimAsTime(expRaw)
	if !ok {
		return principal.Principal{}, fmt.Errorf("%w: malformed exp", ErrInvalidAccessToken)
	}
	if now().After(exp.Add(v.Leeway)) {
		return principal.Principal{}, fmt.Errorf("%w: expired", ErrInvalidAccessToken)
	}

	clientID, _ := claims["client_id"].(string)
	if clientID == "" {
		return principal.Principal{}, fmt.Errorf("%w: client_id claim missing", ErrInvalidAccessToken)
	}
	workspace, _ := claims["workspace"].(string)
	if workspace == "" {
		return principal.Principal{}, fmt.Errorf("%w: workspace claim missing", ErrInvalidAccessToken)
	}
	scope, _ := claims["scope"].(string)
	sub, _ := claims["sub"].(string)

	return principal.Principal{
		Type:        principal.TypeServiceAccount,
		ID:          clientID,
		WorkspaceID: workspace,
		Role:        roleFromScope(scope),
		Claims: map[string]string{
			"auth_method": "oauth_jwt",
			"client_id":   clientID,
			"oauth_sub":   sub,
			"scope":       scope,
		},
	}, nil
}

// roleFromScope maps an OAuth `scope` string to a Principal Role. The
// scope grammar is space-separated tokens (RFC 6749 §3.3); this
// matches simple per-token: "write" or "admin" promote, otherwise
// viewer.
func roleFromScope(scope string) principal.Role {
	for _, tok := range strings.Fields(scope) {
		switch tok {
		case "admin":
			return principal.RoleAdmin
		case "write":
			return principal.RoleEditor
		}
	}
	return principal.RoleViewer
}

func claimAsTime(raw any) (time.Time, bool) {
	switch v := raw.(type) {
	case float64:
		return time.Unix(int64(v), 0), true
	case int64:
		return time.Unix(v, 0), true
	case int:
		return time.Unix(int64(v), 0), true
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return time.Unix(i, 0), true
		}
	}
	return time.Time{}, false
}
