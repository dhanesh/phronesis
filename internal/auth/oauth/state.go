// Package oauth implements the storage substrate for the OAuth 2.1
// authorization-code-with-PKCE flow.
//
// State held here:
//
//   - Registered clients (RFC 7591 dynamic client registration).
//     Public clients (PKCE-only, no client_secret) — the only kind
//     Stage 3a supports.
//   - Pending authorization codes. Single-use, TTL = 10 minutes
//     (RFC 6749 §4.1.2 recommends ≤10 min).
//   - Refresh tokens. Single-use rotation (RFC 6749 §6 — recommended;
//     Stage 3a issues fresh refresh tokens on each /token exchange).
//
// All state is in-memory. Persistence behind SQLite is a Stage 3b
// concern; the in-process map is a rational floor for the common
// case (single replica, the manifold's T3 / B3 boundary).
//
// Satisfies: RT-2 (OAuth 2.1 substrate), RT-14 (validated input at
//
//	authorize/token boundaries — see handlers_oauth.go).
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Errors returned by Store. Callers translate to OAuth error codes
// (invalid_grant, invalid_client, etc.) at the handler boundary.
var (
	ErrUnknownClient = errors.New("oauth: unknown client_id")
	ErrUnknownCode   = errors.New("oauth: unknown or expired authorization code")
	ErrCodeUsed      = errors.New("oauth: authorization code already redeemed")
	ErrUnknownToken  = errors.New("oauth: unknown or expired refresh token")
	ErrPKCEMismatch  = errors.New("oauth: PKCE code_verifier does not match challenge")
	ErrPKCEUnknown   = errors.New("oauth: PKCE code_challenge_method must be S256")
)

// CodeTTL is the maximum lifetime of an authorization code. RFC 6749
// recommends ≤10 minutes. We pick the upper bound — a real human + a
// browser flow rarely takes longer than 10 minutes.
const CodeTTL = 10 * time.Minute

// RefreshTokenTTL is the lifetime of a refresh token. 30 days is the
// pragmatic floor for AI-agent clients that may sit idle for days.
// Refresh tokens rotate on each use (single-use), so the practical
// risk of a stolen 30-day token is bounded by the next-use window.
const RefreshTokenTTL = 30 * 24 * time.Hour

// Client is a registered OAuth client. Public clients (PKCE-only)
// are the only kind Stage 3a supports — there is no client_secret
// because the MCP authorization profile mandates PKCE for public
// clients (Claude Code, browser-based clients, native CLIs).
//
// RFC 7591 fields preserved verbatim where possible so a future
// /oauth/register response can echo the canonical metadata.
type Client struct {
	ID              string    `json:"client_id"`
	Name            string    `json:"client_name,omitempty"`
	RedirectURIs    []string  `json:"redirect_uris"`
	GrantTypes      []string  `json:"grant_types"`
	ResponseTypes   []string  `json:"response_types"`
	TokenAuthMethod string    `json:"token_endpoint_auth_method"`
	Scope           string    `json:"scope,omitempty"`
	CreatedAt       time.Time `json:"client_id_issued_at"`
}

// AuthorizationCode is a pending authorization-code grant minted by
// /oauth/authorize and redeemed by /oauth/token.
//
// Holds the PKCE challenge (S256) and the per-request scope/redirect
// so /token can verify them against the client's submission. iat +
// expires_at fix the single-use single-redeem window.
type AuthorizationCode struct {
	Code                string
	ClientID            string
	UserSub             string // OIDC sub of the authenticated human
	WorkspaceID         string
	RedirectURI         string // exact match required at /token
	Scope               string
	CodeChallenge       string // base64url(SHA256(verifier))
	CodeChallengeMethod string // always "S256" — others rejected at /authorize
	IssuedAt            time.Time
	ExpiresAt           time.Time
}

// RefreshToken backs grant_type=refresh_token. Single-use rotation is
// applied at the handler layer; the store provides Consume to enforce.
type RefreshToken struct {
	Token       string
	ClientID    string
	UserSub     string
	WorkspaceID string
	Scope       string
	IssuedAt    time.Time
	ExpiresAt   time.Time
}

// Store is the in-memory OAuth state store. Concurrency-safe; all
// methods take an internal lock.
//
// Nil receivers are safe — every method short-circuits to a zero
// result + appropriate error so wiring code does not need to nil-
// check before every call.
type Store struct {
	mu      sync.Mutex
	clients map[string]Client
	codes   map[string]AuthorizationCode
	tokens  map[string]RefreshToken
	now     func() time.Time
}

// NewStore builds an empty store. now defaults to time.Now when nil
// — kept injectable for tests.
func NewStore(now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{
		clients: make(map[string]Client),
		codes:   make(map[string]AuthorizationCode),
		tokens:  make(map[string]RefreshToken),
		now:     now,
	}
}

// RegisterClient assigns a fresh client_id and persists the client.
// id is generated unless explicitly set on c.ID — the tests set it
// to keep assertions deterministic; production callers leave it empty.
func (s *Store) RegisterClient(_ context.Context, c Client) (Client, error) {
	if s == nil {
		return Client{}, errors.New("oauth: nil Store")
	}
	if len(c.RedirectURIs) == 0 {
		return Client{}, errors.New("oauth: at least one redirect_uri required")
	}
	if c.ID == "" {
		id, err := randomID("phr_oauth_client_")
		if err != nil {
			return Client{}, err
		}
		c.ID = id
	}
	if len(c.GrantTypes) == 0 {
		c.GrantTypes = []string{"authorization_code", "refresh_token"}
	}
	if len(c.ResponseTypes) == 0 {
		c.ResponseTypes = []string{"code"}
	}
	if c.TokenAuthMethod == "" {
		c.TokenAuthMethod = "none" // public client — PKCE only
	}
	c.CreatedAt = s.now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[c.ID] = c
	return c, nil
}

