package main

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// signExpired builds a regular JWT that is already expired (for the exp branch).
func signExpired(t *testing.T, userID int64, role string) string {
	t.Helper()
	claims := Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jwtSecret)
	if err != nil {
		t.Fatalf("sign expired: %v", err)
	}
	return s
}

// TestRevalidateWSToken covers the periodic re-validation predicate (issue #82):
// temp revocation, temp/regular expiry, and the password-change boundary.
func TestRevalidateWSToken(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		"INSERT INTO users (username, password_hash, name, role, status) VALUES ('u','h','U','user','active')"); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// --- temp token: valid, then revoked ---
	linkID := "link-123"
	tempTok, err := generateTempLinkJWT(linkID, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("gen temp: %v", err)
	}
	tempClient := &wsClient{token: tempTok, isTemp: true, db: db}
	if err := revalidateWSToken(tempClient); err != nil {
		t.Fatalf("fresh temp token should be valid: %v", err)
	}
	linkStore.mu.Lock()
	linkStore.blacklist[linkID] = struct{}{}
	linkStore.mu.Unlock()
	if err := revalidateWSToken(tempClient); err == nil {
		t.Fatal("revoked temp token should be invalid")
	}

	// --- temp token expired ---
	expiredTemp, _ := generateTempLinkJWT("link-exp", time.Now().Add(-1*time.Hour))
	if err := revalidateWSToken(&wsClient{token: expiredTemp, isTemp: true, db: db}); err == nil {
		t.Fatal("expired temp token should be invalid")
	}

	// --- regular token: valid, then invalidated by password change ---
	regTok, err := generateJWT(1, "user")
	if err != nil {
		t.Fatalf("gen jwt: %v", err)
	}
	regClient := &wsClient{token: regTok, isTemp: false, db: db}
	if err := revalidateWSToken(regClient); err != nil {
		t.Fatalf("fresh regular token should be valid: %v", err)
	}
	// Advance boundary to the future so the just-issued token predates it.
	if _, err := db.ExecContext(ctx,
		"UPDATE users SET password_changed_at = '2099-01-01 00:00:00.000' WHERE id = 1"); err != nil {
		t.Fatalf("set boundary: %v", err)
	}
	if err := revalidateWSToken(regClient); err == nil {
		t.Fatal("token predating password change should be invalid")
	}

	// --- regular token expired ---
	if err := revalidateWSToken(&wsClient{token: signExpired(t, 1, "user"), isTemp: false, db: db}); err == nil {
		t.Fatal("expired regular token should be invalid")
	}
}
