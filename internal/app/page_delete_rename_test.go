package app

import (
	"net/http"
	"testing"
)

func TestPageDeleteAndRenameRBAC(t *testing.T) {
	s := newOIDCStoreServer(t)
	admin := passwordLogin(t, s)
	viewer, _ := oidcLogin(t, s, "vic", "vic@example.com")
	if rr := do(t, s, admin, http.MethodPost, "/api/pages/old", editBody); rr.Code != http.StatusOK {
		t.Fatal(rr.Code)
	}
	if rr := do(t, s, viewer, http.MethodPost, "/api/pages/old/rename", map[string]string{"to": "new"}); rr.Code != http.StatusForbidden {
		t.Errorf("viewer rename = %d, want 403", rr.Code)
	}
	if rr := do(t, s, viewer, http.MethodDelete, "/api/pages/old", nil); rr.Code != http.StatusForbidden {
		t.Errorf("viewer delete = %d, want 403", rr.Code)
	}
	if rr := do(t, s, admin, http.MethodPost, "/api/pages/old/rename", map[string]string{"to": "New Name"}); rr.Code != http.StatusOK {
		t.Fatalf("rename = %d %s", rr.Code, rr.Body.String())
	}
	if rr := do(t, s, admin, http.MethodPost, "/api/pages/missing/rename", map[string]string{"to": "x"}); rr.Code != http.StatusNotFound {
		t.Errorf("rename missing = %d, want 404", rr.Code)
	}
	if rr := do(t, s, admin, http.MethodPost, "/api/pages/other", editBody); rr.Code != http.StatusOK {
		t.Fatal(rr.Code)
	}
	if rr := do(t, s, admin, http.MethodPost, "/api/workspaces/default/pages/other/rename", map[string]string{"to": "new-name"}); rr.Code != http.StatusConflict {
		t.Errorf("rename onto existing = %d, want 409", rr.Code)
	}
	if rr := do(t, s, admin, http.MethodDelete, "/api/pages/new-name", nil); rr.Code != http.StatusNoContent {
		t.Errorf("delete = %d", rr.Code)
	}
	if rr := do(t, s, admin, http.MethodDelete, "/api/pages/new-name", nil); rr.Code != http.StatusNotFound {
		t.Errorf("second delete = %d, want 404", rr.Code)
	}
}
