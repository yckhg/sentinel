package main

import (
	"context"
	"testing"
	"time"
)

// TestIatBeforeBoundary checks the pure iat/boundary comparison, including the
// same-wall-clock-second login-then-change case that second-granular iat could
// otherwise let slip through.
func TestIatBeforeBoundary(t *testing.T) {
	base := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name     string
		iat      time.Time
		boundary time.Time
		want     bool
	}{
		{"no boundary", base, time.Time{}, false},
		{"iat well before boundary", base, base.Add(1 * time.Hour), true},
		{"iat after boundary", base.Add(1 * time.Hour), base, false},
		{
			// login iat floored to :00; password change 500ms later same second.
			name:     "same second, change after login",
			iat:      base, // second-granular floor
			boundary: base.Add(500 * time.Millisecond),
			want:     true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := iatBeforeBoundary(c.iat, c.boundary); got != c.want {
				t.Fatalf("iatBeforeBoundary=%v want %v", got, c.want)
			}
		})
	}
}

// TestTokenInvalidatedByPasswordChange verifies the DB-backed boundary: NULL
// boundary never rejects (assertion Q); after change-password sets the boundary,
// a pre-change token is rejected while another user's token is unaffected.
func TestTokenInvalidatedByPasswordChange(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// user U (id will be 1) and V (id 2)
	if _, err := db.ExecContext(ctx,
		"INSERT INTO users (username, password_hash, name, role, status) VALUES ('u','h','U','user','active'),('v','h','V','user','active')"); err != nil {
		t.Fatalf("seed users: %v", err)
	}

	preChange := time.Now().UTC().Add(-1 * time.Minute)

	// No boundary yet → both valid (assertion Q).
	if tokenInvalidatedByPasswordChange(ctx, db, 1, preChange) {
		t.Fatal("U token should be valid before any password change")
	}

	// Simulate change-password for U: set boundary to now.
	if _, err := db.ExecContext(ctx,
		"UPDATE users SET password_changed_at = strftime('%Y-%m-%d %H:%M:%f','now') WHERE id = 1"); err != nil {
		t.Fatalf("set boundary: %v", err)
	}

	// U's pre-change token now rejected.
	if !tokenInvalidatedByPasswordChange(ctx, db, 1, preChange) {
		t.Fatal("U pre-change token should be invalidated")
	}
	// A token U obtains after the change (iat = now+1s) stays valid.
	if tokenInvalidatedByPasswordChange(ctx, db, 1, time.Now().UTC().Add(1*time.Second)) {
		t.Fatal("U post-change token should be valid")
	}
	// V unaffected.
	if tokenInvalidatedByPasswordChange(ctx, db, 2, preChange) {
		t.Fatal("V token should be unaffected by U's change")
	}
}
