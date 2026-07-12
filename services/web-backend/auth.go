package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const dbTimeout = 5 * time.Second

func dbCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, dbTimeout)
}

// JWT configuration
var jwtSecret []byte

const jwtSecretFile = "/data/.jwt-secret"

func initJWTSecret() {
	secret := os.Getenv("JWT_SECRET")
	if secret != "" {
		jwtSecret = []byte(secret)
		return
	}

	// Try to read persisted secret from file
	if data, err := os.ReadFile(jwtSecretFile); err == nil && len(data) > 0 {
		secret = strings.TrimSpace(string(data))
		if secret != "" {
			jwtSecret = []byte(secret)
			log.Println("WARNING: Using auto-generated JWT secret from file. Set JWT_SECRET env var for production.")
			return
		}
	}

	// Generate a new random secret and persist it
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("failed to generate JWT secret: %v", err)
	}
	secret = hex.EncodeToString(b)

	if err := os.WriteFile(jwtSecretFile, []byte(secret), 0600); err != nil {
		log.Printf("WARNING: could not persist JWT secret to %s: %v", jwtSecretFile, err)
	}

	jwtSecret = []byte(secret)
	log.Println("WARNING: Using auto-generated JWT secret. Set JWT_SECRET env var for production.")
}

// JWT claims
type Claims struct {
	UserID int64  `json:"userId"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// Context key for authenticated user
type contextKey string

const userContextKey contextKey = "user"

type AuthUser struct {
	UserID int64
	Role   string
}

// --- Request/Response types ---

type registerRequest struct {
	Username        string `json:"username"`
	Password        string `json:"password"`
	ConfirmPassword string `json:"confirmPassword"`
	Name            string `json:"name"`
	InviteToken     string `json:"inviteToken"`
}

type registerResponse struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Status   string `json:"status"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token string    `json:"token"`
	User  loginUser `json:"user"`
}

type loginUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// --- Handlers ---

func handleRegister(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req registerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		req.Username = strings.TrimSpace(req.Username)
		req.Name = strings.TrimSpace(req.Name)
		inviteToken := strings.TrimSpace(req.InviteToken)

		if req.Username == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username is required"})
			return
		}
		if req.Password == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password is required"})
			return
		}
		if len(req.Password) < 8 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
			return
		}
		if req.Password != req.ConfirmPassword {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "비밀번호가 일치하지 않습니다"})
			return
		}
		if req.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}

		// Check if invite token is valid — auto-approve if so
		autoApprove := inviteToken != "" && isValidInviteToken(db, r, inviteToken)
		status := "pending"
		var email *string
		if autoApprove {
			status = "active"
			if e := getInvitationEmail(db, r, inviteToken); e != "" {
				email = &e
			}
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			log.Printf("bcrypt error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		result, err := db.ExecContext(ctx,
			"INSERT INTO users (username, password_hash, name, status, email) VALUES (?, ?, ?, ?, ?)",
			req.Username, string(hash), req.Name, status, email,
		)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed: users.email") {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "email already registered"})
				return
			}
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "username already exists"})
				return
			}
			log.Printf("insert user error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		id, _ := result.LastInsertId()

		// Mark invitation as accepted
		if autoApprove {
			acceptInvitation(db, r, inviteToken)
		}

		var emailStr string
		if email != nil {
			emailStr = *email
		}
		log.Printf("user registered: id=%d username=%s name=%s status=%s email=%s", id, req.Username, req.Name, status, maskEmail(emailStr))

		writeJSON(w, http.StatusCreated, registerResponse{
			ID:       id,
			Username: req.Username,
			Name:     req.Name,
			Email:    emailStr,
			Status:   status,
		})
	}
}

