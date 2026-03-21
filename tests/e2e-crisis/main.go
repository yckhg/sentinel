package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/gorilla/websocket"
)

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	backendURL := envOrDefault("WEB_BACKEND_URL", "http://web-backend:8080")
	mqttBrokerURL := envOrDefault("MQTT_BROKER_URL", "tcp://mosquitto:1883")
	adminUser := envOrDefault("ADMIN_USERNAME", "admin")
	adminPass := envOrDefault("ADMIN_PASSWORD", "sentinel1234")

	errors := 0
	pass := func(msg string) { fmt.Printf("  ✓ %s\n", msg) }
	fail := func(msg string) { errors++; fmt.Printf("  ✗ %s\n", msg) }

	fmt.Println("=== E2E Crisis Flow Test ===")
	fmt.Println()

	// Step 1: Login as admin to get JWT
	fmt.Println("--- Step 1: Admin login ---")
	token, err := login(backendURL, adminUser, adminPass)
	if err != nil {
		log.Fatalf("FATAL: admin login failed: %v", err)
	}
	pass("Admin login successful")

	// Step 2: Connect WebSocket to receive crisis_alert
	fmt.Println("--- Step 2: WebSocket connect ---")
	wsCh := make(chan map[string]any, 10)
	wsConn, err := connectWebSocket(backendURL, token, wsCh)
	if err != nil {
		log.Fatalf("FATAL: WebSocket connect failed: %v", err)
	}
	defer wsConn.Close()
	pass("WebSocket connected")

	// Wait for "connected" message
	select {
	case msg := <-wsCh:
		if msg["type"] == "connected" {
			pass("WebSocket connected message received")
		}
	case <-time.After(3 * time.Second):
		fail("WebSocket connected message not received")
	}

	// Step 3: Get initial incident count
	fmt.Println("--- Step 3: Baseline incident count ---")
	beforeCount, err := getIncidentCount(backendURL, token)
	if err != nil {
		log.Fatalf("FATAL: get incidents failed: %v", err)
	}
	pass(fmt.Sprintf("Initial incident count: %d", beforeCount))

	// Step 4: Publish MQTT alert — START TIMING
	fmt.Println("--- Step 4: Publish MQTT alert ---")
	chainStart := time.Now()

	alertPayload := map[string]string{
		"deviceId":    "dev001",
		"siteId":      "site1",
		"type":        "scream",
		"description": "E2E test: Help detected in Factory 1",
		"severity":    "critical",
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
	}
	if err := publishMQTTAlert(mqttBrokerURL, alertPayload); err != nil {
		log.Fatalf("FATAL: MQTT publish failed: %v", err)
	}
	pass("MQTT alert published to safety/site1/alert")

	// Step 5: Wait for WebSocket crisis_alert event
	fmt.Println("--- Step 5: Wait for WebSocket crisis_alert ---")
	wsReceived := false
	timeout := time.After(5 * time.Second)
WaitLoop:
	for {
		select {
		case msg := <-wsCh:
			if msg["type"] == "crisis_alert" {
				chainDuration := time.Since(chainStart)
				pass(fmt.Sprintf("WebSocket crisis_alert received in %v", chainDuration))
				wsReceived = true
				break WaitLoop
			}
		case <-timeout:
			fail("WebSocket crisis_alert NOT received within 5s")
			break WaitLoop
		}
	}

	// Step 6: Verify incident record in database
	fmt.Println("--- Step 6: Verify incident in database ---")
	// Small buffer for DB write completion
	time.Sleep(500 * time.Millisecond)
	afterCount, err := getIncidentCount(backendURL, token)
	if err != nil {
		fail(fmt.Sprintf("Get incidents failed: %v", err))
	} else if afterCount > beforeCount {
		pass(fmt.Sprintf("Incident created in database (count: %d → %d)", beforeCount, afterCount))
	} else {
		fail(fmt.Sprintf("Incident NOT created (count unchanged: %d)", afterCount))
	}

	// Step 7: Verify latest incident has correct data
	fmt.Println("--- Step 7: Verify incident data ---")
	incident, err := getLatestIncident(backendURL, token)
	if err != nil {
		fail(fmt.Sprintf("Get latest incident failed: %v", err))
	} else {
		desc, _ := incident["description"].(string)
		siteID, _ := incident["siteId"].(string)
		if strings.Contains(desc, "E2E test") && siteID == "site1" {
			pass(fmt.Sprintf("Incident data correct: siteId=%s description=%s", siteID, desc))
		} else {
			fail(fmt.Sprintf("Incident data mismatch: siteId=%s description=%s", siteID, desc))
		}
	}

	// Step 8: Check full chain timing
	fmt.Println("--- Step 8: Timing check ---")
	totalDuration := time.Since(chainStart)
	if wsReceived && totalDuration < 5*time.Second {
		pass(fmt.Sprintf("Full chain completed in %v (< 5s)", totalDuration))
	} else if !wsReceived {
		fail("Cannot verify timing — WebSocket event not received")
	} else {
		fail(fmt.Sprintf("Chain too slow: %v (> 5s)", totalDuration))
	}

	// Summary
	fmt.Println()
	fmt.Println("===============================")
	if errors == 0 {
		fmt.Println("Result: ALL CHECKS PASSED")
	} else {
		fmt.Printf("Result: %d check(s) FAILED\n", errors)
		os.Exit(1)
	}
}

// login authenticates with the backend and returns a JWT token.
func login(backendURL, username, password string) (string, error) {
	body, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})

	resp, err := http.Post(backendURL+"/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("HTTP error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		data, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("login returned %d: %s", resp.StatusCode, string(data))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode error: %w", err)
	}

	token, ok := result["token"].(string)
	if !ok || token == "" {
		return "", fmt.Errorf("no token in response")
	}
	return token, nil
}

// connectWebSocket connects to the backend WebSocket and sends received messages to the channel.
func connectWebSocket(backendURL, token string, ch chan<- map[string]any) (*websocket.Conn, error) {
	wsURL := strings.Replace(backendURL, "http://", "ws://", 1) + "/ws?token=" + url.QueryEscape(token)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dial error: %w", err)
	}

	go func() {
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]any
			if json.Unmarshal(message, &msg) == nil {
				ch <- msg
			}
		}
	}()

	return conn, nil
}

// publishMQTTAlert publishes a crisis alert message to the MQTT broker.
func publishMQTTAlert(brokerURL string, payload map[string]string) error {
	opts := mqtt.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID("e2e-crisis-test").
		SetConnectTimeout(5 * time.Second)

	client := mqtt.NewClient(opts)
	tok := client.Connect()
	if !tok.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("MQTT connect timeout")
	}
	if tok.Error() != nil {
		return fmt.Errorf("MQTT connect: %w", tok.Error())
	}
	defer client.Disconnect(250)

	data, _ := json.Marshal(payload)
	pubTok := client.Publish("safety/site1/alert", 2, false, data)
	if !pubTok.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("MQTT publish timeout")
	}
	return pubTok.Error()
}

// getIncidentCount returns the total number of incidents.
func getIncidentCount(backendURL, token string) (int, error) {
	req, _ := http.NewRequest("GET", backendURL+"/api/incidents?limit=1", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var result struct {
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	return result.Pagination.Total, nil
}

// getLatestIncident returns the most recent incident.
func getLatestIncident(backendURL, token string) (map[string]any, error) {
	req, _ := http.NewRequest("GET", backendURL+"/api/incidents?limit=1&page=1", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("no incidents found")
	}
	return result.Data[0], nil
}
