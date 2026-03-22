package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
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
	lastSeen      time.Time
}

// equipmentStore holds in-memory device statuses keyed by "siteId:deviceId".
var equipmentStore = struct {
	sync.RWMutex
	devices map[string]*DeviceStatus
}{devices: make(map[string]*DeviceStatus)}

var heartbeatTimeout = 30 * time.Second

// AlertPayload received from MQTT safety/+/alert topic.
type AlertPayload struct {
	DeviceID    string `json:"deviceId"`
	SiteID      string `json:"siteId"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
	Timestamp   string `json:"timestamp"`
}

// IncidentPayload forwarded to web-backend POST /api/incidents.
type IncidentPayload struct {
	SiteID      string `json:"siteId"`
	Description string `json:"description"`
	OccurredAt  string `json:"occurredAt"`
}

// HeartbeatPayload received from MQTT safety/+/heartbeat topic.
type HeartbeatPayload struct {
	DeviceID  string `json:"deviceId"`
	SiteID    string `json:"siteId"`
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

// RestartRequest received from web-backend POST /api/restart.
type RestartRequest struct {
	SiteID      string `json:"siteId"`
	DeviceID    string `json:"deviceId"`
	RequestedBy string `json:"requestedBy"`
	Reason      string `json:"reason"`
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
		handleHeartbeat(msg)
	})
	hbToken.Wait()
	if hbToken.Error() != nil {
		log.Printf("[MQTT] Failed to subscribe to safety/+/heartbeat: %v", hbToken.Error())
	} else {
		log.Println("[MQTT] Subscribed to safety/+/heartbeat (QoS 0)")
	}
}

func handleAlert(msg mqtt.Message, notifierURL, webBackendURL string) {
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

	// Use siteId from topic for consistency
	parts := strings.Split(msg.Topic(), "/")
	if len(parts) >= 2 {
		alert.SiteID = parts[1]
	}

	log.Printf("[ALERT] deviceId=%s siteId=%s type=%s severity=%s", alert.DeviceID, alert.SiteID, alert.Type, alert.Severity)

	// Forward to notifier and web-backend in parallel
	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		forwardToNotifier(notifierURL, &alert)
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		forwardToWebBackend(webBackendURL, &alert)
	}()

	<-done
	<-done
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

func forwardToWebBackend(webBackendURL string, alert *AlertPayload) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	incident := IncidentPayload{
		SiteID:      alert.SiteID,
		Description: alert.Description,
		OccurredAt:  alert.Timestamp,
	}
	body, err := json.Marshal(incident)
	if err != nil {
		log.Printf("[FORWARD] Failed to marshal incident for web-backend: %v", err)
		return
	}
	url := fmt.Sprintf("%s/api/incidents", webBackendURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("[FORWARD] Failed to create web-backend request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("[FORWARD] Failed to send incident to web-backend: %v (retrying in 1s)", err)
		time.Sleep(1 * time.Second)

		retryCtx, retryCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer retryCancel()

		retryReq, retryErr := http.NewRequestWithContext(retryCtx, http.MethodPost, url, bytes.NewReader(body))
		if retryErr != nil {
			log.Printf("[FORWARD] Failed to create web-backend retry request: %v", retryErr)
			return
		}
		retryReq.Header.Set("Content-Type", "application/json")

		resp, err = httpClient.Do(retryReq)
		if err != nil {
			log.Printf("[FORWARD] Retry failed for web-backend: %v", err)
			return
		}
	}
	defer resp.Body.Close()
	log.Printf("[FORWARD] Web-backend response: %d", resp.StatusCode)
}

func handleHeartbeat(msg mqtt.Message) {
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
	ds.lastSeen = now
	equipmentStore.Unlock()

	log.Printf("[HEARTBEAT] deviceId=%s siteId=%s status=%s", hb.DeviceID, hb.SiteID, hb.Status)
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
