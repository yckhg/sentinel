package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// DeviceStatus tracks the alive/dead state of a device based on heartbeats.
type DeviceStatus struct {
	DeviceID      string `json:"deviceId"`
	SiteID        string `json:"siteId"`
	Alive         bool   `json:"alive"`
	LastHeartbeat string `json:"lastHeartbeat"`
	AlertState    string `json:"alertState"` // "none" | "active"
	lastSeen      time.Time
}

// equipmentStore holds in-memory device statuses keyed by "siteId:deviceId".
var equipmentStore = struct {
	sync.RWMutex
	devices map[string]*DeviceStatus
}{devices: make(map[string]*DeviceStatus)}

var heartbeatTimeout = 30 * time.Second

// maxDevices caps the in-memory equipment store (LRU eviction guarantees the
// no-unbounded-growth invariant). evictTTL is the visibility-only TTL that
// REMOVES a device unseen past the TTL (distinct from dead-marking, which keeps
// the device). Both are set in init() from env with the startup invariant
// evictTTL > heartbeatTimeout enforced.
var (
	maxDevices = 1000
	evictTTL   = 86400 * time.Second
)

// --- MQTT topics ---
const (
	topicAlert     = "safety/+/alert"
	topicHeartbeat = "safety/+/heartbeat"
	topicResolved  = "safety/+/alert/resolved"
	topicCandidate = "safety/+/event/candidate"
)

// requiredHealthTopics are the alert-safety subscriptions that must be
// SUBACK-granted for /healthz to report healthy. candidate is a lossy reference
// channel and is deliberately excluded (its non-establishment must not degrade).
var requiredHealthTopics = []string{topicAlert, topicHeartbeat, topicResolved}

// subackFailure is the MQTT SUBACK return code meaning "subscription failed".
const subackFailure byte = 0x80

// healthState tracks the live MQTT connection + per-topic SUBACK-granted state so
// that /healthz reflects the real ability to receive field alerts, not merely
// that the HTTP server is up. All access is in-memory (no network round-trip),
// so /healthz always returns within 1s (never blocks on the broker).
type healthState struct {
	sync.RWMutex
	connected bool
	grants    map[string]byte // topic → SUBACK granted QoS (0x80 = failure)
}

func newHealthState() *healthState {
	return &healthState{grants: make(map[string]byte)}
}

// setConnected records the connection state. On disconnect, all subscription
// grants are cleared — a dropped connection un-establishes every subscription,
// and re-subscription SUBACKs must be re-received after reconnect.
func (h *healthState) setConnected(v bool) {
	h.Lock()
	h.connected = v
	if !v {
		h.grants = make(map[string]byte)
	}
	h.Unlock()
}

func (h *healthState) setGrant(topic string, granted byte) {
	h.Lock()
	h.grants[topic] = granted
	h.Unlock()
}

func (h *healthState) snapshot() (bool, map[string]byte) {
	h.RLock()
	defer h.RUnlock()
	g := make(map[string]byte, len(h.grants))
	for k, v := range h.grants {
		g[k] = v
	}
	return h.connected, g
}

// health is the process-wide MQTT health state consumed by /healthz.
var health = newHealthState()

// isHealthy is the pure health-state computation: healthy iff connected AND every
// required alert-safety topic was SUBACK-granted a valid QoS (not 0x80 failure).
// candidate grants are ignored. Pure over its inputs for unit-testability.
func isHealthy(connected bool, grants map[string]byte) bool {
	if !connected {
		return false
	}
	for _, t := range requiredHealthTopics {
		g, ok := grants[t]
		if !ok || g == subackFailure {
			return false
		}
	}
	return true
}

// recordGrant inspects a completed Subscribe token and records the SUBACK-granted
// QoS for the topic in the health state. A token error or 0x80 result marks the
// topic as not established (so /healthz stays degraded until a clean re-SUBACK).
func recordGrant(token mqtt.Token, topic string) {
	if token.Error() != nil {
		health.setGrant(topic, subackFailure)
		return
	}
	if st, ok := token.(*mqtt.SubscribeToken); ok {
		if g, ok := st.Result()[topic]; ok {
			health.setGrant(topic, g)
			return
		}
	}
	// No per-topic result available: treat the completed, error-free subscribe as
	// granted at QoS 0 (established). Defensive fallback — paho populates Result().
	health.setGrant(topic, 0)
}

// publishTimeout bounds how long a publish HTTP handler waits for the MQTT
// broker to accept a message. Designer-approved change: when the broker was
// connected once but then dropped (auto-reconnect in progress), Publish().Wait()
// would block indefinitely, hanging the HTTP request. We now bound the wait and
// return 503 on timeout instead of an unbounded hang.
const publishTimeout = 5 * time.Second

// maxLoggedPayloadBytes caps how much of a raw MQTT payload we write to logs,
// limiting leak surface and log bloat if the payload schema grows. (#56)
const maxLoggedPayloadBytes = 256

// truncatePayload renders up to maxLoggedPayloadBytes of a raw payload for
// logging, appending a marker (with the original length) when truncated.
func truncatePayload(p []byte) string {
	if len(p) <= maxLoggedPayloadBytes {
		return string(p)
	}
	return string(p[:maxLoggedPayloadBytes]) + fmt.Sprintf("...[truncated, %d bytes total]", len(p))
}

