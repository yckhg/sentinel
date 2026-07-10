package main

import (
	"encoding/json"
	"log"
	"net/http"
)

// systemAlarmRequest is the payload an internal service (currently notifier)
// POSTs to /internal/alarms as the loss-prevention last resort when all external
// notification channels (KakaoTalk/SMS) have failed for a contact.
// Contract mirrors notifier's AlarmPayload — see docs/services/notifier.md
// "Outbound Calls" and docs/interfaces/web-api.md §12 (Internal) / §13 (system_alarm).
type systemAlarmRequest struct {
	Type    string         `json:"type"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// handleCreateSystemAlarm records a system alarm reported by an internal service
// and broadcasts it to connected admin clients over the existing WebSocket
// system_alarm channel. No auth — internal Docker network only (mirrors the other
// /internal/* and hw-gateway ingest routes). Persistence is intentionally omitted:
// the spec (docs/spec/notifier.md assertion C) does not assert DB loading, and no
// alarms table exists; the guaranteed behaviour is "record attempt + admin broadcast".
func handleCreateSystemAlarm() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req systemAlarmRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if req.Type == "" {
			req.Type = "system_alarm"
		}

		log.Printf("system alarm received: type=%s message=%s", req.Type, req.Message)

		// Broadcast to admin WebSocket clients (system_alarm is admin-only).
		BroadcastSystemAlarm(map[string]any{
			"type":    req.Type,
			"message": req.Message,
			"details": req.Details,
		})

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
