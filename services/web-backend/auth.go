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
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// JWT configuration
var jwtSecret []byte

func initJWTSecret() {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		// Generate a random secret if not configured
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			log.Fatalf("failed to generate JWT secret: %v", err)
		}
		secret = hex.EncodeToString(b)
		log.Println("WARNING: JWT_SECRET not set, using random secret (tokens will not survive restarts)")
	}
	jwtSecret = []byte(secret)
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

		result, err := db.Exec(
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

		var id int64
		var passwordHash, role, status string
		err := db.QueryRow(
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
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, AuthUser{
			UserID: claims.UserID,
			Role:   claims.Role,
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
	if adminPass == "" {
		adminPass = "sentinel1234"
	}

	var exists int
	err := db.QueryRow("SELECT COUNT(*) FROM users WHERE username = ?", adminUser).Scan(&exists)
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

	_, err = db.Exec(
		"INSERT INTO users (username, password_hash, name, role, status) VALUES (?, ?, ?, 'admin', 'active')",
		adminUser, string(hash), "Administrator",
	)
	if err != nil {
		return err
	}

	log.Printf("admin user created: username=%s", adminUser)
	return nil
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