type alertEntry struct {
	insertedAt time.Time
}

var (
	processedAlertsMu sync.Mutex
	processedAlerts   = make(map[string]alertEntry) // alertId → 처리됨
)

// startAlertCacheCleanup removes entries older than 24 hours every hour.
func startAlertCacheCleanup() {
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			cutoff := time.Now().Add(-24 * time.Hour)
			processedAlertsMu.Lock()
			for id, entry := range processedAlerts {
				if entry.insertedAt.Before(cutoff) {
					delete(processedAlerts, id)
				}
			}
			processedAlertsMu.Unlock()
		}
	}()
}

// AlertPayload received from MQTT safety/+/alert topic.
type AlertPayload struct {
	DeviceID    string `json:"deviceId"`
	SiteID      string `json:"siteId"`
	Type        string `json:"type"`
	Timestamp   string `json:"timestamp"`
	AlertID     string `json:"alertId"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
	Test        bool   `json:"test,omitempty"`
}

// IncidentPayload forwarded to web-backend POST /api/incidents.
type IncidentPayload struct {
	SiteID      string `json:"siteId"`
	DeviceID    string `json:"deviceId,omitempty"`
	AlertID     string `json:"alertId,omitempty"`
	Description string `json:"description"`
	OccurredAt  string `json:"occurredAt"`
	IsTest      bool   `json:"isTest,omitempty"`
}

// TestAlertRequest received from web-backend POST /api/test-alert.
type TestAlertRequest struct {
	SiteID   string `json:"siteId"`
	DeviceID string `json:"deviceId"`
}

// CandidatePayload received from MQTT safety/+/event/candidate topic.
type CandidatePayload struct {
	DeviceID   string  `json:"deviceId"`
	SiteID     string  `json:"siteId"`
	Type       string  `json:"type"`
	Class      string  `json:"class"`
	Confidence float64 `json:"confidence"`
	Threshold  float64 `json:"threshold"`
	Timestamp  string  `json:"timestamp"`
}

// HeartbeatPayload received from MQTT safety/+/heartbeat topic.
type HeartbeatPayload struct {
	DeviceID   string `json:"deviceId"`
	SiteID     string `json:"siteId"`
	Status     string `json:"status"`
	Timestamp  string `json:"timestamp"`
	AlertState string `json:"alertState"` // "none" | "active"
	AlertID    string `json:"alertId,omitempty"`
}

// RestartRequest received from web-backend POST /api/restart.
type RestartRequest struct {
	SiteID      string `json:"siteId"`
	DeviceID    string `json:"deviceId"`
	RequestedBy string `json:"requestedBy"`
	Reason      string `json:"reason"`
}

// AlertResolvedPayload — bidirectional MQTT message on safety/{siteId}/alert/resolved.
// Spec: docs/interfaces/mqtt-publisher-guide.md §5.5.
type AlertResolvedPayload struct {
	IncidentID    int64                  `json:"incidentId"`
	SiteID        string                 `json:"siteId"`
	ResolvedAt    string                 `json:"resolvedAt"`
	ResolvedBy    AlertResolvedBy        `json:"resolvedBy"`
	OriginalAlert map[string]any         `json:"originalAlert,omitempty"`
}

type AlertResolvedBy struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Label string `json:"label"`
}

// RestartMQTTPayload published to MQTT safety/{siteId}/cmd/restart.
type RestartMQTTPayload struct {
	DeviceID    string `json:"deviceId"`
	SiteID      string `json:"siteId"`
	RequestedBy string `json:"requestedBy"`
	Reason      string `json:"reason"`
	Timestamp   string `json:"timestamp"`
}

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		ResponseHeaderTimeout: 5 * time.Second,
	},
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// parsePositiveIntEnv parses raw as a positive integer, returning fallback when
// raw is empty or not a positive integer (env contract: "positive int only").
func parsePositiveIntEnv(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return n
	}
	return fallback
}

// resolveEvictTTL enforces the startup invariant evictTTL > heartbeatTimeout so a
// dead-marked device stays queryable for a while before TTL removal. On violation
// (ttl <= heartbeatTimeout) it forces the default and reports forced=true so the
// caller can warn. Pure for unit-testability.
func resolveEvictTTL(ttl, heartbeatTimeout, defaultTTL time.Duration) (time.Duration, bool) {
	if ttl <= heartbeatTimeout {
		return defaultTTL, true
	}
	return ttl, false
}

// lruVictim returns the key of the least-recently-seen device (smallest lastSeen),
// or "" if the map is empty. Used to keep the store within EQUIPMENT_MAX_DEVICES.
func lruVictim(devices map[string]*DeviceStatus) string {
	var victim string
	var oldest time.Time
	for k, ds := range devices {
		if victim == "" || ds.lastSeen.Before(oldest) {
			victim = k
			oldest = ds.lastSeen
		}
	}
	return victim
}

// ttlExpiredKeys returns keys whose lastSeen is older than ttl relative to now
// (visibility-only TTL removal — distinct from dead-marking which retains).
func ttlExpiredKeys(devices map[string]*DeviceStatus, now time.Time, ttl time.Duration) []string {
	var expired []string
	for k, ds := range devices {
		if now.Sub(ds.lastSeen) > ttl {
			expired = append(expired, k)
		}
	}
	return expired
}

func init() {
	heartbeatTimeout = time.Duration(parsePositiveIntEnv(os.Getenv("HEARTBEAT_TIMEOUT_SEC"), 30)) * time.Second
	maxDevices = parsePositiveIntEnv(os.Getenv("EQUIPMENT_MAX_DEVICES"), 1000)

	ttl := time.Duration(parsePositiveIntEnv(os.Getenv("EQUIPMENT_EVICT_TTL_SEC"), 86400)) * time.Second
	resolved, forced := resolveEvictTTL(ttl, heartbeatTimeout, 86400*time.Second)
	if forced {
		log.Printf("[CONFIG] EQUIPMENT_EVICT_TTL_SEC (%v) must be > HEARTBEAT_TIMEOUT_SEC (%v); forcing default 86400s", ttl, heartbeatTimeout)
	}
	evictTTL = resolved
}

func main() {
	brokerURL := getEnv("MQTT_BROKER_URL", "tcp://mosquitto:1883")
	notifierURL := getEnv("NOTIFIER_URL", "http://notifier:8080")
	webBackendURL := getEnv("WEB_BACKEND_URL", "http://web-backend:8080")

	// Start background heartbeat checker
	go heartbeatChecker()

	// Start background processedAlerts TTL cleanup (24h retention)
	startAlertCacheCleanup()

	// Setup MQTT client options
	opts := mqtt.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID("sentinel-hw-gateway").
		// Persistent session (clean=false) + fixed clientID: on the reconnect
		// boundary the broker redelivers queued QoS1/2 messages so a receiver-side
		// alert is not lost (spec §회복력, assertion S; interface-mqtt §브로커 접속 계약).
		SetCleanSession(false).
		// Short keep-alive (≤5s) so a silent broker-down is detected within the
		// keep-alive boundary, aligning the /healthz degraded transition with the
		// 5s publish timeout (spec §헬스체크 단절 감지, assertion O2).
		SetKeepAlive(5 * time.Second).
		SetAutoReconnect(true).
		SetMaxReconnectInterval(60 * time.Second).
		SetConnectionLostHandler(func(_ mqtt.Client, err error) {
			log.Printf("[MQTT] Connection lost: %v", err)
			// Surface the disconnect to /healthz (degraded) and drop all grants.
			health.setConnected(false)
		}).
		SetOnConnectHandler(func(client mqtt.Client) {
			log.Println("[MQTT] Connected to broker")
			health.setConnected(true)
			subscribeTopics(client, notifierURL, webBackendURL)
		})

	mqttClient := mqtt.NewClient(opts)

	// Connect in background with retry
	go connectWithRetry(mqttClient)

	// HTTP server
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		// In-memory flag read only — never blocks on the broker, so this always
		// returns within 1s even while the broker is down (spec assertions A2/O2).
		connected, grants := health.snapshot()
		w.Header().Set("Content-Type", "application/json")
		if isHealthy(connected, grants) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok","service":"hw-gateway"}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"status":"degraded","service":"hw-gateway"}`))
	})

	mux.HandleFunc("POST /api/restart", func(w http.ResponseWriter, r *http.Request) {
		handleRestart(w, r, mqttClient)
	})

	mux.HandleFunc("GET /api/equipment/status", handleEquipmentStatus)

	mux.HandleFunc("POST /api/test-alert", func(w http.ResponseWriter, r *http.Request) {
		handleTestAlert(w, r, mqttClient)
	})

	mux.HandleFunc("POST /api/alert/resolved", func(w http.ResponseWriter, r *http.Request) {
		handleAlertResolvedPublish(w, r, mqttClient)
	})

	srv := newHTTPServer(maxBytesMiddleware(mux))

	log.Println("hw-gateway listening on :8080")
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// maxRequestBodyBytes caps request bodies (#41): the control endpoints accept
// only small JSON, so 1 MB prevents memory exhaustion from oversized bodies.
const maxRequestBodyBytes = 1 << 20 // 1 MB

