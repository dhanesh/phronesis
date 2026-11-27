package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/dhanesh/phronesis/internal/auth/oidc"
	"github.com/dhanesh/phronesis/internal/principal"
)

// Browser SSO against a real OIDC IdP (Dex in testing): authorization code
// + PKCE, state bound to the browser by a SameSite=Lax cookie, nonce
// checked in the id_token, RS256 over the IdP's JWKS (decision 2026-11-27,
// beta scope).
//
//	GET /api/auth/oidc/start?next=/w/page  -> 302 to the IdP
//	GET /api/auth/oidc/callback?code&state -> session cookie, 302 to next

const (
	ssoStateCookie = "phronesis_oidc_state"
	ssoFlowTTL     = 10 * time.Minute
	ssoMaxFlows    = 10000
	ssoClockLeeway = 2 * time.Minute
)

type ssoFlow struct {
	nonce, verifier, next string
	expires               time.Time
}

type ssoProvider struct {
	cfg Config

	mu      sync.Mutex
	rp      *oidc.RP
	adapter *oidc.Adapter
	flows   map[string]ssoFlow
}

func newSSOProvider(cfg Config) (*ssoProvider, error) {
	if !cfg.OIDCEnabled || cfg.OIDCClientID == "" {
		return nil, nil
	}
	if cfg.OIDCIssuer == "" {
		return nil, errors.New("oidc: PHRONESIS_OIDC_ISSUER is required with PHRONESIS_OIDC_CLIENT_ID")
	}
	if cfg.OIDCClientSecret == "" {
		return nil, errors.New("oidc: PHRONESIS_OIDC_CLIENT_SECRET is required with PHRONESIS_OIDC_CLIENT_ID")
	}
	if cfg.OIDCRedirectURL == "" {
		if cfg.PublicURL == "" {
			return nil, errors.New("oidc: set PHRONESIS_PUBLIC_URL (or PHRONESIS_OIDC_REDIRECT_URL) for SSO")
		}
		cfg.OIDCRedirectURL = strings.TrimRight(cfg.PublicURL, "/") + "/api/auth/oidc/callback"
	}
	if cfg.OIDCAudience == "" {
		cfg.OIDCAudience = cfg.OIDCClientID
	}
	return &ssoProvider{cfg: cfg, flows: map[string]ssoFlow{}}, nil
}

// ready returns the RP and RS256 adapter, running discovery on first use
// (and again after a failure), so the server starts before the IdP does.
func (p *ssoProvider) ready(ctx context.Context) (*oidc.RP, *oidc.Adapter, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rp != nil {
		return p.rp, p.adapter, nil
	}
	d, err := oidc.Discover(ctx, nil, p.cfg.OIDCIssuer, p.cfg.OIDCProviderURL)
	if err != nil {
		return nil, nil, err
	}
	ad, err := oidc.NewAdapter(oidc.Config{
		Issuer:   strings.TrimRight(p.cfg.OIDCIssuer, "/"),
		Audience: p.cfg.OIDCAudience,
		Verifier: &oidc.RS256Verifier{Cache: oidc.NewJWKSCache(d.JWKSURI, time.Hour)},
		Leeway:   ssoClockLeeway,
		ClaimMapping: oidc.ClaimMapping{
			SchemaVersion:    oidc.CurrentSchemaVersion,
			UserIDClaim:      "sub",
			DisplayNameClaim: "name",
			EmailClaim:       "email",
			GroupsClaim:      "groups",
			// A new user is a viewer; the real role is the users row
			// (decision 2026-11-27).
			RoleBinding:      func(map[string]any) principal.Role { return principal.RoleViewer },
			WorkspaceBinding: func(map[string]any) string { return defaultWorkspaceID },
		},
	})
	if err != nil {
		return nil, nil, err
	}
	p.rp = &oidc.RP{
		ClientID:     p.cfg.OIDCClientID,
		ClientSecret: p.cfg.OIDCClientSecret,
		RedirectURL:  p.cfg.OIDCRedirectURL,
		Discovery:    d,
	}
	p.adapter = ad
	slog.Info("oidc sso ready", slog.String("issuer", d.Issuer), slog.String("component", "phronesis"))
	return p.rp, p.adapter, nil
}

func (p *ssoProvider) putFlow(state string, f ssoFlow) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	if len(p.flows) >= ssoMaxFlows {
		for k, v := range p.flows {
			if now.After(v.expires) {
				delete(p.flows, k)
			}
		}
		if len(p.flows) >= ssoMaxFlows {
			return errors.New("too many pending sign-ins")
		}
	}
	p.flows[state] = f
	return nil
}

