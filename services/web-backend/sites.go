package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

type siteResponse struct {
	ID           int64  `json:"id"`
	Address      string `json:"address"`
	ManagerName  string `json:"managerName"`
	ManagerPhone string `json:"managerPhone"`
}

type siteUpdateRequest struct {
	Address      string `json:"address"`
	ManagerName  string `json:"managerName"`
	ManagerPhone string `json:"managerPhone"`
}

func handleListSites(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)
		if user.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		rows, err := db.QueryContext(ctx, "SELECT id, address, manager_name, manager_phone FROM sites ORDER BY id ASC")
		if err != nil {
			log.Printf("query sites error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		defer rows.Close()

		sites := []siteResponse{}
		for rows.Next() {
			var s siteResponse
			if err := rows.Scan(&s.ID, &s.Address, &s.ManagerName, &s.ManagerPhone); err != nil {
				log.Printf("scan site error: %v", err)
				continue
			}
			sites = append(sites, s)
		}

		writeJSON(w, http.StatusOK, sites)
	}
}

func handleUpdateSite(db *sql.DB) http.HandlerFunc {
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

		var req siteUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		req.Address = strings.TrimSpace(req.Address)
		req.ManagerName = strings.TrimSpace(req.ManagerName)
		req.ManagerPhone = strings.TrimSpace(req.ManagerPhone)

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		// Load existing site
		var existing siteResponse
		err := db.QueryRowContext(ctx, "SELECT id, address, manager_name, manager_phone FROM sites WHERE id = ?", id).Scan(
			&existing.ID, &existing.Address, &existing.ManagerName, &existing.ManagerPhone,
		)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "site not found"})
			return
		}
		if err != nil {
			log.Printf("query site error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		// Apply partial updates
		if req.Address != "" {
			existing.Address = req.Address
		}
		if req.ManagerName != "" {
			existing.ManagerName = req.ManagerName
		}
		if req.ManagerPhone != "" {
			if !validatePhone(req.ManagerPhone) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid phone number format"})
				return
			}
			existing.ManagerPhone = req.ManagerPhone
		}

		_, err = db.ExecContext(ctx,
			"UPDATE sites SET address = ?, manager_name = ?, manager_phone = ? WHERE id = ?",
			existing.Address, existing.ManagerName, existing.ManagerPhone, id,
		)
		if err != nil {
			log.Printf("update site error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		log.Printf("site updated: id=%d by user=%d", id, user.UserID)
		writeJSON(w, http.StatusOK, existing)
	}
}
