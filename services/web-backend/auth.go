package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
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
	Username string `json:"username"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

type registerResponse struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
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
		if req.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
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
			"INSERT INTO users (username, password_hash, name) VALUES (?, ?, ?)",
			req.Username, string(hash), req.Name,
		)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "username already exists"})
				return
			}
			log.Printf("insert user error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		id, _ := result.LastInsertId()

		log.Printf("user registered: id=%d username=%s name=%s status=pending", id, req.Username, req.Name)

		writeJSON(w, http.StatusCreated, registerResponse{
			ID:       id,
			Username: req.Username,
			Name:     req.Name,
			Status:   "pending",
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
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		return jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, jwt.ErrSignatureInvalid
	}

	return claims, nil
}

// --- Auth middleware ---

func authMiddleware(next http.Handler) http.Handler {
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

		claims, err := parseJWT(parts[1])
		if err == nil {
			ctx := context.WithValue(r.Context(), userContextKey, AuthUser{
				UserID: claims.UserID,
				Role:   claims.Role,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Fallback: try temp link JWT for viewer access (read-only)
		tempClaims, tempErr := parseTempLinkJWT(parts[1])
		if tempErr != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
			return
		}

		// Check blacklist
		linkStore.mu.RLock()
		_, revoked := linkStore.blacklist[tempClaims.LinkID]
		linkStore.mu.RUnlock()
		if revoked {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "link has been revoked"})
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, AuthUser{
			UserID: 0,
			Role:   "viewer",
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
}

type approvalResponse struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

// requireAdmin extracts JWT from Authorization header and verifies admin role.
// Returns the AuthUser if valid admin, or writes error response and returns nil.
func requireAdmin(w http.ResponseWriter, r *http.Request) *AuthUser {
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

	if claims.Role != "admin" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
		return nil
	}

	return &AuthUser{UserID: claims.UserID, Role: claims.Role}
}

func handlePendingUsers(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if requireAdmin(w, r) == nil {
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		rows, err := db.QueryContext(ctx,
			"SELECT id, username, name, status, created_at FROM users WHERE status = 'pending' ORDER BY created_at ASC",
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
			if err := rows.Scan(&u.ID, &u.Username, &u.Name, &u.Status, &u.CreatedAt); err != nil {
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
		admin := requireAdmin(w, r)
		if admin == nil {
			return
		}

		userId := r.PathValue("userId")
		if userId == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "userId is required"})
			return
		}

		var parsedID int64
		if _, err := fmt.Sscanf(userId, "%d", &parsedID); err != nil {
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
		admin := requireAdmin(w, r)
		if admin == nil {
			return
		}

		userId := r.PathValue("userId")
		if userId == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "userId is required"})
			return
		}

		var parsedID int64
		if _, err := fmt.Sscanf(userId, "%d", &parsedID); err != nil {
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
		if requireAdmin(w, r) == nil {
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		rows, err := db.QueryContext(ctx,
			"SELECT id, username, name, role, status, created_at FROM users WHERE status = 'active' ORDER BY created_at ASC",
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
			Role      string `json:"role"`
			CreatedAt string `json:"createdAt"`
		}
		var users []activeUser
		for rows.Next() {
			var u activeUser
			var status string
			if err := rows.Scan(&u.ID, &u.Username, &u.Name, &u.Role, &status, &u.CreatedAt); err != nil {
				log.Printf("scan active user error: %v", err)
				continue
			}
			users = append(users, u)
		}

		writeJSON(w, http.StatusOK, users)
	}
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
