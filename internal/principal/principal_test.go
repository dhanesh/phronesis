package principal

import (
	"context"
	"errors"
	"testing"
)

// @constraint RT-5 S1 TN8
// The same Principal type cleanly represents both users and service accounts,
// and the Type field distinguishes them for audit purposes.
func TestPrincipalTypeDistinguishesUserVsServiceAccount(t *testing.T) {
	user := Principal{Type: TypeUser, ID: "alice", WorkspaceID: "ws", Role: RoleEditor}
	sa := Principal{Type: TypeServiceAccount, ID: "sa-bot", WorkspaceID: "ws", Role: RoleEditor}

	if !user.IsUser() || user.IsServiceAccount() {
		t.Error("user Principal misclassified")
	}
	if !sa.IsServiceAccount() || sa.IsUser() {
		t.Error("service account Principal misclassified")
	}
	// Same role, same abilities; only Type differs (TN8: unified authz path).
	if user.Can("write") != sa.Can("write") {
		t.Error("same role should grant same abilities regardless of Type")
	}
}

// @constraint RT-5 B2 TN8
// The three RBAC roles implement a strict inclusion ladder:
//   viewer  can read
//   editor  can read + write
//   admin   can read + write + admin
func TestRBACRoleLadder(t *testing.T) {
	tests := []struct {
		role   Role
		read   bool
		write  bool
		admin  bool
	}{
		{RoleViewer, true, false, false},
		{RoleEditor, true, true, false},
		{RoleAdmin, true, true, true},
	}
	for _, tt := range tests {
		p := Principal{Type: TypeUser, WorkspaceID: "ws", Role: tt.role}
		if got := p.Can("read"); got != tt.read {
			t.Errorf("role %s can read: got %v, want %v", tt.role, got, tt.read)
		}
		if got := p.Can("write"); got != tt.write {
			t.Errorf("role %s can write: got %v, want %v", tt.role, got, tt.write)
		}
		if got := p.Can("admin"); got != tt.admin {
			t.Errorf("role %s can admin: got %v, want %v", tt.role, got, tt.admin)
		}
	}
}

// @constraint RT-5
// A principal without a workspace id cannot authorize anything, even as admin.
// This closes a footgun: forgotten WorkspaceID must not silently grant access.
func TestPrincipalWithoutWorkspaceDeniesAll(t *testing.T) {
	p := Principal{Type: TypeUser, ID: "alice", Role: RoleAdmin /* no WorkspaceID */}
	for _, a := range []string{"read", "write", "admin"} {
		if p.Can(a) {
			t.Errorf("principal with empty WorkspaceID should not allow %q", a)
		}
	}
}

// @constraint RT-5
// FromContext returns ErrNotAuthenticated when no principal attached.
func TestFromContextMissing(t *testing.T) {
	_, err := FromContext(context.Background())
	if !errors.Is(err, ErrNotAuthenticated) {
		t.Errorf("expected ErrNotAuthenticated, got %v", err)
	}
}

// @constraint RT-5
// WithPrincipal + FromContext round-trip preserves all fields.
func TestWithPrincipalFromContextRoundTrip(t *testing.T) {
	in := Principal{
		Type:        TypeServiceAccount,
		ID:          "sa-1",
		WorkspaceID: "ws-docs",
		Role:        RoleEditor,
		Claims:      map[string]string{"label": "ci-bot"},
	}
	ctx := WithPrincipal(context.Background(), in)
	out, err := FromContext(ctx)
	if err != nil {
		t.Fatalf("FromContext: %v", err)
	}
	if out.Type != in.Type || out.ID != in.ID || out.WorkspaceID != in.WorkspaceID || out.Role != in.Role {
		t.Errorf("round-trip mismatch: got %+v, want %+v", out, in)
	}
	if out.Claims["label"] != "ci-bot" {
		t.Errorf("claims lost in round-trip")
	}
}

// @constraint RT-5
// Require enforces workspace-scope: a principal authenticated against workspace
// A must not be usable on workspace B.
func TestRequireEnforcesWorkspaceScope(t *testing.T) {
	ctx := WithPrincipal(context.Background(), Principal{
		Type: TypeUser, ID: "alice", WorkspaceID: "ws-A", Role: RoleEditor,
	})
	_, err := Require(ctx, "ws-B")
	if !errors.Is(err, ErrWrongWorkspace) {
		t.Errorf("expected ErrWrongWorkspace, got %v", err)
	}
	p, err := Require(ctx, "ws-A")
	if err != nil {
		t.Errorf("expected success for matching workspace, got %v", err)
	}
	if p.ID != "alice" {
		t.Errorf("wrong principal returned")
	}
}
