package oidc

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
)

// RS256Signer signs JWT compact tokens using an RSA private key.
//
// Mirror image of RS256Verifier — the same key material can round-trip
// through Sign + Verify when the verifier's JWKSCache is seeded with
// the matching public key.
//
// Satisfies: RT-2 (OAuth 2.1 server can mint id/access tokens),
//
//	RT-14 (regulation-tagged spec compliance — RS256 is the default
//	       algorithm for OAuth 2.1 + OIDC).
//
// Concurrency: safe for concurrent use; the RSA key material is
// read-only after construction.
type RS256Signer struct {
	Key *rsa.PrivateKey
	Kid string
}

// NewRS256Signer constructs a signer. Returns an error if key is nil
// or kid is empty — both are required by the JWT spec when more than
// one signing key may be in rotation.
func NewRS256Signer(key *rsa.PrivateKey, kid string) (*RS256Signer, error) {
	if key == nil {
		return nil, errors.New("oidc: RS256Signer requires a private key")
	}
	if kid == "" {
		return nil, errors.New("oidc: RS256Signer requires a kid")
	}
	return &RS256Signer{Key: key, Kid: kid}, nil
}

// Sign produces a compact JWT (header.payload.signature) over claims.
// Header is constructed with alg=RS256, typ=JWT, kid=s.Kid.
//
// The caller is responsible for setting iat/exp/iss/aud/sub on claims.
// Sign does NOT inject defaults — the canonical signing surface stays
// declarative so audit + token introspection match what was minted.
func (s *RS256Signer) Sign(claims map[string]any) (string, error) {
	if s == nil || s.Key == nil {
		return "", errors.New("oidc: RS256Signer is nil or missing key")
	}
	header := map[string]string{
		"alg": "RS256",
		"typ": "JWT",
		"kid": s.Kid,
	}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("oidc: marshal header: %w", err)
	}
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("oidc: marshal claims: %w", err)
	}
	hb := base64.RawURLEncoding.EncodeToString(headerBytes)
	pb := base64.RawURLEncoding.EncodeToString(payloadBytes)
	signingInput := hb + "." + pb

	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.Key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("oidc: rsa sign: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// PublicJWK returns the signer's public key formatted as a single JWK
// suitable for inclusion in a .well-known/jwks.json document. Use
// (*RS256Signer).JWKSDocument() to produce the wrapping {"keys":[...]}
// envelope.
//
// Satisfies: RT-2 (resource server publishes JWKS so MCP clients can
//
//	verify access tokens issued by this phronesis instance).
func (s *RS256Signer) PublicJWK() PublicJWK {
	pub := s.Key.PublicKey
	return PublicJWK{
		Kty: "RSA",
		Use: "sig",
		Alg: "RS256",
		Kid: s.Kid,
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

// JWKSDocument returns the marshalled {"keys":[<public-jwk>]} payload
// served at /.well-known/jwks.json.
func (s *RS256Signer) JWKSDocument() ([]byte, error) {
	doc := struct {
		Keys []PublicJWK `json:"keys"`
	}{Keys: []PublicJWK{s.PublicJWK()}}
	return json.Marshal(doc)
}

// PublicJWK is the JSON-encoded public-key shape served from
// /.well-known/jwks.json. It mirrors the input shape the existing
// RS256Verifier accepts so a verifier pointed at this instance's
// JWKS URI can validate self-issued tokens.
type PublicJWK struct {
	Kty string `json:"kty"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	Kid string `json:"kid,omitempty"`
	N   string `json:"n"`
	E   string `json:"e"`
}