// maxBytesMiddleware wraps every request body in an http.MaxBytesReader so a
// handler that decodes an oversized body gets an error (→ 400) instead of
// buffering unbounded data. GET/HEAD requests without a body are unaffected.
func maxBytesMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// newHTTPServer builds the service HTTP server with hardened timeouts. Without
// them ReadHeaderTimeout/ReadTimeout/IdleTimeout default to 0 (unlimited) and a
// slow/malicious client can trickle headers or body to hold goroutines/sockets
// open indefinitely (Slowloris) — critical for a safety control entrypoint.
func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              ":8080",
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func connectWithRetry(client mqtt.Client) {
	backoff := 1 * time.Second
	maxBackoff := 60 * time.Second
	lastLog := time.Time{}

	for {
		token := client.Connect()
		token.Wait()
		if token.Error() == nil {
			return
		}

		now := time.Now()
		if now.Sub(lastLog) >= 30*time.Second {
			log.Printf("[MQTT] Broker unreachable: %v (retrying with %.0fs backoff)", token.Error(), backoff.Seconds())
			lastLog = now
		}

		time.Sleep(backoff)
		backoff = time.Duration(math.Min(float64(backoff*2), float64(maxBackoff)))
	}
}

func subscribeTopics(client mqtt.Client, notifierURL, webBackendURL string) {
	// Alert topic (QoS 2 — exactly once). Persistent-session redelivery of a
	// QoS2 alert published while the gateway was briefly offline (assertion S).
	alertToken := client.Subscribe(topicAlert, 2, func(_ mqtt.Client, msg mqtt.Message) {
		handleAlert(msg, notifierURL, webBackendURL)
	})
	alertToken.Wait()
	recordGrant(alertToken, topicAlert)
	if alertToken.Error() != nil {
		log.Printf("[MQTT] Failed to subscribe to %s: %v", topicAlert, alertToken.Error())
	} else {
		log.Printf("[MQTT] Subscribed to %s (QoS 2)", topicAlert)
	}

	// Heartbeat topic (QoS 1 subscription — persistent session; published at QoS0
	// so no offline queueing/flood, but the subscription is granted at QoS1).
	hbToken := client.Subscribe(topicHeartbeat, 1, func(_ mqtt.Client, msg mqtt.Message) {
		handleHeartbeat(msg, webBackendURL)
	})
	hbToken.Wait()
	recordGrant(hbToken, topicHeartbeat)
	if hbToken.Error() != nil {
		log.Printf("[MQTT] Failed to subscribe to %s: %v", topicHeartbeat, hbToken.Error())
	} else {
		log.Printf("[MQTT] Subscribed to %s (QoS 1)", topicHeartbeat)
	}

	// alert/resolved topic (QoS 1 — bidirectional sync). Redelivery on the
	// reconnect boundary is idempotency-safe downstream (spec §재연결 중복).
	resolvedToken := client.Subscribe(topicResolved, 1, func(_ mqtt.Client, msg mqtt.Message) {
		handleAlertResolvedSubscription(msg, webBackendURL)
	})
	resolvedToken.Wait()
	recordGrant(resolvedToken, topicResolved)
	if resolvedToken.Error() != nil {
		log.Printf("[MQTT] Failed to subscribe to %s: %v", topicResolved, resolvedToken.Error())
	} else {
		log.Printf("[MQTT] Subscribed to %s (QoS 1)", topicResolved)
	}

	// event/candidate topic (QoS 0 — best-effort lossy reference channel; excluded
	// from /healthz judgment).
	candidateToken := client.Subscribe(topicCandidate, 0, func(_ mqtt.Client, msg mqtt.Message) {
		handleCandidate(msg, webBackendURL)
	})
	candidateToken.Wait()
	recordGrant(candidateToken, topicCandidate)
	if candidateToken.Error() != nil {
		log.Printf("[MQTT] Failed to subscribe to %s: %v", topicCandidate, candidateToken.Error())
	} else {
		log.Printf("[MQTT] Subscribed to %s (QoS 0)", topicCandidate)
	}
}

