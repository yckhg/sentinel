package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

var hwGatewayURL string

func initHWGatewayURL() {
	hwGatewayURL = os.Getenv("HW_GATEWAY_URL")
	if hwGatewayURL == "" {
		hwGatewayURL = "http://hw-gateway:8080"
	}
	log.Printf("hw-gateway URL: %s", hwGatewayURL)
}

type restartRequest struct {
	SiteID   string `json:"siteId"`
	DeviceID string `json:"deviceId"`
	Reason   string `json:"reason"`
}

func handleEquipmentRestart() http.HandlerFunc {
	client := &http.Client{Timeout: 10 * time.Second}

	return func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)

		var req restartRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if req.SiteID == "" || req.DeviceID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "siteId and deviceId are required"})
			return
		}

		log.Printf("restart request from user %d (%s): siteId=%s deviceId=%s reason=%s",
			user.UserID, user.Role, req.SiteID, req.DeviceID, req.Reason)

		// Forward to hw-gateway with user info
		payload := map[string]string{
			"siteId":      req.SiteID,
			"deviceId":    req.DeviceID,
			"requestedBy": fmt.Sprintf("user:%d", user.UserID),
			"reason":      req.Reason,
		}
		body, err := json.Marshal(payload)
		if err != nil {
			log.Printf("failed to marshal restart payload: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		resp, err := client.Post(hwGatewayURL+"/api/restart", "application/json", bytes.NewReader(body))
		if err != nil {
			log.Printf("failed to forward restart to hw-gateway: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to reach hw-gateway"})
			return
		}
		defer resp.Body.Close()

		// Forward hw-gateway response back to client
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read hw-gateway response"})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
	}
}
