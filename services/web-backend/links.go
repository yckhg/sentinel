package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TempLinkClaims for temporary CCTV access tokens
type TempLinkClaims struct {
	LinkID string `json:"linkId"`
	jwt.RegisteredClaims
}

// TempLink represents an active temporary link in memory
type TempLink struct {
	ID        string    `json:"id"`
	Token     string    `json:"token,omitempty"`
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type tempLinkStore struct {
	mu        sync.RWMutex
	links     map[string]*TempLink // keyed by UUID
	blacklist map[string]struct{}  // revoked token JTIs (linkIDs)
}

var linkStore = &tempLinkStore{
	links:     make(map[string]*TempLink),
	blacklist: make(map[string]struct{}),
}

// startLinkCleanup runs a background goroutine that periodically removes
// expired temporary links and their corresponding blacklist entries.
func startLinkCleanup() {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			linkStore.mu.Lock()
			removedLinks := 0
			removedBlacklist := 0
			for id, link := range linkStore.links {
				if link.ExpiresAt.Before(now) {
					delete(linkStore.links, id)
					removedLinks++
					if _, ok := linkStore.blacklist[id]; ok {
						delete(linkStore.blacklist, id)
						removedBlacklist++
					}
				}
			}
			linkStore.mu.Unlock()
			if removedLinks > 0 || removedBlacklist > 0 {
				log.Printf("link cleanup: removed %d expired links, %d blacklist entries", removedLinks, removedBlacklist)
			}
		}
	}()
}

func generateUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Printf("uuid generation error: %v", err)
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	// Format as UUID v4
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func generateTempLinkJWT(linkID string, expiresAt time.Time) (string, error) {
	claims := TempLinkClaims{
		LinkID: linkID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

func parseTempLinkJWT(tokenString string) (*TempLinkClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &TempLinkClaims{}, func(token *jwt.Token) (any, error) {
		return jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*TempLinkClaims)
	if !ok || !token.Valid {
		return nil, jwt.ErrSignatureInvalid
	}
	return claims, nil
}

func getFrontendURL() string {
	u := os.Getenv("FRONTEND_URL")
	if u == "" {
		u = "http://localhost:3080"
	}
	return strings.TrimRight(u, "/")
}

// handleCreateTempLink handles POST /api/links/temp
// Accepts admin JWT or internal service calls (no auth)
func handleCreateTempLink() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check auth: if Authorization header present, must be valid admin
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
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
			if claims.Role != "admin" {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
				return
			}
		}
		// No auth header = internal service call (Docker network isolation)

		var req struct {
			Label string `json:"label"`
		}
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
				return
			}
		}

		linkID := generateUUID()
		expiresAt := time.Now().Add(24 * time.Hour)

		token, err := generateTempLinkJWT(linkID, expiresAt)
		if err != nil {
			log.Printf("temp link JWT generation error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		link := &TempLink{
			ID:        linkID,
			Token:     token,
			Label:     req.Label,
			CreatedAt: time.Now(),
			ExpiresAt: expiresAt,
		}

		linkStore.mu.Lock()
		linkStore.links[linkID] = link
		linkStore.mu.Unlock()

		frontendURL := getFrontendURL()
		url := fmt.Sprintf("%s/view/%s", frontendURL, token)

		log.Printf("temp link created: id=%s label=%s expires=%s", linkID, req.Label, expiresAt.Format(time.RFC3339))

		writeJSON(w, http.StatusCreated, map[string]any{
			"id":        linkID,
			"token":     token,
			"url":       url,
			"expiresAt": expiresAt.Format(time.RFC3339),
		})
	}
}

// handleVerifyTempLink handles GET /api/links/verify/{token}
// Public endpoint — token is the auth mechanism
func handleVerifyTempLink() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokenStr := r.PathValue("token")
		if tokenStr == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token is required"})
			return
		}

		claims, err := parseTempLinkJWT(tokenStr)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "token expired or revoked"})
			return
		}

		// Check blacklist
		linkStore.mu.RLock()
		_, revoked := linkStore.blacklist[claims.LinkID]
		linkStore.mu.RUnlock()

		if revoked {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "token expired or revoked"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"valid":     true,
			"expiresAt": claims.ExpiresAt.Time.Format(time.RFC3339),
		})
	}
}

// handleListTempLinks handles GET /api/links (admin only)
func handleListTempLinks() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)
		if user.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return
		}

		now := time.Now()
		linkStore.mu.RLock()
		defer linkStore.mu.RUnlock()

		active := []map[string]any{}
		for _, link := range linkStore.links {
			// Skip expired links
			if link.ExpiresAt.Before(now) {
				continue
			}
			// Skip revoked links
			if _, revoked := linkStore.blacklist[link.ID]; revoked {
				continue
			}
			active = append(active, map[string]any{
				"id":        link.ID,
				"label":     link.Label,
				"createdAt": link.CreatedAt.Format(time.RFC3339),
				"expiresAt": link.ExpiresAt.Format(time.RFC3339),
			})
		}

		writeJSON(w, http.StatusOK, active)
	}
}

// handleRevokeTempLink handles DELETE /api/links/{id} (admin only)
func handleRevokeTempLink() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)
		if user.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return
		}

		linkID := r.PathValue("id")
		if linkID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "link id is required"})
			return
		}

		linkStore.mu.Lock()
		defer linkStore.mu.Unlock()

		link, exists := linkStore.links[linkID]
		if !exists {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "link not found"})
			return
		}

		// Add to blacklist
		linkStore.blacklist[linkID] = struct{}{}

		log.Printf("temp link revoked: id=%s label=%s", linkID, link.Label)

		w.WriteHeader(http.StatusNoContent)
	}
}
