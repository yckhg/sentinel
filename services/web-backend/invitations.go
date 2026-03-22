package main

import (
	"bytes"
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
)

var notifierURL string

func initNotifierURL() {
	notifierURL = os.Getenv("NOTIFIER_URL")
	if notifierURL == "" {
		notifierURL = "http://notifier:8080"
	}
	log.Printf("notifier URL: %s", notifierURL)
}

// --- Types ---

type invitationResponse struct {
	ID        int64  `json:"id"`
	Email     string `json:"email"`
	Token     string `json:"token"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
	ExpiresAt string `json:"expiresAt"`
}

type createInvitationRequest struct {
	Email string `json:"email"`
}

// --- Handlers ---

func handleCreateInvitation(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)
		if user.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return
		}

		var req createInvitationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		req.Email = strings.TrimSpace(req.Email)
		if req.Email == "" || !strings.Contains(req.Email, "@") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "valid email is required"})
			return
		}

		tokenBytes := make([]byte, 32)
		if _, err := rand.Read(tokenBytes); err != nil {
			log.Printf("crypto/rand error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		token := hex.EncodeToString(tokenBytes)
		now := time.Now().UTC()
		expiresAt := now.Add(7 * 24 * time.Hour)

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		result, err := db.ExecContext(ctx,
			"INSERT INTO invitations (email, token, status, created_at, expires_at) VALUES (?, ?, 'pending', ?, ?)",
			req.Email, token, now.Format("2006-01-02 15:04:05"), expiresAt.Format("2006-01-02 15:04:05"),
		)
		if err != nil {
			log.Printf("insert invitation error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		id, _ := result.LastInsertId()
		log.Printf("invitation created: id=%d email=%s by user=%d", id, req.Email, user.UserID)

		// Send invitation email via notifier (async)
		go sendInvitationEmail(req.Email, token)

		writeJSON(w, http.StatusCreated, invitationResponse{
			ID:        id,
			Email:     req.Email,
			Token:     token,
			Status:    "pending",
			CreatedAt: now.Format("2006-01-02 15:04:05"),
			ExpiresAt: expiresAt.Format("2006-01-02 15:04:05"),
		})
	}
}

func sendInvitationEmail(email, token string) {
	frontendURL := getFrontendURL()
	registerURL := fmt.Sprintf("%s/register?invite=%s", frontendURL, token)

	body := fmt.Sprintf(`<html><body>
<h2>Sentinel — 초대장</h2>
<p>Sentinel 시스템에 초대되었습니다.</p>
<p>아래 링크를 클릭하여 계정을 등록해 주세요:</p>
<p><a href="%s">계정 등록하기</a></p>
<p>이 링크는 7일간 유효합니다.</p>
</body></html>`, registerURL)

	payload := map[string]string{
		"to":      email,
		"subject": "[Sentinel] 초대장 — 계정을 등록해 주세요",
		"body":    body,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("invitation email: marshal error: %v", err)
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(notifierURL+"/api/send-email", "application/json", bytes.NewReader(jsonData))
	if err != nil {
		log.Printf("invitation email: send error: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		log.Printf("invitation email sent to %s", email)
	} else {
		log.Printf("invitation email: notifier returned status %d for %s", resp.StatusCode, email)
	}
}

func handleListInvitations(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)
		if user.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		rows, err := db.QueryContext(ctx,
			"SELECT id, email, token, status, created_at, expires_at FROM invitations ORDER BY created_at DESC")
		if err != nil {
			log.Printf("query invitations error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		defer rows.Close()

		invitations := []invitationResponse{}
		now := time.Now().UTC()
		for rows.Next() {
			var inv invitationResponse
			if err := rows.Scan(&inv.ID, &inv.Email, &inv.Token, &inv.Status, &inv.CreatedAt, &inv.ExpiresAt); err != nil {
				log.Printf("scan invitation error: %v", err)
				continue
			}
			// Auto-expire pending invitations
			if inv.Status == "pending" {
				if expTime, err := time.Parse("2006-01-02 15:04:05", inv.ExpiresAt); err == nil && now.After(expTime) {
					inv.Status = "expired"
				}
			}
			invitations = append(invitations, inv)
		}

		writeJSON(w, http.StatusOK, invitations)
	}
}

func handleDeleteInvitation(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)
		if user.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return
		}

		idStr := r.PathValue("id")
		var id int64
		if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		result, err := db.ExecContext(ctx, "UPDATE invitations SET status = 'cancelled' WHERE id = ? AND status = 'pending'", id)
		if err != nil {
			log.Printf("cancel invitation error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "invitation not found or not pending"})
			return
		}

		log.Printf("invitation cancelled: id=%d by user=%d", id, user.UserID)
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleVerifyInvitation(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.PathValue("token")
		if token == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token is required"})
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		var inv invitationResponse
		err := db.QueryRowContext(ctx,
			"SELECT id, email, token, status, created_at, expires_at FROM invitations WHERE token = ?",
			token,
		).Scan(&inv.ID, &inv.Email, &inv.Token, &inv.Status, &inv.CreatedAt, &inv.ExpiresAt)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "invitation not found"})
			return
		}
		if err != nil {
			log.Printf("verify invitation error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		if inv.Status != "pending" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invitation is " + inv.Status})
			return
		}

		// Check expiry
		expTime, err := time.Parse("2006-01-02 15:04:05", inv.ExpiresAt)
		if err == nil && time.Now().UTC().After(expTime) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invitation has expired"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{
			"email":  inv.Email,
			"status": "valid",
		})
	}
}

// acceptInvitation marks an invitation as accepted after successful registration.
func acceptInvitation(db *sql.DB, ctx_parent *http.Request, token string) {
	ctx, cancel := dbCtx(ctx_parent.Context())
	defer cancel()

	_, err := db.ExecContext(ctx,
		"UPDATE invitations SET status = 'accepted' WHERE token = ? AND status = 'pending'",
		token,
	)
	if err != nil {
		log.Printf("accept invitation error: %v", err)
	}
}

// isValidInviteToken checks if a token is valid (pending and not expired).
func isValidInviteToken(db *sql.DB, r *http.Request, token string) bool {
	ctx, cancel := dbCtx(r.Context())
	defer cancel()

	var status, expiresAt string
	err := db.QueryRowContext(ctx,
		"SELECT status, expires_at FROM invitations WHERE token = ?", token,
	).Scan(&status, &expiresAt)
	if err != nil {
		return false
	}

	if status != "pending" {
		return false
	}

	expTime, err := time.Parse("2006-01-02 15:04:05", expiresAt)
	if err != nil {
		return false
	}

	return time.Now().UTC().Before(expTime)
}
