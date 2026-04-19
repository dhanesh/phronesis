package oidc

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

// StubProvider is an in-process HS256-based OIDC token issuer intended for
// CI and development use. It implements the "stub provider passing
// integration tests" commitment from TN5 / RT-11.3.
//
// DO NOT use in production: HS256 with a shared secret is not a real OIDC
// deployment. Production replaces HMACVerifier with an RS256 verifier backed
// by the IdP's JWKS.
type StubProvider struct {
	Issuer   string
	Audience string
	Secret   []byte
	Kid      string // embedded in header; included so rotation tests work
	Clock    func() time.Time
}

// NewStubProvider constructs a provider. Secret should be at least 32 bytes;
// test code typically uses a random fixture. Kid defaults to "stub-1".
func NewStubProvider(issuer, audience string, secret []byte) *StubProvider {
	return &StubProvider{
		Issuer:   issuer,
		Audience: audience,
		Secret:   secret,
		Kid:      "stub-1",
		Clock:    time.Now,
	}
}

// Claims carries the payload fields callers can set; unset fields are omitted.
type Claims struct {
	Subject   string
	Email     string
	Name      string
	Groups    []string
	TTL       time.Duration
	NotBefore time.Duration // relative to now
	Extra     map[string]any
}

// Issue returns an HS256-signed id_token with the given claims.
//
// Satisfies: RT-11.3 (stub provider full flow in CI)
func (p *StubProvider) Issue(c Claims) (string, error) {
	if p.Clock == nil {
		p.Clock = time.Now
	}
	if c.TTL == 0 {
		c.TTL = time.Hour
	}
	now := p.Clock()
	header := map[string]string{
		"alg": "HS256",
		"typ": "JWT",
		"kid": p.Kid,
	}
	payload := map[string]any{
		"iss": p.Issuer,
		"aud": p.Audience,
		"iat": now.Unix(),
		"exp": now.Add(c.TTL).Unix(),
	}
	if c.NotBefore != 0 {
		payload["nbf"] = now.Add(c.NotBefore).Unix()
	}
	if c.Subject != "" {
		payload["sub"] = c.Subject
	}
	if c.Email != "" {
		payload["email"] = c.Email
	}
	if c.Name != "" {
		payload["name"] = c.Name
	}
	if len(c.Groups) > 0 {
		payload["groups"] = c.Groups
	}
	for k, v := range c.Extra {
		payload[k] = v
	}
	return p.sign(header, payload)
}

// Verifier returns an HMACVerifier paired with this provider's secret.
// Wire this into Adapter.cfg.Verifier for CI-only flows.
func (p *StubProvider) Verifier() Verifier { return &HMACVerifier{Secret: p.Secret} }

// RotateSecret replaces the signing secret and bumps the kid. Tests exercise
// this to verify the adapter's JWKS-rotation-compatible flow rejects tokens
// signed with the old secret.
func (p *StubProvider) RotateSecret(newSecret []byte, newKid string) {
	if newKid == "" {
		return
	}
	p.Secret = newSecret
	p.Kid = newKid
}

func (p *StubProvider) sign(header map[string]string, payload map[string]any) (string, error) {
	if len(p.Secret) == 0 {
		return "", errors.New("oidc: stub provider secret is empty")
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	pb, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	headerB64 := enc.EncodeToString(hb)
	payloadB64 := enc.EncodeToString(pb)
	signingInput := headerB64 + "." + payloadB64

	mac := hmac.New(sha256.New, p.Secret)
	mac.Write([]byte(signingInput))
	sig := mac.Sum(nil)
	sigB64 := enc.EncodeToString(sig)

	return signingInput + "." + sigB64, nil
}
