package app

import (
	"strings"
	"testing"
)

func TestValidateProduction(t *testing.T) {
	ok := Config{Env: "production", AdminPassword: "a-real-secret"}
	if err := validateProduction(ok); err != nil {
		t.Fatalf("good production config refused: %v", err)
	}
	if err := validateProduction(Config{AdminPassword: defaultAdminPassword}); err != nil {
		t.Errorf("dev config refused: %v", err)
	}
	err := validateProduction(Config{Env: "production", AdminPassword: defaultAdminPassword})
	if err == nil || !strings.Contains(err.Error(), "PHRONESIS_ADMIN_PASSWORD") {
		t.Errorf("default password in production: %v", err)
	}
	err = validateProduction(Config{Env: "production", AdminPassword: "x-secret", OIDCEnabled: true, OIDCSecret: "s"})
	if err == nil || !strings.Contains(err.Error(), "stub") {
		t.Errorf("stub OIDC in production: %v", err)
	}
	if err := validateProduction(Config{Env: "production", AdminPassword: "x-secret", OIDCEnabled: true, OIDCClientID: "c"}); err != nil {
		t.Errorf("real OIDC in production refused: %v", err)
	}
}