// isRetainedMessage returns true (and logs a warning) if msg carries the MQTT
// retained flag. The Sentinel contract fixes retain=false on ALL safety/# topics
// (docs/spec/interface-mqtt.md), so any retained message is stale/contract-violating
// and must never drive state — most critically the alert/resolved auto-resolve path,
// where a stale retained resolve would auto-close the latest unresolved incident
// without a human gate. Receiver-side defense: drop + log, never process.
func isRetainedMessage(msg mqtt.Message) bool {
	if msg.Retained() {
		log.Printf("[RETAINED] ignoring retained message on topic=%s (contract: retain=false)", msg.Topic())
		return true
	}
	return false
}

func handleAlert(msg mqtt.Message, notifierURL, webBackendURL string) {
	if isRetainedMessage(msg) {
		return
	}
	log.Printf("[MQTT] Received alert on topic: %s", msg.Topic())

	var alert AlertPayload
	if err := json.Unmarshal(msg.Payload(), &alert); err != nil {
		log.Printf("[MQTT] Malformed JSON payload on %s: %v", msg.Topic(), err)
		return
	}

	if alert.DeviceID == "" || alert.SiteID == "" || alert.Type == "" || alert.Timestamp == "" {
		log.Printf("[MQTT] Missing required fields in alert payload: %s", truncatePayload(msg.Payload()))
		return
	}

	// Deduplication: skip already-processed alertIds (in-memory, resets on restart).
	// NOTE: registration happens only AFTER a successful forward (see below), so a
	// forward that ultimately fails (e.g. web-backend 5xx after retries) does not block
	// the firmware's retransmit from recovering.
	if alert.AlertID != "" {
		processedAlertsMu.Lock()
		_, exists := processedAlerts[alert.AlertID]
		processedAlertsMu.Unlock()
		if exists {
			log.Printf("[ALERT] Duplicate alertId=%s, skipping", alert.AlertID)
			return
		}
	}

	// Use siteId from topic for consistency
	parts := strings.Split(msg.Topic(), "/")
	if len(parts) >= 2 {
		alert.SiteID = parts[1]
	}

	if alert.Test {
		log.Printf("[ALERT][TEST] deviceId=%s siteId=%s type=%s severity=%s", alert.DeviceID, alert.SiteID, alert.Type, alert.Severity)
	} else {
		log.Printf("[ALERT] deviceId=%s siteId=%s type=%s severity=%s", alert.DeviceID, alert.SiteID, alert.Type, alert.Severity)
	}

	// Forward to notifier and web-backend in parallel
	done := make(chan struct{}, 2)
	var forwardOK bool

	go func() {
		defer func() { done <- struct{}{} }()
		forwardToNotifier(notifierURL, &alert)
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		forwardOK = forwardToWebBackend(webBackendURL, &alert)
	}()

	// Best-effort: register device in web-backend (fire-and-forget)
	dispatchDeviceSeen(webBackendURL, alert.SiteID, alert.DeviceID, "none")

	<-done
	<-done

	// Record the alertId for dedup ONLY after the incident was successfully forwarded (2xx).
	// The two channel receives above establish happens-before on forwardOK (written by the
	// forward goroutine), so this read is race-free. If the forward ultimately failed, we
	// leave the alertId unrecorded so a firmware retransmit can retry.
	if alert.AlertID != "" && forwardOK {
		processedAlertsMu.Lock()
		processedAlerts[alert.AlertID] = alertEntry{insertedAt: time.Now()}
		processedAlertsMu.Unlock()
	}
}

