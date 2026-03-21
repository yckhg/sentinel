package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

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

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