func handleLogin(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		req.Username = strings.TrimSpace(req.Username)

		if req.Username == "" || req.Password == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username and password are required"})
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		var id int64
		var passwordHash, role, status string
		err := db.QueryRowContext(ctx,
			"SELECT id, password_hash, role, status FROM users WHERE username = ?",
			req.Username,
		).Scan(&id, &passwordHash, &role, &status)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid username or password"})
			return
		}
		if err != nil {
			log.Printf("login query error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid username or password"})
			return
		}

		if status != "active" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "account pending approval"})
			return
		}

		token, err := generateJWT(id, role)
		if err != nil {
			log.Printf("jwt generation error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		log.Printf("user logged in: id=%d username=%s role=%s", id, req.Username, role)

		writeJSON(w, http.StatusOK, loginResponse{
			Token: token,
			User: loginUser{
				ID:       id,
				Username: req.Username,
				Role:     role,
			},
		})
	}
}

// --- JWT helpers ---

func generateJWT(userID int64, role string) (string, error) {
	claims := Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

func parseJWT(tokenString string) (*Claims, error) {
	// Pin the accepted signing algorithm to HS256 (the algorithm generateJWT
	// uses). Without WithValidMethods the parser trusts the token header's alg,
	// which is the classic algorithm-confusion foothold; pinning it is defensive
	// best practice even with a symmetric key.
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		return jwtSecret, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, jwt.ErrSignatureInvalid
	}

	return claims, nil
}

// --- Credential-change boundary (issue #83, assertion Q2) ---

// dbTimeLayouts are the formats a persisted timestamp may arrive in. SQLite's
// strftime('%Y-%m-%d %H:%M:%f','now') yields "YYYY-MM-DD HH:MM:SS.sss"; datetime()
// yields second precision; and the modernc.org/sqlite driver auto-parses DATETIME
// columns and hands them back in RFC3339 ("...T...Z"). All are UTC. We try each so
// the credential boundary compares correctly regardless of read path.
var dbTimeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
}

// parseDBTime parses a SQLite UTC datetime string into a time.Time (UTC).
func parseDBTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range dbTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// iatBeforeBoundary reports whether a token issued at iat predates the credential
// boundary. iat has second granularity (JWT numeric date), so it is the floor of
// the real login instant; the boundary is stored with sub-second precision and is
// strictly after any earlier login, making float comparison robust even when a
// login and a subsequent password change fall inside the same wall-clock second.
func iatBeforeBoundary(iat, boundary time.Time) bool {
	if boundary.IsZero() {
		return false
	}
	return float64(iat.Unix()) < float64(boundary.UnixNano())/1e9
}

// tokenInvalidatedByPasswordChange reports whether the token (identified by its
// owning userID and iat) was issued before that user's password_changed_at
// boundary. A NULL/absent boundary (user never changed password) never rejects,
// so unchanged-password tokens survive to expiry (assertion Q).
func tokenInvalidatedByPasswordChange(ctx context.Context, db *sql.DB, userID int64, iat time.Time) bool {
	var boundaryStr sql.NullString
	err := db.QueryRowContext(ctx, "SELECT password_changed_at FROM users WHERE id = ?", userID).Scan(&boundaryStr)
	if err != nil || !boundaryStr.Valid {
		return false
	}
	boundary, ok := parseDBTime(boundaryStr.String)
	if !ok {
		return false
	}
	return iatBeforeBoundary(iat, boundary)
}

// --- Auth middleware ---