func forwardToNotifier(notifierURL string, alert *AlertPayload) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	body, err := json.Marshal(alert)
	if err != nil {
		log.Printf("[FORWARD] Failed to marshal alert for notifier: %v", err)
		return
	}
	url := fmt.Sprintf("%s/api/notify", notifierURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("[FORWARD] Failed to create notifier request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("[FORWARD] Failed to send alert to notifier: %v", err)
		return
	}
	defer resp.Body.Close()
	log.Printf("[FORWARD] Notifier response: %d", resp.StatusCode)
}

// backoffWithJitter returns a duration with exponential backoff and ±25% random jitter.
func backoffWithJitter(base time.Duration, attempt int) time.Duration {
	delay := base * time.Duration(math.Pow(2, float64(attempt)))
	jitter := float64(delay) * 0.25 * (2*rand.Float64() - 1) // ±25%
	return delay + time.Duration(jitter)
}

// minValidTimestamp is the Unix timestamp threshold below which a device timestamp
// is considered unset or invalid (2020-01-01 00:00:00 UTC).
const minValidTimestamp = int64(1577836800)

// sanitizeTimestamp returns the parsed timestamp if it is valid (>= 2020-01-01),
// otherwise returns time.Now() and logs a warning.
func sanitizeTimestamp(raw string, deviceID string) time.Time {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		if t.Unix() >= minValidTimestamp {
			return t
		}
		log.Printf("[TIMESTAMP] invalid timestamp from device %s (got: %s, unix=%d), using server time", deviceID, raw, t.Unix())
		return time.Now().UTC()
	}
	// Try parsing as Unix integer string (some firmware sends epoch as integer)
	if ts, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if ts >= minValidTimestamp {
			return time.Unix(ts, 0).UTC()
		}
		log.Printf("[TIMESTAMP] invalid timestamp from device %s (got: %s, unix=%d), using server time", deviceID, raw, ts)
		return time.Now().UTC()
	}
	log.Printf("[TIMESTAMP] unparseable timestamp from device %s (got: %q), using server time", deviceID, raw)
	return time.Now().UTC()
}

// forwardToWebBackend POSTs the incident to web-backend, retrying on transport errors and
// HTTP 5xx (transient/server-side). A 4xx is a client error and is NOT retried. Returns true
// only when the incident was accepted (2xx). The 2xx path also covers web-backend's
// alertId-based dedup (returns the existing incident 200 on a duplicate).
func forwardToWebBackend(webBackendURL string, alert *AlertPayload) bool {
	occurredAt := sanitizeTimestamp(alert.Timestamp, alert.DeviceID)
	incident := IncidentPayload{
		SiteID:      alert.SiteID,
		DeviceID:    alert.DeviceID,
		AlertID:     alert.AlertID,
		Description: alert.Description,
		OccurredAt:  occurredAt.Format(time.RFC3339),
		IsTest:      alert.Test,
	}
	body, err := json.Marshal(incident)
	if err != nil {
		log.Printf("[FORWARD] Failed to marshal incident for web-backend: %v", err)
		return false
	}
	url := fmt.Sprintf("%s/api/incidents", webBackendURL)

	maxRetries := 3
	baseDelay := 1 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if reqErr != nil {
			cancel()
			log.Printf("[FORWARD] Failed to create web-backend request: %v", reqErr)
			return false
		}
		req.Header.Set("Content-Type", "application/json")

		resp, doErr := httpClient.Do(req)
		if doErr == nil {
			status := resp.StatusCode
			resp.Body.Close()
			cancel()
			log.Printf("[FORWARD] Web-backend response: %d", status)
			// 2xx: accepted (create 201 or dedup 200). 4xx: client error, do not retry.
			if status < 500 {
				return status >= 200 && status < 300
			}
			// 5xx: fall through to the retry/backoff below.
		} else {
			cancel()
		}

		if attempt < maxRetries {
			delay := backoffWithJitter(baseDelay, attempt)
			if doErr != nil {
				log.Printf("[FORWARD] Failed to send incident to web-backend: %v (retry %d/%d in %v)", doErr, attempt+1, maxRetries, delay.Round(time.Millisecond))
			} else {
				log.Printf("[FORWARD] Web-backend returned 5xx (retry %d/%d in %v)", attempt+1, maxRetries, delay.Round(time.Millisecond))
			}
			time.Sleep(delay)
		} else if doErr != nil {
			log.Printf("[FORWARD] All retries exhausted for web-backend: %v", doErr)
		} else {
			log.Printf("[FORWARD] All retries exhausted for web-backend (last response 5xx)")
		}
	}
	return false
}

