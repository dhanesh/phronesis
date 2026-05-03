package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// @constraint RT-2 — discovery doc carries every field MCP clients
// look up: issuer, authorization_endpoint, token_endpoint,
// registration_endpoint, jwks_uri, code_challenge_methods_supported.
func TestHandleOAuthMetadataReturnsExpectedFields(t *testing.T) {
	s, _ := newOAuthTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	rec := httptest.NewRecorder()
	s.handleOAuthMetadata(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, k := range []string{
		"issuer",
		"authorization_endpoint",
		"token_endpoint",
		"registration_endpoint",
		"jwks_uri",
		"response_types_supported",
		"grant_types_supported",
		"code_challenge_methods_supported",
		"token_endpoint_auth_methods_supported",
	} {
		if _, ok := doc[k]; !ok {
			t.Errorf("missing required field %q", k)
		}
	}
	if iss, _ := doc["issuer"].(string); iss != "https://phronesis.test" {
		t.Errorf("issuer = %q; want https://phronesis.test", iss)
	}
	if methods, _ := doc["code_challenge_methods_supported"].([]any); len(methods) != 1 || methods[0] != "S256" {
		t.Errorf("code_challenge_methods_supported = %v; want [S256]", methods)
	}
}

// @constraint RT-2 — JWKS endpoint serves a parseable JWK set with
// at least one RSA key carrying alg=RS256.
func TestHandleJWKSPublishesPublicKey(t *testing.T) {
	s, _ := newOAuthTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	rec := httptest.NewRecorder()
	s.handleJWKS(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q; want application/json", ct)
	}
	var doc struct {
		Keys []map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Keys) != 1 {
		t.Fatalf("expected 1 key in JWKS; got %d", len(doc.Keys))
	}
	for _, required := range []string{"kty", "kid", "alg", "n", "e"} {
		if doc.Keys[0][required] == "" {
			t.Errorf("missing required JWK field %q", required)
		}
	}
	if doc.Keys[0]["kty"] != "RSA" {
		t.Errorf("kty = %q; want RSA", doc.Keys[0]["kty"])
	}
	if doc.Keys[0]["alg"] != "RS256" {
		t.Errorf("alg = %q; want RS256", doc.Keys[0]["alg"])
	}
}