func authMiddleware(db *sql.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authorization header required"})
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid authorization header format"})
			return
		}
		tokenStr := parts[1]

		// Identify temp-link tokens FIRST. Both token kinds are signed with the
		// same secret, so the regular parser (parseJWT) accepts a temp token as a
		// role-less user (userId=0, role="") — which would bypass both the
		// revocation (blacklist) check and the temp read-only scope. A temp token
		// is distinguished by carrying a non-empty linkId claim.
		if tempClaims, err := parseTempLinkJWT(tokenStr); err == nil && tempClaims.LinkID != "" {
			// Enforce revocation on every /api/* request (not just verify).
			linkStore.mu.RLock()
			_, revoked := linkStore.blacklist[tempClaims.LinkID]
			linkStore.mu.RUnlock()
			if revoked {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "link has been revoked"})
				return
			}
			// Temp links are read-only CCTV viewers — restrict scope.
			if !tempScopeAllowed(r) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "temp link is read-only"})
				return
			}
			ctx := context.WithValue(r.Context(), userContextKey, AuthUser{
				UserID: 0,
				Role:   "temp",
			})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Regular user/admin JWT.
		claims, err := parseJWT(tokenStr)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
			return
		}
		// Reject tokens lacking a real user identity or a known role (e.g. a
		// malformed or temp-shaped token that slipped past detection) so they
		// cannot ride through user-level routes with role="".
		if claims.UserID == 0 || (claims.Role != "admin" && claims.Role != "user") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
			return
		}

		// Credential-change boundary: reject any token issued before the owner's
		// password_changed_at (assertion Q2, issue #83).
		bctx, bcancel := dbCtx(r.Context())
		invalidated := tokenInvalidatedByPasswordChange(bctx, db, claims.UserID, claims.IssuedAt.Time)
		bcancel()
		if invalidated {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "token invalidated by password change"})
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, AuthUser{
			UserID: claims.UserID,
			Role:   claims.Role,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// tempScopeAllowed reports whether a temp-link (read-only CCTV viewer) token may
// access the requested route. Temp viewers may only GET the camera list and
// recording playback — everything else (incidents, contacts, equipment restart,
// archives, admin resources, and all mutating methods) is denied.
func tempScopeAllowed(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	p := r.URL.Path
	if p == "/api/cameras" {
		return true
	}
	if strings.HasPrefix(p, "/api/recordings/") {
		return true
	}
	return false
}

func getAuthUser(r *http.Request) AuthUser {
	user, _ := r.Context().Value(userContextKey).(AuthUser)
	return user
}

// --- Admin seeding ---

func seedAdminUser(db *sql.DB) error {
	adminUser := os.Getenv("ADMIN_USERNAME")
	adminPass := os.Getenv("ADMIN_PASSWORD")
	if adminUser == "" {
		adminUser = "admin"
	}
	if adminPass == "" || adminPass == "sentinel1234" {
		if adminPass == "" {
			adminPass = "sentinel1234"
		}
		log.Printf("WARNING: Using default admin password. Set ADMIN_PASSWORD env var for production.")
	}
	if len(adminPass) < 8 {
		log.Printf("WARNING: Admin password is shorter than 8 characters. Use a stronger password for production.")
	}

	ctx, cancel := dbCtx(context.Background())
	defer cancel()

	var exists int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE username = ?", adminUser).Scan(&exists)
	if err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(adminPass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	_, err = db.ExecContext(ctx,
		"INSERT INTO users (username, password_hash, name, role, status) VALUES (?, ?, ?, 'admin', 'active')",
		adminUser, string(hash), "Administrator",
	)
	if err != nil {
		return err
	}

	log.Printf("admin user created: username=%s", adminUser)
	return nil
}

// --- Admin approval handlers ---

type pendingUserResponse struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
}

type approvalResponse struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

// requireAdmin extracts JWT from Authorization header and verifies admin role.
// Returns the AuthUser if valid admin, or writes error response and returns nil.
func requireAdmin(db *sql.DB, w http.ResponseWriter, r *http.Request) *AuthUser {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authorization header required"})
		return nil
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid authorization header format"})
		return nil
	}

	claims, err := parseJWT(parts[1])
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
		return nil
	}

	// Credential-change boundary also applies to the direct-verification paths
	// (/auth/pending|approve|reject|users) — a pre-change admin token is 401.
	bctx, bcancel := dbCtx(r.Context())
	invalidated := tokenInvalidatedByPasswordChange(bctx, db, claims.UserID, claims.IssuedAt.Time)
	bcancel()
	if invalidated {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "token invalidated by password change"})
		return nil
	}

	if claims.Role != "admin" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
		return nil
	}

	return &AuthUser{UserID: claims.UserID, Role: claims.Role}
}