func handleHeartbeat(msg mqtt.Message, webBackendURL string) {
	if isRetainedMessage(msg) {
		return
	}
	log.Printf("[MQTT] Received heartbeat on topic: %s", msg.Topic())

	var hb HeartbeatPayload
	if err := json.Unmarshal(msg.Payload(), &hb); err != nil {
		log.Printf("[MQTT] Malformed JSON heartbeat payload on %s: %v", msg.Topic(), err)
		return
	}

	if hb.DeviceID == "" || hb.SiteID == "" {
		log.Printf("[MQTT] Missing required fields in heartbeat payload: %s", truncatePayload(msg.Payload()))
		return
	}

	// Use siteId from topic for consistency
	parts := strings.Split(msg.Topic(), "/")
	if len(parts) >= 2 {
		hb.SiteID = parts[1]
	}

	// Default alertState to "none" if not provided
	if hb.AlertState == "" {
		hb.AlertState = "none"
	}

	now := time.Now().UTC()
	key := hb.SiteID + ":" + hb.DeviceID

	equipmentStore.Lock()
	ds, exists := equipmentStore.devices[key]
	if !exists {
		// LRU cap eviction — the sole guarantee of the no-unbounded-growth
		// invariant. Evict least-recently-seen devices until adding one keeps the
		// store <= EQUIPMENT_MAX_DEVICES (spec §장비 스토어 보존 상한, assertion Q).
		for len(equipmentStore.devices) >= maxDevices {
			victim := lruVictim(equipmentStore.devices)
			if victim == "" {
				break
			}
			delete(equipmentStore.devices, victim)
			log.Printf("[EVICT] LRU cap (%d) exceeded, evicted least-recently-seen device: %s", maxDevices, victim)
		}
		ds = &DeviceStatus{
			DeviceID: hb.DeviceID,
			SiteID:   hb.SiteID,
		}
		equipmentStore.devices[key] = ds
		log.Printf("[HEARTBEAT] New device registered: %s", key)
	}
	ds.Alive = true
	ds.LastHeartbeat = now.Format(time.RFC3339)
	ds.AlertState = hb.AlertState
	ds.lastSeen = now
	equipmentStore.Unlock()

	log.Printf("[HEARTBEAT] deviceId=%s siteId=%s status=%s alertState=%s", hb.DeviceID, hb.SiteID, hb.Status, hb.AlertState)

	// Best-effort: notify web-backend for persistent device registration
	dispatchDeviceSeen(webBackendURL, hb.SiteID, hb.DeviceID, hb.AlertState)
}

// handleCandidate processes safety/+/event/candidate messages.
// Best-effort: logs the candidate event and notifies web-backend via POST /api/devices/seen.
// No incident is created and notifier is not called.
func handleCandidate(msg mqtt.Message, webBackendURL string) {
	if isRetainedMessage(msg) {
		return
	}
	var candidate CandidatePayload
	if err := json.Unmarshal(msg.Payload(), &candidate); err != nil {
		log.Printf("[MQTT] Malformed JSON candidate payload on %s: %v", msg.Topic(), err)
		return
	}

	if candidate.DeviceID == "" || candidate.SiteID == "" {
		log.Printf("[MQTT] Missing required fields in candidate payload: %s", truncatePayload(msg.Payload()))
		return
	}

	if candidate.Class == "" || candidate.Confidence <= 0 || candidate.Threshold <= 0 {
		log.Printf("[CANDIDATE] missing required fields (class/confidence/threshold), skipping: %s", truncatePayload(msg.Payload()))
		return
	}

	log.Printf("[CANDIDATE] deviceId=%s class=%s conf=%.3f threshold=%.2f", candidate.DeviceID, candidate.Class, candidate.Confidence, candidate.Threshold)

	// Best-effort: register device in web-backend (fire-and-forget)
	dispatchDeviceSeen(webBackendURL, candidate.SiteID, candidate.DeviceID, "none")
}

// deviceSeenMaxConcurrent caps the number of concurrent postDeviceSeen HTTP
// calls in flight (#55). heartbeat/alert/candidate traffic each fires a
// best-effort device-seen notification, and each call can live up to its 5s
// timeout when web-backend is slow or down. Without a cap, high-frequency
// candidate traffic across many devices spawns unbounded goroutines/sockets.
const deviceSeenMaxConcurrent = 32

// deviceSeenSem bounds concurrent device-seen dispatches to the cap above.
var deviceSeenSem = make(chan struct{}, deviceSeenMaxConcurrent)

// deviceSeenSender performs the actual send. It is a package var so tests can
// substitute a deterministic (e.g. blocking) sender to exercise the cap.
var deviceSeenSender = postDeviceSeen

// dispatchDeviceSeen fires a bounded, best-effort device-seen notification.
// When the outbound concurrency cap is reached the call is dropped (logged)
// rather than blocking a hot MQTT/HTTP path or accumulating goroutines/sockets —
// device-seen is fire-and-forget and is re-established by the next heartbeat.
func dispatchDeviceSeen(webBackendURL, siteID, deviceID, alertState string) {
	// Snapshot the semaphore and sender so a single dispatch uses a consistent
	// pair even if the package vars are swapped (e.g. by tests).
	sem := deviceSeenSem
	send := deviceSeenSender
	select {
	case sem <- struct{}{}:
		go func() {
			defer func() { <-sem }()
			send(webBackendURL, siteID, deviceID, alertState)
		}()
	default:
		log.Printf("[DEVICE-SEEN] Dropped (outbound cap %d reached): site=%s device=%s",
			deviceSeenMaxConcurrent, siteID, deviceID)
	}
}

