package oidc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dhanesh/phronesis/internal/principal"
)

// Config is a per-provider adapter configuration.
//
// Satisfies: RT-11, S4, S8
type Config struct {
	Issuer       string           // must match id_token "iss" claim (S4)
	Audience     string           // must match id_token "aud" claim (S4)
	Verifier     Verifier         // signature verification strategy (RT-11.1)
	ClaimMapping ClaimMapping     // id_token -> Principal translation (RT-11.2)
	Clock        func() time.Time // injectable for tests; defaults to time.Now
	Leeway       time.Duration    // allowed clock skew on exp/nbf; default 0
}

// Adapter is the OIDC authentication entry point.
type Adapter struct {
	cfg Config
}

// NewAdapter validates Config and returns a ready adapter.
func NewAdapter(cfg Config) (*Adapter, error) {
	if cfg.Issuer == "" || cfg.Audience == "" {
		return nil, errors.New("oidc: Config.Issuer and Config.Audience are required")
	}
	if cfg.Verifier == nil {
		return nil, errors.New("oidc: Config.Verifier is required")
	}
	if cfg.ClaimMapping.SchemaVersion == 0 {
		return nil, errors.New("oidc: Config.ClaimMapping.SchemaVersion is required")
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &Adapter{cfg: cfg}, nil
}

// ErrInvalidToken is the umbrella error for any malformed or untrustworthy
// token. Callers should not surface detail beyond this to end users.
var ErrInvalidToken = errors.New("oidc: invalid token")

// Authenticate validates a signed id_token and returns the resulting
// Principal. Validates, in order:
//  1. JWT compact shape (3 base64url segments)
//  2. Alg + kid from header (rejects alg=none)
//  3. Signature via cfg.Verifier
//  4. exp (not expired) and nbf (already valid, if present)
//  5. iss matches Config.Issuer (S4)
//  6. aud matches Config.Audience (S4)
//  7. ClaimMapping.Apply to build Principal (RT-11.2, S8)
//
// Note on iat: RFC 7519 makes iat optional and the Verifier can't meaningfully
// enforce "issued at" bounds without a policy (e.g., reject tokens older than
// N minutes). That policy is intentionally deferred — add it via a Config
// field (MaxAge?) when a real IdP integration needs it.
//
// Satisfies: RT-11, S4, S8, S3 (no bypass for expired tokens)
func (a *Adapter) Authenticate(_ctx context.Context, idToken string) (principal.Principal, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return principal.Principal{}, fmt.Errorf("%w: not a compact JWT", ErrInvalidToken)
	}
	headerB64, payloadB64, sigB64 := parts[0], parts[1], parts[2]

	headerBytes, err := base64.RawURLEncoding.DecodeString(headerB64)
	if err != nil {
		return principal.Principal{}, fmt.Errorf("%w: header decode: %v", ErrInvalidToken, err)
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return principal.Principal{}, fmt.Errorf("%w: payload decode: %v", ErrInvalidToken, err)
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return principal.Principal{}, fmt.Errorf("%w: signature decode: %v", ErrInvalidToken, err)
	}

	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return principal.Principal{}, fmt.Errorf("%w: header json: %v", ErrInvalidToken, err)
	}
	if header.Alg == "" || header.Alg == "none" {
		return principal.Principal{}, fmt.Errorf("%w: missing or 'none' alg", ErrInvalidToken)
	}

	signingInput := []byte(headerB64 + "." + payloadB64)
	if err := a.cfg.Verifier.Verify(header.Alg, header.Kid, signingInput, sigBytes); err != nil {
		return principal.Principal{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	var claims map[string]any
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return principal.Principal{}, fmt.Errorf("%w: payload json: %v", ErrInvalidToken, err)
	}

	if err := a.validateTemporal(claims); err != nil {
		return principal.Principal{}, err
	}
	if err := a.validateIssuerAudience(claims); err != nil {
		return principal.Principal{}, err
	}

	return a.cfg.ClaimMapping.Apply(claims)
}

func (a *Adapter) validateTemporal(claims map[string]any) error {
	now := a.cfg.Clock()
	if expRaw, ok := claims["exp"]; ok {
		exp := asTime(expRaw)
		if exp.IsZero() {
			return fmt.Errorf("%w: malformed exp", ErrInvalidToken)
		}
		if now.After(exp.Add(a.cfg.Leeway)) {
			return fmt.Errorf("%w: token expired at %s", ErrInvalidToken, exp.Format(time.RFC3339))
		}
	} else {
		return fmt.Errorf("%w: exp claim missing", ErrInvalidToken)
	}
	if nbfRaw, ok := claims["nbf"]; ok {
		nbf := asTime(nbfRaw)
		if nbf.IsZero() {
			return fmt.Errorf("%w: malformed nbf", ErrInvalidToken)
		}
		if now.Before(nbf.Add(-a.cfg.Leeway)) {
			return fmt.Errorf("%w: token not yet valid (nbf)", ErrInvalidToken)
		}
	}
	return nil
}

func (a *Adapter) validateIssuerAudience(claims map[string]any) error {
	iss, ok := claims["iss"].(string)
	if !ok || iss != a.cfg.Issuer {
		return fmt.Errorf("%w: iss mismatch (got %v)", ErrInvalidToken, claims["iss"])
	}
	// aud may be a string or []any
	switch aud := claims["aud"].(type) {
	case string:
		if aud != a.cfg.Audience {
			return fmt.Errorf("%w: aud mismatch", ErrInvalidToken)
		}
	case []any:
		ok := false
		for _, v := range aud {
			if s, is := v.(string); is && s == a.cfg.Audience {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("%w: aud does not contain %q", ErrInvalidToken, a.cfg.Audience)
		}
	default:
		return fmt.Errorf("%w: aud missing or wrong type", ErrInvalidToken)
	}
	return nil
}

func asTime(raw any) time.Time {
	switch v := raw.(type) {
	case float64:
		return time.Unix(int64(v), 0)
	case int64:
		return time.Unix(v, 0)
	case int:
		return time.Unix(int64(v), 0)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return time.Unix(i, 0)
		}
	}
	return time.Time{}
}
