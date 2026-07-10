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

func init() {
	if v := os.Getenv("HEARTBEAT_TIMEOUT_SEC"); v != "" {
		if sec, err := strconv.Atoi(v); err == nil && sec > 0 {
			heartbeatTimeout = time.Duration(sec) * time.Second
		}
	}
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
		SetCleanSession(true).
		SetKeepAlive(60 * time.Second).
		SetAutoReconnect(true).
		SetMaxReconnectInterval(60 * time.Second).
		SetConnectionLostHandler(func(_ mqtt.Client, err error) {
			log.Printf("[MQTT] Connection lost: %v", err)
		}).
		SetOnConnectHandler(func(client mqtt.Client) {
			log.Println("[MQTT] Connected to broker")
			subscribeTopics(client, notifierURL, webBackendURL)
		})

	mqttClient := mqtt.NewClient(opts)

	// Connect in background with retry
	go connectWithRetry(mqttClient)

	// HTTP server
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","service":"hw-gateway"}`))
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

	log.Println("hw-gateway listening on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
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
	// Subscribe to alert topic (QoS 2 — exactly once)
	alertToken := client.Subscribe("safety/+/alert", 2, func(_ mqtt.Client, msg mqtt.Message) {
		handleAlert(msg, notifierURL, webBackendURL)
	})
	alertToken.Wait()
	if alertToken.Error() != nil {
		log.Printf("[MQTT] Failed to subscribe to safety/+/alert: %v", alertToken.Error())
	} else {
		log.Println("[MQTT] Subscribed to safety/+/alert (QoS 2)")
	}

	// Subscribe to heartbeat topic (QoS 0 — at most once)
	hbToken := client.Subscribe("safety/+/heartbeat", 0, func(_ mqtt.Client, msg mqtt.Message) {
		handleHeartbeat(msg, webBackendURL)
	})
	hbToken.Wait()
	if hbToken.Error() != nil {
		log.Printf("[MQTT] Failed to subscribe to safety/+/heartbeat: %v", hbToken.Error())
	} else {
		log.Println("[MQTT] Subscribed to safety/+/heartbeat (QoS 0)")
	}

	// Subscribe to alert/resolved topic (QoS 1 — bidirectional sync, see mqtt-publisher-guide.md §5.5)
	resolvedToken := client.Subscribe("safety/+/alert/resolved", 1, func(_ mqtt.Client, msg mqtt.Message) {
		handleAlertResolvedSubscription(msg, webBackendURL)
	})
	resolvedToken.Wait()
	if resolvedToken.Error() != nil {
		log.Printf("[MQTT] Failed to subscribe to safety/+/alert/resolved: %v", resolvedToken.Error())
	} else {
		log.Println("[MQTT] Subscribed to safety/+/alert/resolved (QoS 1)")
	}

	// Subscribe to event/candidate topic (QoS 0 — best-effort, high frequency)
	candidateToken := client.Subscribe("safety/+/event/candidate", 0, func(_ mqtt.Client, msg mqtt.Message) {
		handleCandidate(msg, webBackendURL)
	})
	candidateToken.Wait()
	if candidateToken.Error() != nil {
		log.Printf("[MQTT] Failed to subscribe to safety/+/event/candidate: %v", candidateToken.Error())
	} else {
		log.Println("[MQTT] Subscribed to safety/+/event/candidate (QoS 0)")
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
		log.Printf("[MQTT] Missing required fields in alert payload: %s", string(msg.Payload()))
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
	go postDeviceSeen(webBackendURL, alert.SiteID, alert.DeviceID, "none")

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
		log.Printf("[MQTT] Missing required fields in heartbeat payload: %s", string(msg.Payload()))
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
	go postDeviceSeen(webBackendURL, hb.SiteID, hb.DeviceID, hb.AlertState)
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
		log.Printf("[MQTT] Missing required fields in candidate payload: %s", string(msg.Payload()))
		return
	}

	if candidate.Class == "" || candidate.Confidence <= 0 || candidate.Threshold <= 0 {
		log.Printf("[CANDIDATE] missing required fields (class/confidence/threshold), skipping: %s", msg.Payload())
		return
	}

	log.Printf("[CANDIDATE] deviceId=%s class=%s conf=%.3f threshold=%.2f", candidate.DeviceID, candidate.Class, candidate.Confidence, candidate.Threshold)

	// Best-effort: register device in web-backend (fire-and-forget)
	go postDeviceSeen(webBackendURL, candidate.SiteID, candidate.DeviceID, "none")
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
	token.Wait()

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
	token.Wait()

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
	token.Wait()
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
