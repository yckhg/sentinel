package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Notification channel test-send surface (docs/spec/notification-test-send.md).
//
//   GET  /api/notifications/channels          → {email:{usable,reason}, sms:{usable,reason}}
//   POST /api/notifications/test {channel,target} → {outcome}  (sent|failed|not_configured)
//
// Both are admin-only (same권한 as contacts CUD). channel ∈ {email, sms}. The
// handlers proxy to the notifier's internal channel-status / test-send endpoints
// (status source = send source = the same notifier, §출력 7). web-backend does NOT
// self-judge usability — it projects the notifier's runtime config result verbatim.
//
// notifier unreachable → 502 (+reason upstream_unavailable), never downgraded to a
// false not_configured / sent (§출력 14, assertion N). The proxy hop timeout is set
// larger than the notifier per-channel budget (≤12s, §출력 9) so a normal channel
// delay is awaited rather than being cut off early as a false 502.

// emailTargetRegex validates the admin-entered destination email for a test send.
// A minimal shape check (local@domain.tld) — malformed/empty targets are rejected
// with 400 BEFORE the rate limiter is consulted (§출력 10, assertion J).
var emailTargetRegex = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// notifierProxyClient is the web-backend→notifier proxy hop client. Its timeout is
// deliberately ABOVE the notifier per-channel budget (≤12s, §출력 9) so web-backend
// waits for the notifier's sent/failed verdict on a slow channel instead of cutting
// the connection early and mis-terminating as a false 502 (assertion N NOK guard).
var notifierProxyClient = &http.Client{Timeout: 15 * time.Second}

type notifChannelStatus struct {
	Usable bool   `json:"usable"`
	Reason string `json:"reason"`
}

// notifChannelsResponse is projected verbatim from the notifier channel-status
// result. Exactly the two in-scope channels — no KakaoTalk key (§출력 12, G/K).
type notifChannelsResponse struct {
	Email notifChannelStatus `json:"email"`
	SMS   notifChannelStatus `json:"sms"`
}

type notifTestRequest struct {
	Channel string `json:"channel"`
	Target  string `json:"target"`
}

// handleNotificationChannels serves GET /api/notifications/channels (admin-only).
// It queries the notifier's current runtime config at REQUEST time and projects
// the per-channel usable/reason. notifier미도달 → 502 (upstream_unavailable), never
// a false not_configured / reason-less usable=false (§출력 1·14).
func handleNotificationChannels(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if getAuthUser(r).Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return
		}

		resp, err := notifierProxyClient.Get(notifierURL + "/internal/channel-status")
		if err != nil {
			// notifier 미도달 (down / restart window): 502, not a false not_configured.
			log.Printf("notification channels: notifier unreachable: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "notifier unavailable", "reason": "upstream_unavailable"})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Printf("notification channels: notifier returned status %d", resp.StatusCode)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "notifier error", "reason": "upstream_unavailable"})
			return
		}

		var out notifChannelsResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			log.Printf("notification channels: decode notifier response: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "notifier response invalid", "reason": "upstream_unavailable"})
			return
		}

		// Project ONLY email/sms — the struct drops any other channel key the
		// notifier might carry, so KakaoTalk can never surface (§출력 12).
		writeJSON(w, http.StatusOK, out)
	}
}

// handleNotificationTest serves POST /api/notifications/test (admin-only). Order:
// admin gate → input/channel validation (400, BEFORE rate-limit, token미소모) →
// (channel,target) rate limit (429, 발송 0) → proxy to notifier single synchronous
// test-send. notifier미도달 → 502 (upstream_unavailable), never downgraded to
// not_configured/sent (§출력 10·14, assertions F/G/J/M/N).
func handleNotificationTest(db *sql.DB) http.HandlerFunc {
	// One limiter per handler instance: (channel,target) scope, 1/min (§출력 10).
	// Created here (not per-request) so it persists across requests; created per
	// handler-construction so each mounted server starts with an empty limiter.
	limiter := newRateLimiter(1, time.Minute)

	return func(w http.ResponseWriter, r *http.Request) {
		if getAuthUser(r).Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return
		}

		var req notifTestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		req.Channel = strings.TrimSpace(req.Channel)
		req.Target = strings.TrimSpace(req.Target)

		// Channel support check (§출력 12) + input validation (§입력 2·3) BOTH run
		// before the rate limiter, and a 400 never consumes a token (§출력 10, J).
		switch req.Channel {
		case "email":
			if !emailTargetRegex.MatchString(req.Target) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "valid target email is required"})
				return
			}
		case "sms":
			if !validatePhone(req.Target) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "valid target phone number is required"})
				return
			}
		default:
			// channel ∉ {email, sms} (e.g. KakaoTalk) → 400 before any proxy (G).
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported channel"})
			return
		}

		// (channel,target)-scoped rate limit — distinct targets do not interfere
		// (§출력 10, M). Only valid requests reach here, so a 400 above cannot burn
		// the token that a legitimate retry needs (J↔M interaction).
		if !limiter.allow(req.Channel + "\x1f" + req.Target) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many test sends for this channel/target, try again later"})
			return
		}

		// Proxy the single synchronous send to the notifier (status source = send
		// source = notifier). notifier 미도달 → 502, no manufactured outcome (N).
		payload, _ := json.Marshal(notifTestRequest{Channel: req.Channel, Target: req.Target})
		resp, err := notifierProxyClient.Post(notifierURL+"/internal/test-send", "application/json", bytes.NewReader(payload))
		if err != nil {
			log.Printf("notification test: notifier unreachable: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "notifier unavailable", "reason": "upstream_unavailable"})
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		// The email test path reuses the notifier's send-email (503 when SMTP未설정),
		// which the notifier maps to outcome not_configured. A transport-level 5xx
		// that carries no closed-set outcome is treated as upstream failure (502) so
		// notifier미도달 is never downgraded. A 2xx with an outcome is projected as-is.
		var nr struct {
			Outcome string `json:"outcome"`
			Reason  string `json:"reason"`
		}
		_ = json.Unmarshal(body, &nr)

		if nr.Outcome == "" {
			// notifier responded but produced no closed-set outcome → treat as an
			// upstream failure rather than manufacture sent/not_configured (§출력 14).
			log.Printf("notification test: notifier returned no outcome (status %d)", resp.StatusCode)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "notifier produced no outcome", "reason": "upstream_unavailable"})
			return
		}

		out := map[string]string{"outcome": nr.Outcome}
		if nr.Reason != "" {
			out["reason"] = nr.Reason
		}
		writeJSON(w, http.StatusOK, out)
	}
}
