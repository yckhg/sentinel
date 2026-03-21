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
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

type contactResponse struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

func validatePhone(phone string) bool {
	return phoneRegex.MatchString(phone)
}

func handleListContacts(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query("SELECT id, name, phone FROM contacts ORDER BY id ASC")
		if err != nil {
			log.Printf("query contacts error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		defer rows.Close()

		contacts := []contactResponse{}
		for rows.Next() {
			var c contactResponse
			if err := rows.Scan(&c.ID, &c.Name, &c.Phone); err != nil {
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

		result, err := db.Exec(
			"INSERT INTO contacts (name, phone) VALUES (?, ?)",
			req.Name, req.Phone,
		)
		if err != nil {
			log.Printf("insert contact error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		id, _ := result.LastInsertId()
		log.Printf("contact created: id=%d name=%s phone=%s by user=%d", id, req.Name, req.Phone, user.UserID)

		writeJSON(w, http.StatusCreated, contactResponse{
			ID:    id,
			Name:  req.Name,
			Phone: req.Phone,
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

		// Load existing contact
		var existing contactResponse
		err := db.QueryRow("SELECT id, name, phone FROM contacts WHERE id = ?", id).Scan(
			&existing.ID, &existing.Name, &existing.Phone,
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

		_, err = db.Exec(
			"UPDATE contacts SET name = ?, phone = ? WHERE id = ?",
			existing.Name, existing.Phone, id,
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

		result, err := db.Exec("DELETE FROM contacts WHERE id = ?", id)
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
