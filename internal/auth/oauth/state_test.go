package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"
)

func challengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// @constraint RT-2 — RegisterClient assigns a fresh client_id and
// preserves RFC 7591 metadata.
func TestRegisterClientPopulatesDefaults(t *testing.T) {
	s := NewStore(nil)
	c, err := s.RegisterClient(context.Background(), Client{
		Name:         "claude-code",
		RedirectURIs: []string{"http://localhost/cb"},
	})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	if c.ID == "" {
		t.Fatal("client_id should be populated")
	}
	if c.TokenAuthMethod != "none" {
		t.Errorf("token_endpoint_auth_method = %q; want none (PKCE-only public client)", c.TokenAuthMethod)
	}
	if got := c.GrantTypes; len(got) != 2 || got[0] != "authorization_code" || got[1] != "refresh_token" {
		t.Errorf("default grant_types = %v; want [authorization_code refresh_token]", got)
	}
	if c.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestRegisterClientRequiresRedirectURI(t *testing.T) {
	s := NewStore(nil)
	_, err := s.RegisterClient(context.Background(), Client{})
	if err == nil {
		t.Fatal("expected error when no redirect_uris provided")
	}
}

// @constraint RT-2 — codes are single-use; even a correct second
// redemption MUST fail.
func TestConsumeCodeIsSingleUse(t *testing.T) {
	s := NewStore(nil)
	verifier := "test-verifier-1234567890abcdef"
	code, err := s.MintCode(AuthorizationCode{
		ClientID:            "c1",
		UserSub:             "user-1",
		WorkspaceID:         "default",
		RedirectURI:         "http://x/cb",
		Scope:               "read",
		CodeChallenge:       challengeFor(verifier),
		CodeChallengeMethod: "S256",
	})
	if err != nil {
		t.Fatalf("MintCode: %v", err)
	}
	if _, err := s.ConsumeCode(code.Code, "c1", "http://x/cb", verifier); err != nil {
		t.Fatalf("first ConsumeCode: %v", err)
	}
	if _, err := s.ConsumeCode(code.Code, "c1", "http://x/cb", verifier); !errors.Is(err, ErrUnknownCode) {
		t.Errorf("second ConsumeCode err = %v; want ErrUnknownCode (replay)", err)
	}
}

// @constraint RT-2 — expired codes return ErrUnknownCode and do
// NOT leak whether the code ever existed.
func TestConsumeCodeRejectsExpired(t *testing.T) {
	now := time.Now()
	clock := &mockClock{t: now}
	s := NewStore(clock.Now)
	verifier := "v"
	code, _ := s.MintCode(AuthorizationCode{
		ClientID: "c1", RedirectURI: "/cb",
		CodeChallenge: challengeFor(verifier), CodeChallengeMethod: "S256",
	})
	clock.t = now.Add(CodeTTL + time.Second)
	if _, err := s.ConsumeCode(code.Code, "c1", "/cb", verifier); !errors.Is(err, ErrUnknownCode) {
		t.Errorf("expected ErrUnknownCode for expired, got %v", err)
	}
}

// @constraint RT-2 — wrong PKCE verifier surfaces as ErrPKCEMismatch
// (distinct from ErrUnknownCode so handler logs are auditable).
func TestConsumeCodeRejectsBadPKCE(t *testing.T) {
	s := NewStore(nil)
	verifier := "right"
	code, _ := s.MintCode(AuthorizationCode{
		ClientID: "c1", RedirectURI: "/cb",
		CodeChallenge: challengeFor(verifier), CodeChallengeMethod: "S256",
	})
	if _, err := s.ConsumeCode(code.Code, "c1", "/cb", "wrong"); !errors.Is(err, ErrPKCEMismatch) {
		t.Errorf("expected ErrPKCEMismatch, got %v", err)
	}
}

// @constraint RT-2 — only S256 is accepted at MintCode. Plain method
// rejection prevents downgrade.
func TestMintCodeRejectsNonS256Challenge(t *testing.T) {
	s := NewStore(nil)
	_, err := s.MintCode(AuthorizationCode{
		ClientID: "c1", RedirectURI: "/cb",
		CodeChallenge: "literal", CodeChallengeMethod: "plain",
	})
	if !errors.Is(err, ErrPKCEUnknown) {
		t.Errorf("expected ErrPKCEUnknown for plain method, got %v", err)
	}
}

// @constraint RT-2 — redirect_uri exact match enforced. A client
// that registered https://x/cb cannot redeem with /cb.
func TestConsumeCodeRequiresExactRedirectURIMatch(t *testing.T) {
	s := NewStore(nil)
	verifier := "v"
	code, _ := s.MintCode(AuthorizationCode{
		ClientID: "c1", RedirectURI: "https://x/cb",
		CodeChallenge: challengeFor(verifier), CodeChallengeMethod: "S256",
	})
	if _, err := s.ConsumeCode(code.Code, "c1", "https://x/cb/", verifier); !errors.Is(err, ErrUnknownCode) {
		t.Errorf("expected ErrUnknownCode for trailing-slash mismatch, got %v", err)
	}
}

// @constraint RT-2 — refresh tokens rotate on each use.
func TestConsumeRefreshTokenIsSingleUse(t *testing.T) {
	s := NewStore(nil)
	rt, _ := s.MintRefreshToken(RefreshToken{ClientID: "c1", UserSub: "u", Scope: "read"})
	if _, err := s.ConsumeRefreshToken(rt.Token, "c1"); err != nil {
		t.Fatalf("first ConsumeRefreshToken: %v", err)
	}
	if _, err := s.ConsumeRefreshToken(rt.Token, "c1"); !errors.Is(err, ErrUnknownToken) {
		t.Errorf("second ConsumeRefreshToken err = %v; want ErrUnknownToken", err)
	}
}

func TestCleanupRemovesExpiredCodes(t *testing.T) {
	now := time.Now()
	clock := &mockClock{t: now}
	s := NewStore(clock.Now)

	verifier := "v"
	c, _ := s.MintCode(AuthorizationCode{
		ClientID: "c1", RedirectURI: "/cb",
		CodeChallenge: challengeFor(verifier), CodeChallengeMethod: "S256",
	})
	if _, ok := s.codes[c.Code]; !ok {
		t.Fatal("code should be present after Mint")
	}
	clock.t = now.Add(CodeTTL + time.Hour)
	s.Cleanup()
	if _, ok := s.codes[c.Code]; ok {
		t.Error("expired code should have been swept by Cleanup")
	}
}

// @constraint RT-2 — concurrent operations are race-free. Run under
// `go test -race` to assert.
func TestStoreConcurrentAccessIsRaceFree(t *testing.T) {
	s := NewStore(nil)
	c, _ := s.RegisterClient(context.Background(), Client{RedirectURIs: []string{"/cb"}})
	verifier := "v"

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, err := s.MintCode(AuthorizationCode{
				ClientID: c.ID, RedirectURI: "/cb",
				CodeChallenge: challengeFor(verifier), CodeChallengeMethod: "S256",
			})
			if err != nil {
				return
			}
			_, _ = s.ConsumeCode(code.Code, c.ID, "/cb", verifier)
		}()
	}
	wg.Wait()
}

func TestNilStoreIsSafe(t *testing.T) {
	var s *Store
	if _, err := s.Client("anything"); !errors.Is(err, ErrUnknownClient) {
		t.Errorf("nil Client() = %v; want ErrUnknownClient", err)
	}
	if _, err := s.ConsumeCode("c", "id", "/cb", "v"); !errors.Is(err, ErrUnknownCode) {
		t.Errorf("nil ConsumeCode() = %v; want ErrUnknownCode", err)
	}
	if _, err := s.ConsumeRefreshToken("t", "id"); !errors.Is(err, ErrUnknownToken) {
		t.Errorf("nil ConsumeRefreshToken() = %v; want ErrUnknownToken", err)
	}
	s.Cleanup() // must not panic
}

type mockClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *mockClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
