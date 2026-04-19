package oidc

import (
	"errors"
	"fmt"

	"github.com/dhanesh/phronesis/internal/principal"
)

// ClaimMapping is the per-provider configuration for translating OIDC id_token
// claims into a Principal. It is explicitly schema-versioned (S8) so a breaking
// claim-schema change in an IdP surfaces as a config error rather than silent
// user misattribution.
//
// Satisfies: RT-11.2, S8, pre-mortem #3 (IdP claim schema change)
type ClaimMapping struct {
	// SchemaVersion pins the interpretation of this mapping. Bumping this
	// value requires a corresponding server release that understands the new
	// schema. Unknown versions are rejected by Apply.
	SchemaVersion int

	// UserIDClaim names the claim whose value becomes Principal.ID.
	// Common values: "sub" (stable opaque id), "email", "upn", "preferred_username".
	UserIDClaim string

	// DisplayNameClaim is OPTIONAL. If present, surfaces as Claims["display_name"].
	DisplayNameClaim string

	// GroupsClaim is OPTIONAL. If present, the claim value (string or []string)
	// is split into Claims["groups"] as a comma-joined string for audit purposes.
	// v1 does NOT use groups for authorization (B2 role assignment is external).
	GroupsClaim string

	// RoleBinding is REQUIRED: given an id_token claim set, this function
	// returns the principal role to assign. Most deployments hard-code a role
	// for all authenticated users and manage elevations in-app; advanced
	// deployments can map IdP groups to roles here.
	RoleBinding func(claims map[string]any) principal.Role

	// WorkspaceBinding is REQUIRED: returns the workspace id this
	// principal is authenticating against. For single-workspace deployments
	// this is a constant; multi-workspace deployments read it from a claim.
	WorkspaceBinding func(claims map[string]any) string
}

// ErrUnsupportedSchema is returned when Apply is called with a mapping whose
// SchemaVersion is not 1.
var ErrUnsupportedSchema = errors.New("oidc: unsupported claim schema version")

// ErrMissingClaim is returned when a required claim is missing from the token.
var ErrMissingClaim = errors.New("oidc: required claim missing")

// CurrentSchemaVersion is the only schema this code understands today. Bumping
// it requires updating Apply and bumping server release version.
const CurrentSchemaVersion = 1

// Apply translates a validated id_token's claims into a Principal.
//
// Satisfies: RT-11.2, S8
//
// Returns ErrUnsupportedSchema for unknown SchemaVersion (per S8, this is the
// "IdP changed claim schema, server refuses to silently misattribute" path).
func (cm ClaimMapping) Apply(claims map[string]any) (principal.Principal, error) {
	if cm.SchemaVersion != CurrentSchemaVersion {
		return principal.Principal{}, fmt.Errorf("%w: got %d, supported %d", ErrUnsupportedSchema, cm.SchemaVersion, CurrentSchemaVersion)
	}
	if cm.UserIDClaim == "" {
		return principal.Principal{}, errors.New("oidc: ClaimMapping.UserIDClaim is required")
	}
	if cm.RoleBinding == nil || cm.WorkspaceBinding == nil {
		return principal.Principal{}, errors.New("oidc: ClaimMapping.RoleBinding and WorkspaceBinding are required")
	}

	userID, ok := stringClaim(claims, cm.UserIDClaim)
	if !ok || userID == "" {
		return principal.Principal{}, fmt.Errorf("%w: %q", ErrMissingClaim, cm.UserIDClaim)
	}

	out := principal.Principal{
		Type:        principal.TypeUser, // OIDC always authenticates humans; service accounts use API keys
		ID:          userID,
		WorkspaceID: cm.WorkspaceBinding(claims),
		Role:        cm.RoleBinding(claims),
		Claims:      map[string]string{},
	}
	if cm.DisplayNameClaim != "" {
		if v, ok := stringClaim(claims, cm.DisplayNameClaim); ok {
			out.Claims["display_name"] = v
		}
	}
	if cm.GroupsClaim != "" {
		if v, ok := claims[cm.GroupsClaim]; ok {
			out.Claims["groups"] = stringifyGroups(v)
		}
	}
	out.Claims["oidc_schema_version"] = fmt.Sprintf("%d", cm.SchemaVersion)
	return out, nil
}

func stringClaim(claims map[string]any, key string) (string, bool) {
	v, ok := claims[key]
	if !ok {
		return "", false
	}
	if s, ok := v.(string); ok {
		return s, true
	}
	return "", false
}

func stringifyGroups(v any) string {
	switch g := v.(type) {
	case string:
		return g
	case []any:
		out := ""
		for i, e := range g {
			if i > 0 {
				out += ","
			}
			if s, ok := e.(string); ok {
				out += s
			}
		}
		return out
	case []string:
		out := ""
		for i, e := range g {
			if i > 0 {
				out += ","
			}
			out += e
		}
		return out
	}
	return ""
}