// postDeviceSeen notifies web-backend that a device was seen.
// alertState should be "none" or "active"; empty string is treated as "none" by the receiver.
// Best-effort: failures are logged but never retried or propagated.
func postDeviceSeen(webBackendURL, siteID, deviceID, alertState string) {
	if webBackendURL == "" || siteID == "" || deviceID == "" {
		return
	}
	body, err := json.Marshal(map[string]string{
		"siteId":     siteID,
		"deviceId":   deviceID,
		"alertState": alertState,
	})
	if err != nil {
		log.Printf("[DEVICE-SEEN] Failed to marshal: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/api/devices/seen", webBackendURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("[DEVICE-SEEN] Failed to create request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("[DEVICE-SEEN] Failed to call web-backend: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("[DEVICE-SEEN] Non-2xx response: %d", resp.StatusCode)
	}
}

// heartbeatChecker periodically marks devices as dead if no heartbeat within timeout.
func heartbeatChecker() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()

		// Phase 1: read lock to identify stale devices
		var staleKeys []string
		equipmentStore.RLock()
		for key, ds := range equipmentStore.devices {
			if ds.Alive && now.Sub(ds.lastSeen) > heartbeatTimeout {
				staleKeys = append(staleKeys, key)
			}
		}
		equipmentStore.RUnlock()

		// Phase 2: write lock to mark stale devices as dead
		if len(staleKeys) > 0 {
			equipmentStore.Lock()
			for _, key := range staleKeys {
				ds := equipmentStore.devices[key]
				// Re-check under write lock in case heartbeat arrived between phases
				if ds != nil && ds.Alive && now.Sub(ds.lastSeen) > heartbeatTimeout {
					ds.Alive = false
					log.Printf("[HEARTBEAT] Device %s marked as dead (no heartbeat for %v)", key, heartbeatTimeout)
				}
			}
			equipmentStore.Unlock()
		}

		// Phase 3: TTL eviction — REMOVE devices unseen past evictTTL (visibility
		// cleanup, distinct from dead-marking above which keeps the device). With
		// the startup invariant evictTTL > heartbeatTimeout, a dead-marked device
		// stays queryable until the TTL elapses (spec assertions R/C/T).
		equipmentStore.Lock()
		for _, key := range ttlExpiredKeys(equipmentStore.devices, now, evictTTL) {
			delete(equipmentStore.devices, key)
			log.Printf("[EVICT] TTL (%v) exceeded, removed device from store: %s", evictTTL, key)
		}
		equipmentStore.Unlock()
	}
}

// handleEquipmentStatus returns all device statuses.
func handleEquipmentStatus(w http.ResponseWriter, r *http.Request) {
	equipmentStore.RLock()
	statuses := make([]DeviceStatus, 0, len(equipmentStore.devices))
	for _, ds := range equipmentStore.devices {
		statuses = append(statuses, DeviceStatus{
			DeviceID:      ds.DeviceID,
			SiteID:        ds.SiteID,
			Alive:         ds.Alive,
			LastHeartbeat: ds.LastHeartbeat,
			AlertState:    ds.AlertState,
		})
	}
	equipmentStore.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(statuses)
}

func handleRestart(w http.ResponseWriter, r *http.Request, mqttClient mqtt.Client) {
	var req RestartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON payload"})
		return
	}

	if req.SiteID == "" || req.DeviceID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "siteId and deviceId are required"})
		return
	}

	if !mqttClient.IsConnected() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "MQTT broker not connected"})
		return
	}

	// Build MQTT payload
	payload := RestartMQTTPayload{
		DeviceID:    req.DeviceID,
		SiteID:      req.SiteID,
		RequestedBy: req.RequestedBy,
		Reason:      req.Reason,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to marshal payload"})
		return
	}

	topic := fmt.Sprintf("safety/%s/cmd/restart", req.SiteID)
	token := mqttClient.Publish(topic, 1, false, body) // QoS 1
	if !token.WaitTimeout(publishTimeout) {
		log.Printf("[RESTART] Publish to %s timed out after %v (broker unreachable)", topic, publishTimeout)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "MQTT publish timeout — broker unreachable"})
		return
	}

	if token.Error() != nil {
		log.Printf("[RESTART] Failed to publish to %s: %v", topic, token.Error())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to publish MQTT command"})
		return
	}

	log.Printf("[RESTART] Published restart command to %s: deviceId=%s requestedBy=%s", topic, req.DeviceID, req.RequestedBy)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "sent",
		"topic":  topic,
	})
}

