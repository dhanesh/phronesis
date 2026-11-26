package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/dhanesh/phronesis/internal/principal"
)

// fakeIdP serves discovery, JWKS and a token endpoint. The issuer it
// advertises is publicIssuer (what browsers see), not its own URL, to
// exercise the back-channel rewrite.
type fakeIdP struct {
	srv          *httptest.Server
	signer       *RS256Signer
	publicIssuer string
	nonce        string
	challenge    string
}

func newFakeIdP(t *testing.T, publicIssuer string) *fakeIdP {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	signer, _ := NewRS256Signer(key, "k1")
	f := &fakeIdP{signer: signer, publicIssuer: publicIssuer}
	mux := http.NewServeMux()
	mux.HandleFunc("/dex/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 f.publicIssuer,
			"authorization_endpoint": f.publicIssuer + "/auth",
			"token_endpoint":         f.publicIssuer + "/token",
			"jwks_uri":               f.publicIssuer + "/keys",
		})
	})
	mux.HandleFunc("/dex/keys", func(w http.ResponseWriter, r *http.Request) {
		b, _ := signer.JWKSDocument()
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/dex/token", func(w http.ResponseWriter, r *http.Request) {
		u, p, _ := r.BasicAuth()
		_ = r.ParseForm()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if u != "phronesis" || p != "s3cret" || r.PostForm.Get("code") != "the-code" ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		now := time.Now().Unix()
		tok, _ := signer.Sign(map[string]any{
			"iss": f.publicIssuer, "aud": []string{"phronesis"}, "sub": "CiRkZXgtdXNlcg",
			"email": "ann@example.com", "name": "Ann", "nonce": f.nonce, "iat": now, "exp": now + 300,
		})
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": tok, "token_type": "bearer"})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func TestRPDiscoverRewritesBackchannelAndRejectsIssuerMismatch(t *testing.T) {
	f := newFakeIdP(t, "http://localhost:5556/dex")
	d, err := Discover(context.Background(), nil, "http://localhost:5556/dex", f.srv.URL+"/dex")
	if err != nil {
		t.Fatal(err)
	}
	if d.AuthorizationEndpoint != "http://localhost:5556/dex/auth" {
		t.Errorf("authorization endpoint must stay browser-facing: %s", d.AuthorizationEndpoint)
	}
	if d.TokenEndpoint != f.srv.URL+"/dex/token" || d.JWKSURI != f.srv.URL+"/dex/keys" {
		t.Errorf("back-channel endpoints not rewritten: %+v", d)
	}
	if _, err := Discover(context.Background(), nil, "http://other/dex", f.srv.URL+"/dex"); err == nil {
		t.Error("issuer mismatch accepted")
	}
}

func TestRPCodeFlowEndToEnd(t *testing.T) {
	f := newFakeIdP(t, "http://localhost:5556/dex")
	d, err := Discover(context.Background(), nil, "http://localhost:5556/dex", f.srv.URL+"/dex")
	if err != nil {
		t.Fatal(err)
	}
	rp := &RP{ClientID: "phronesis", ClientSecret: "s3cret", RedirectURL: "http://localhost:8080/api/auth/oidc/callback", Discovery: d}
	fp, _ := NewFlowParams()
	u, _ := url.Parse(rp.AuthCodeURL(fp))
	q := u.Query()
	if q.Get("state") != fp.State || q.Get("nonce") != fp.Nonce || q.Get("code_challenge_method") != "S256" ||
		!strings.Contains(q.Get("scope"), "openid") {
		t.Fatalf("auth URL = %s", u)
	}
	f.nonce, f.challenge = fp.Nonce, q.Get("code_challenge")

	if _, err := rp.Exchange(context.Background(), "the-code", "wrong-verifier"); err == nil {
		t.Fatal("exchange with a wrong PKCE verifier succeeded")
	}
	idt, err := rp.Exchange(context.Background(), "the-code", fp.Verifier)
	if err != nil {
		t.Fatal(err)
	}
	ad, err := NewAdapter(Config{
		Issuer: "http://localhost:5556/dex", Audience: "phronesis",
		Verifier: &RS256Verifier{Cache: NewJWKSCache(d.JWKSURI, time.Hour)},
		Leeway:   time.Minute,
		ClaimMapping: ClaimMapping{
			SchemaVersion: CurrentSchemaVersion, UserIDClaim: "sub", DisplayNameClaim: "name", EmailClaim: "email",
			RoleBinding:      func(map[string]any) principal.Role { return principal.RoleViewer },
			WorkspaceBinding: func(map[string]any) string { return "default" },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := ad.Authenticate(context.Background(), idt)
	if err != nil {
		t.Fatal(err)
	}
	if p.Claims["nonce"] != fp.Nonce || p.Claims["email"] != "ann@example.com" || p.Claims["display_name"] != "Ann" {
		t.Errorf("claims = %v", p.Claims)
	}
}
