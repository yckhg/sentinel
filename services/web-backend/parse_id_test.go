package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestApproveRejectStrictIDParsing locks the strict path-id parsing (#85): the
// approve/reject handlers must reject any userId that isn't a clean base-10
// integer with 400. fmt.Sscanf("%d") used to accept inputs like "5abc" or "5/9"
// as 5 (silently approving/rejecting user 5); strconv.ParseInt rejects them.
//
// Only invalid inputs are exercised — parsing happens before any DB access, so a
// nil *sql.DB is safe (a valid numeric id would proceed to the DB and is out of
// scope here).
func TestApproveRejectStrictIDParsing(t *testing.T) {
	jwtSecret = []byte("test-secret-strict-parse")
	token, err := generateJWT(1, "admin")
	if err != nil {
		t.Fatalf("generateJWT: %v", err)
	}

	handlers := map[string]http.HandlerFunc{
		"approve": handleApproveUser((*sql.DB)(nil)),
		"reject":  handleRejectUser((*sql.DB)(nil)),
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