func handleTestAlert(w http.ResponseWriter, r *http.Request, mqttClient mqtt.Client) {
	var req TestAlertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON payload"})
		return
	}

	if req.SiteID == "" {
		req.SiteID = "test"
	}
	if req.DeviceID == "" {
		req.DeviceID = "TEST-DEVICE"
	}

	if !mqttClient.IsConnected() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "MQTT broker not connected"})
		return
	}

	alert := AlertPayload{
		DeviceID:    req.DeviceID,
		SiteID:      req.SiteID,
		Type:        "test",
		Description: "[테스트] 비상 신호 시뮬레이션",
		Severity:    "critical",
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		Test:        true,
	}

	body, err := json.Marshal(alert)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to marshal payload"})
		return
	}

	topic := fmt.Sprintf("safety/%s/alert", req.SiteID)
	token := mqttClient.Publish(topic, 2, false, body) // QoS 2 — same as real alerts
	if !token.WaitTimeout(publishTimeout) {
		log.Printf("[TEST-ALERT] Publish to %s timed out after %v (broker unreachable)", topic, publishTimeout)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "MQTT publish timeout — broker unreachable"})
		return
	}

	if token.Error() != nil {
		log.Printf("[TEST-ALERT] Failed to publish to %s: %v", topic, token.Error())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to publish test alert"})
		return
	}

	log.Printf("[TEST-ALERT] Published test alert to %s: deviceId=%s", topic, req.DeviceID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "sent",
		"topic":  topic,
	})
}

// handleAlertResolvedPublish publishes safety/{siteId}/alert/resolved on behalf of web-backend.
// Spec: docs/interfaces/mqtt-publisher-guide.md §5.5 (web-initiated resolve).
func handleAlertResolvedPublish(w http.ResponseWriter, r *http.Request, mqttClient mqtt.Client) {
	var payload AlertResolvedPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON payload"})
		return
	}
	if payload.SiteID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "siteId is required"})
		return
	}
	if payload.ResolvedAt == "" {
		payload.ResolvedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if payload.ResolvedBy.Kind == "" {
		payload.ResolvedBy.Kind = "web"
	}

	if !mqttClient.IsConnected() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "MQTT broker not connected"})
		return
	}

	body, err := json.Marshal(payload)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to marshal payload"})
		return
	}

	topic := fmt.Sprintf("safety/%s/alert/resolved", payload.SiteID)
	token := mqttClient.Publish(topic, 1, false, body) // QoS 1, retain false
	if !token.WaitTimeout(publishTimeout) {
		log.Printf("[ALERT-RESOLVED] Publish to %s timed out after %v (broker unreachable)", topic, publishTimeout)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "MQTT publish timeout — broker unreachable"})
		return
	}
	if token.Error() != nil {
		log.Printf("[ALERT-RESOLVED] Failed to publish to %s: %v", topic, token.Error())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to publish MQTT alert/resolved"})
		return
	}

	log.Printf("[ALERT-RESOLVED] Published to %s: incident=%d kind=%s id=%s", topic, payload.IncidentID, payload.ResolvedBy.Kind, payload.ResolvedBy.ID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "sent", "topic": topic})
}

// handleAlertResolvedSubscription receives safety/+/alert/resolved messages.
// - resolvedBy.kind == "web"  → ignore (echo of our own publish, no-op).
// - resolvedBy.kind == "sensor_button" → forward to web-backend POST /api/incidents/{id}/resolve-from-sensor.
func handleAlertResolvedSubscription(msg mqtt.Message, webBackendURL string) {
	if isRetainedMessage(msg) {
		return
	}
	log.Printf("[MQTT] Received alert/resolved on topic: %s", msg.Topic())

	var payload AlertResolvedPayload
	if err := json.Unmarshal(msg.Payload(), &payload); err != nil {
		log.Printf("[MQTT] Malformed alert/resolved payload on %s: %v", msg.Topic(), err)
		return
	}

	// Override siteId from topic for consistency
	parts := strings.Split(msg.Topic(), "/")
	if len(parts) >= 2 {
		payload.SiteID = parts[1]
	}

	// Echo guard: ignore web-originated messages (we published them ourselves).
	if payload.ResolvedBy.Kind == "web" {
		log.Printf("[ALERT-RESOLVED] Ignoring echo of web-originated resolve (incident=%d)", payload.IncidentID)
		return
	}

	if payload.ResolvedBy.Kind != "sensor_button" {
		log.Printf("[ALERT-RESOLVED] Unknown resolvedBy.kind=%q, ignoring", payload.ResolvedBy.Kind)
		return
	}

	if payload.SiteID == "" {
		log.Printf("[ALERT-RESOLVED] Missing siteId, ignoring")
		return
	}

	// Log originalAlert for operational visibility.
	if len(payload.OriginalAlert) > 0 {
		if orig, err := json.Marshal(payload.OriginalAlert); err == nil {
			log.Printf("[ALERT-RESOLVED] originalAlert received (incident=%d siteId=%s): %s", payload.IncidentID, payload.SiteID, orig)
		}
	}
	// omitempty: normal case — originalAlert 없음은 MQTT 스펙상 정상

	// Forward to web-backend. Path-id == 0 lets backend match latest unresolved on siteId.
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[ALERT-RESOLVED] marshal forward error: %v", err)
		return
	}
	url := fmt.Sprintf("%s/api/incidents/%d/resolve-from-sensor", webBackendURL, payload.IncidentID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("[ALERT-RESOLVED] new request error: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("[ALERT-RESOLVED] forward to web-backend failed: %v", err)
		return
	}
	defer resp.Body.Close()
	log.Printf("[ALERT-RESOLVED] web-backend response: %d (incident=%d kind=%s)", resp.StatusCode, payload.IncidentID, payload.ResolvedBy.Kind)
}