func (p *ssoProvider) takeFlow(state string) (ssoFlow, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f, ok := p.flows[state]
	delete(p.flows, state)
	if !ok || time.Now().After(f.expires) {
		return ssoFlow{}, false
	}
	return f, true
}

// safeNext keeps a post-login redirect on this site: a path starting with
// a single "/", no scheme, no host, no backslash tricks.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || strings.ContainsAny(next, "\r\n") {
		return "/"
	}
	return next
}

func (s *Server) handleSSOStart(w http.ResponseWriter, r *http.Request) {
	if s.sso == nil {
		http.Error(w, "sso not enabled", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	rp, _, err := s.sso.ready(r.Context())
	if err != nil {
		slog.Error("oidc discovery failed", slog.String("err", err.Error()), slog.String("component", "phronesis"))
		http.Redirect(w, r, "/?sso_error="+url.QueryEscape("The sign-in provider is not reachable. Try again shortly."), http.StatusFound)
		return
	}
	fp, err := oidc.NewFlowParams()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start sign-in")
		return
	}
	if err := s.sso.putFlow(fp.State, ssoFlow{
		nonce: fp.Nonce, verifier: fp.Verifier,
		next: safeNext(r.URL.Query().Get("next")), expires: time.Now().Add(ssoFlowTTL),
	}); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	// nosemgrep: cookie-missing-secure -- Secure is set on HTTPS via requestIsSecure(r)
	http.SetCookie(w, &http.Cookie{
		Name: ssoStateCookie, Value: fp.State, Path: "/api/auth/oidc/",
		MaxAge: int(ssoFlowTTL.Seconds()), HttpOnly: true,
		Secure: requestIsSecure(r), SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, rp.AuthCodeURL(fp), http.StatusFound)
}

func (s *Server) handleSSOCallback(w http.ResponseWriter, r *http.Request) {
	if s.sso == nil {
		http.Error(w, "sso not enabled", http.StatusNotFound)
		return
	}
	fail := func(reason, userMsg string) {
		s.auditEnqueue("auth.oidc_failed", r, "", map[string]string{"reason": reason})
		http.Redirect(w, r, "/?sso_error="+url.QueryEscape(userMsg), http.StatusFound)
	}
	q := r.URL.Query()
	state := q.Get("state")
	c, err := r.Cookie(ssoStateCookie)
	// Clear the state cookie whatever happens: it is single-use.
	http.SetCookie(w, &http.Cookie{Name: ssoStateCookie, Value: "", Path: "/api/auth/oidc/", MaxAge: -1, HttpOnly: true, Secure: requestIsSecure(r), SameSite: http.SameSiteLaxMode}) // nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure -- Secure on HTTPS (requestIsSecure)
	if err != nil || state == "" || c.Value != state {
		fail("state mismatch", "Sign-in expired or was started in another browser. Please try again.")
		return
	}
	flow, ok := s.sso.takeFlow(state)
	if !ok {
		fail("unknown state", "Sign-in expired. Please try again.")
		return
	}
	if e := q.Get("error"); e != "" {
		fail("idp error: "+e, "The sign-in provider refused the sign-in ("+e+").")
		return
	}
	rp, adapter, err := s.sso.ready(r.Context())
	if err != nil {
		fail("discovery", "The sign-in provider is not reachable. Try again shortly.")
		return
	}
	idToken, err := rp.Exchange(r.Context(), q.Get("code"), flow.verifier)
	if err != nil {
		slog.Warn("oidc code exchange failed", slog.String("err", err.Error()), slog.String("component", "phronesis"))
		fail("code exchange", "Sign-in failed. Please try again.")
		return
	}
	p, err := adapter.Authenticate(r.Context(), idToken)
	if err != nil {
		slog.Warn("oidc id_token rejected", slog.String("err", err.Error()), slog.String("component", "phronesis"))
		fail("invalid id_token", "Sign-in failed. Please try again.")
		return
	}
	if p.Claims["nonce"] != flow.nonce {
		fail("nonce mismatch", "Sign-in failed. Please try again.")
		return
	}
	delete(p.Claims, "nonce")
	if !s.establishOIDCSession(w, r, p) {
		return
	}
	// nosemgrep: open-redirect -- flow.next comes from safeNext: same-origin path only
	http.Redirect(w, r, flow.next, http.StatusFound)
}

// oidcAdapterFor returns the adapter that verifies id_tokens posted to
// /api/auth/oidc/login: the HMAC stub, or the real IdP's RS256 adapter.
func (s *Server) oidcAdapterFor(ctx context.Context) (*oidc.Adapter, error) {
	if s.oidc != nil {
		return s.oidc, nil
	}
	if s.sso != nil {
		_, ad, err := s.sso.ready(ctx)
		return ad, err
	}
	return nil, fmt.Errorf("oidc not enabled")
}
