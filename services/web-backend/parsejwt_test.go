package main

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TestParseJWTPinsAlgHS256 locks the algorithm-pinning hardening (#89): parseJWT
// must accept only HS256-signed tokens (the algorithm generateJWT emits) and
// reject anything else — a different HMAC size, alg=none, or a whitespace-padded
// token — rather than trusting the token header's declared alg.
func TestParseJWTPinsAlgHS256(t *testing.T) {
	jwtSecret = []byte("test-secret-alg-pin")

	claims := Claims{
		UserID: 7,
		Role:   "user",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	// Valid HS256 token round-trips.
	valid, err := generateJWT(7, "user")
	if err != nil {
		t.Fatalf("generateJWT: %v", err)
	}
	got, err := parseJWT(valid)
	if err != nil {
		t.Fatalf("valid HS256 token rejected: %v", err)
	}
	if got.UserID != 7 || got.Role != "user" {
		t.Errorf("claims mismatch: got userID=%d role=%q", got.UserID, got.Role)
	}

	// HS512-signed token (same secret) must be rejected — alg not in the allow list.
	hs512, err := jwt.NewWithClaims(jwt.SigningMethodHS512, claims).SignedString(jwtSecret)
	if err != nil {
		t.Fatalf("sign HS512: %v", err)
	}
	if _, err := parseJWT(hs512); err == nil {
		t.Error("HS512 token accepted; signing algorithm is not pinned")
	}

	// alg=none must be rejected.
	none, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none: %v", err)
	}
	if _, err := parseJWT(none); err == nil {
		t.Error("alg=none token accepted")
	}

	// A whitespace-padded token must not silently pass (no implicit trim).
	if _, err := parseJWT(" " + valid + " "); err == nil {
		t.Error("whitespace-padded token accepted")
	}
}
