package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
)

var phoneRegex = regexp.MustCompile(`^01[016789]-\d{3,4}-\d{4}$`)

type contactRequest struct {
	Name        string `json:"name"`
	Phone       string `json:"phone"`
	Email       string `json:"email"`
	NotifyEmail *bool  `json:"notifyEmail"`
}

type contactResponse struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Phone       string `json:"phone"`
	Email       string `json:"email"`
	NotifyEmail bool   `json:"notifyEmail"`
}

func validatePhone(phone string) bool {
	return phoneRegex.MatchString(phone)
}

func handleListContacts(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		rows, err := db.QueryContext(ctx, "SELECT id, name, phone, COALESCE(email, ''), notify_email FROM contacts ORDER BY id ASC")
		if err != nil {
			log.Printf("query contacts error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		defer rows.Close()

		contacts := []contactResponse{}
		for rows.Next() {
			var c contactResponse
			if err := rows.Scan(&c.ID, &c.Name, &c.Phone, &c.Email, &c.NotifyEmail); err != nil {
				log.Printf("scan contact error: %v", err)
				continue
			}
			contacts = append(contacts, c)
		}

		writeJSON(w, http.StatusOK, contacts)
	}
}

func handleCreateContact(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)
		if user.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return
		}

		var req contactRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		req.Name = strings.TrimSpace(req.Name)
		req.Phone = strings.TrimSpace(req.Phone)
		req.Email = strings.TrimSpace(req.Email)

		if req.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}
		if req.Phone == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "phone is required"})
			return
		}
		if !validatePhone(req.Phone) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid phone number format"})
			return
		}

		notifyEmail := false
		if req.NotifyEmail != nil {
			notifyEmail = *req.NotifyEmail
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		var emailVal interface{}
		if req.Email != "" {
			emailVal = req.Email
		}

		result, err := db.ExecContext(ctx,
			"INSERT INTO contacts (name, phone, email, notify_email) VALUES (?, ?, ?, ?)",
			req.Name, req.Phone, emailVal, notifyEmail,
		)
		if err != nil {
			log.Printf("insert contact error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		id, _ := result.LastInsertId()
		log.Printf("contact created: id=%d name=%s phone=%s email=%v by user=%d", id, req.Name, req.Phone, emailVal, user.UserID)

		writeJSON(w, http.StatusCreated, contactResponse{
			ID:          id,
			Name:        req.Name,
			Phone:       req.Phone,
			Email:       req.Email,
			NotifyEmail: notifyEmail,
		})
	}
}

func handleUpdateContact(db *sql.DB) http.HandlerFunc {
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

		var req contactRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		req.Name = strings.TrimSpace(req.Name)
		req.Phone = strings.TrimSpace(req.Phone)

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		// Load existing contact
		var existing contactResponse
		err := db.QueryRowContext(ctx, "SELECT id, name, phone, COALESCE(email, ''), notify_email FROM contacts WHERE id = ?", id).Scan(
			&existing.ID, &existing.Name, &existing.Phone, &existing.Email, &existing.NotifyEmail,
		)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "contact not found"})
			return
		}
		if err != nil {
			log.Printf("query contact error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		// Apply partial updates
		if req.Name != "" {
			existing.Name = req.Name
		}
		if req.Phone != "" {
			if !validatePhone(req.Phone) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid phone number format"})
				return
			}
			existing.Phone = req.Phone
		}
		req.Email = strings.TrimSpace(req.Email)
		existing.Email = req.Email
		if req.NotifyEmail != nil {
			existing.NotifyEmail = *req.NotifyEmail
		}

		var emailVal interface{}
		if existing.Email != "" {
			emailVal = existing.Email
		}

		_, err = db.ExecContext(ctx,
			"UPDATE contacts SET name = ?, phone = ?, email = ?, notify_email = ? WHERE id = ?",
			existing.Name, existing.Phone, emailVal, existing.NotifyEmail, id,
		)
		if err != nil {
			log.Printf("update contact error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		log.Printf("contact updated: id=%d by user=%d", id, user.UserID)
		writeJSON(w, http.StatusOK, existing)
	}
}

func handleDeleteContact(db *sql.DB) http.HandlerFunc {
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

		result, err := db.ExecContext(ctx, "DELETE FROM contacts WHERE id = ?", id)
		if err != nil {
			log.Printf("delete contact error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "contact not found"})
			return
		}

		log.Printf("contact deleted: id=%d by user=%d", id, user.UserID)
		w.WriteHeader(http.StatusNoContent)
	}
}