func handlePendingUsers(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if requireAdmin(db, w, r) == nil {
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		rows, err := db.QueryContext(ctx,
			"SELECT id, username, name, COALESCE(email, '') AS email, status, created_at FROM users WHERE status = 'pending' ORDER BY created_at ASC",
		)
		if err != nil {
			log.Printf("query pending users error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		defer rows.Close()

		users := []pendingUserResponse{}
		for rows.Next() {
			var u pendingUserResponse
			if err := rows.Scan(&u.ID, &u.Username, &u.Name, &u.Email, &u.Status, &u.CreatedAt); err != nil {
				log.Printf("scan pending user error: %v", err)
				continue
			}
			users = append(users, u)
		}

		writeJSON(w, http.StatusOK, users)
	}
}

func handleApproveUser(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		admin := requireAdmin(db, w, r)
		if admin == nil {
			return
		}

		userId := r.PathValue("userId")
		if userId == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "userId is required"})
			return
		}

		parsedID, err := strconv.ParseInt(userId, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid userId"})
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		result, err := db.ExecContext(ctx,
			"UPDATE users SET status = 'active' WHERE id = ? AND status = 'pending'",
			parsedID,
		)
		if err != nil {
			log.Printf("approve user error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}

		log.Printf("user approved: id=%d by admin=%d", parsedID, admin.UserID)
		writeJSON(w, http.StatusOK, approvalResponse{ID: parsedID, Status: "active"})
	}
}

func handleRejectUser(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		admin := requireAdmin(db, w, r)
		if admin == nil {
			return
		}

		userId := r.PathValue("userId")
		if userId == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "userId is required"})
			return
		}

		parsedID, err := strconv.ParseInt(userId, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid userId"})
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		result, err := db.ExecContext(ctx,
			"UPDATE users SET status = 'rejected' WHERE id = ? AND status = 'pending'",
			parsedID,
		)
		if err != nil {
			log.Printf("reject user error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}

		log.Printf("user rejected: id=%d by admin=%d", parsedID, admin.UserID)
		writeJSON(w, http.StatusOK, approvalResponse{ID: parsedID, Status: "rejected"})
	}
}

func handleActiveUsers(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if requireAdmin(db, w, r) == nil {
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		rows, err := db.QueryContext(ctx,
			"SELECT id, username, name, COALESCE(email, '') AS email, role, created_at FROM users WHERE status = 'active' ORDER BY created_at ASC",
		)
		if err != nil {
			log.Printf("query active users error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		defer rows.Close()

		type activeUser struct {
			ID        int64  `json:"id"`
			Username  string `json:"username"`
			Name      string `json:"name"`
			Email     string `json:"email"`
			Role      string `json:"role"`
			CreatedAt string `json:"createdAt"`
		}
		var users []activeUser
		for rows.Next() {
			var u activeUser
			if err := rows.Scan(&u.ID, &u.Username, &u.Name, &u.Email, &u.Role, &u.CreatedAt); err != nil {
				log.Printf("scan active user error: %v", err)
				continue
			}
			users = append(users, u)
		}

		writeJSON(w, http.StatusOK, users)
	}
}

// --- Password change ---

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func handleChangePassword(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)
		if user.UserID == 0 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}

		var req changePasswordRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if req.CurrentPassword == "" || req.NewPassword == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "currentPassword and newPassword are required"})
			return
		}

		if len(req.NewPassword) < 8 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "new password must be at least 8 characters"})
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		var passwordHash string
		err := db.QueryRowContext(ctx,
			"SELECT password_hash FROM users WHERE id = ?",
			user.UserID,
		).Scan(&passwordHash)
		if err != nil {
			log.Printf("change password query error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.CurrentPassword)); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "current password is incorrect"})
			return
		}

		newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			log.Printf("bcrypt error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		// Advance the credential-change boundary to now (millisecond precision) so
		// every token issued before this instant — including the one used for this
		// request — is rejected on its next authenticated call (assertion Q2, #83).
		_, err = db.ExecContext(ctx,
			"UPDATE users SET password_hash = ?, password_changed_at = strftime('%Y-%m-%d %H:%M:%f','now') WHERE id = ?",
			string(newHash), user.UserID,
		)
		if err != nil {
			log.Printf("update password error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		log.Printf("password changed: userId=%d", user.UserID)
		writeJSON(w, http.StatusOK, map[string]string{"message": "password changed successfully"})
	}
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