// Client returns the registered client for id.
func (s *Store) Client(id string) (Client, error) {
	if s == nil {
		return Client{}, ErrUnknownClient
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.clients[id]
	if !ok {
		return Client{}, ErrUnknownClient
	}
	return c, nil
}

// MintCode persists a new authorization code with TTL = CodeTTL. The
// caller fills out everything except IssuedAt/ExpiresAt/Code (which
// the store generates). Returns the populated code so the handler can
// emit it in the redirect.
func (s *Store) MintCode(c AuthorizationCode) (AuthorizationCode, error) {
	if s == nil {
		return AuthorizationCode{}, errors.New("oauth: nil Store")
	}
	if c.CodeChallengeMethod != "S256" {
		return AuthorizationCode{}, ErrPKCEUnknown
	}
	code, err := randomID("phr_code_")
	if err != nil {
		return AuthorizationCode{}, err
	}
	now := s.now().UTC()
	c.Code = code
	c.IssuedAt = now
	c.ExpiresAt = now.Add(CodeTTL)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes[code] = c
	return c, nil
}

// ConsumeCode looks up code, verifies PKCE, removes it from the store
// (single-use), and returns it. Verifier is the raw code_verifier the
// client submits; it is hashed (SHA256, base64url RAW) and compared
// to the stored challenge.
//
// Returns ErrUnknownCode for unknown/expired entries (the same error
// for both — clients should not learn whether a code ever existed).
func (s *Store) ConsumeCode(code, clientID, redirectURI, verifier string) (AuthorizationCode, error) {
	if s == nil {
		return AuthorizationCode{}, ErrUnknownCode
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.codes[code]
	if !ok {
		return AuthorizationCode{}, ErrUnknownCode
	}
	// Always remove on lookup — replay attempts MUST NOT succeed even
	// if the second attempt has different (correct) parameters.
	delete(s.codes, code)

	if s.now().After(c.ExpiresAt) {
		return AuthorizationCode{}, ErrUnknownCode
	}
	if c.ClientID != clientID {
		return AuthorizationCode{}, ErrUnknownCode
	}
	if c.RedirectURI != redirectURI {
		return AuthorizationCode{}, ErrUnknownCode
	}
	if !pkceVerify(verifier, c.CodeChallenge) {
		return AuthorizationCode{}, ErrPKCEMismatch
	}
	return c, nil
}

// MintRefreshToken persists a new refresh token. Returns the populated
// token (with Token + IssuedAt + ExpiresAt filled in).
func (s *Store) MintRefreshToken(t RefreshToken) (RefreshToken, error) {
	if s == nil {
		return RefreshToken{}, errors.New("oauth: nil Store")
	}
	tok, err := randomID("phr_refresh_")
	if err != nil {
		return RefreshToken{}, err
	}
	now := s.now().UTC()
	t.Token = tok
	t.IssuedAt = now
	t.ExpiresAt = now.Add(RefreshTokenTTL)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[tok] = t
	return t, nil
}

// ConsumeRefreshToken looks up the token, removes it (single-use
// rotation per RFC 6749 §6 recommendation), and returns it. Caller
// mints a fresh refresh token alongside the new access token.
func (s *Store) ConsumeRefreshToken(tok, clientID string) (RefreshToken, error) {
	if s == nil {
		return RefreshToken{}, ErrUnknownToken
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tokens[tok]
	if !ok {
		return RefreshToken{}, ErrUnknownToken
	}
	delete(s.tokens, tok)
	if s.now().After(t.ExpiresAt) {
		return RefreshToken{}, ErrUnknownToken
	}
	if t.ClientID != clientID {
		return RefreshToken{}, ErrUnknownToken
	}
	return t, nil
}

// Cleanup drops codes + refresh tokens that have aged past their
// expiry. Intended for periodic invocation; not strictly required
// because lookup paths re-check expiry, but bounds memory growth
// in the face of abandoned flows.
func (s *Store) Cleanup() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, c := range s.codes {
		if now.After(c.ExpiresAt) {
			delete(s.codes, k)
		}
	}
	for k, t := range s.tokens {
		if now.After(t.ExpiresAt) {
			delete(s.tokens, k)
		}
	}
}

// pkceVerify hashes verifier with SHA-256, base64url-encodes (raw, no
// padding) and compares against the stored challenge.
//
// Satisfies: RT-2 (PKCE S256), regulation-tag T1 (MCP authorization
//
//	profile mandates code_challenge_method=S256).
func pkceVerify(verifier, challenge string) bool {
	if verifier == "" || challenge == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return got == challenge
}

// randomID returns prefix + 32 base32 characters of crypto-random
// entropy (160 bits — well above the OWASP recommendation for opaque
// tokens). base32 stdlib keeps the encoding URL- and copy-safe.
func randomID(prefix string) (string, error) {
	var raw [20]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("oauth: random: %w", err)
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])
	return prefix + strings.ToLower(enc), nil
}
