package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestApproveRejectStrictIDParsing locks the strict path-id parsing (#85): the
// approve/reject handlers must reject any userId that isn't a clean base-10
// integer with 400. fmt.Sscanf("%d") used to accept inputs like "5abc" or "5/9"
// as 5 (silently approving/rejecting user 5); strconv.ParseInt rejects them.
//
// A real (seeded) DB is used rather than a nil *sql.DB: requireAdmin now runs a
// password_changed_at boundary query (#83) on every admin verification, before
// the parse logic, so the handler must reach a live DB to get past auth. The
// seeded admin (id=1) has a NULL boundary, so the freshly minted admin token is
// valid and the request reaches — and is rejected by — the strict-ID parsing.
func TestApproveRejectStrictIDParsing(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.Exec(
		"INSERT INTO users (id, username, password_hash, name, role, status) VALUES (1, 'admin', 'h', 'Admin', 'admin', 'active')",
	); err != nil {
		t.Fatalf("seed admin user: %v", err)
	}

	token, err := generateJWT(1, "admin")
	if err != nil {
		t.Fatalf("generateJWT: %v", err)
	}

	handlers := map[string]http.HandlerFunc{
		"approve": handleApproveUser(db),
		"reject":  handleRejectUser(db),
	}

	// Inputs the old fmt.Sscanf(%d) parse would silently accept as an int (or
	// otherwise mis-handle). strconv.ParseInt must reject every one with 400.
	badIDs := []string{"5abc", "5/9", "5 ", " 5", "007x", "0x5", "3.0", "abc", "5\n", "12 34", ""}

	for name, h := range handlers {
		for _, id := range badIDs {
			req := httptest.NewRequest(http.MethodPost, "/auth/"+name+"/x", nil)
			req.SetPathValue("userId", id)
			req.Header.Set("Authorization", "Bearer "+token)
			rr := httptest.NewRecorder()
			h(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("%s userId=%q: got status %d, want 400", name, id, rr.Code)
			}
		}
	}
}
