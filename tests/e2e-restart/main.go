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

	mqtt "github.com/eclipse/paho.mqtt.golang"
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

	fmt.Println("=== E2E Restart Flow Test ===")
	fmt.Println()

	// Step 1: Login as admin to get JWT
	fmt.Println("--- Step 1: Admin login ---")
	token, err := login(backendURL, adminUser, adminPass)
	if err != nil {
		log.Fatalf("FATAL: admin login failed: %v", err)
	}
	pass("Admin login successful")

	// Step 2: Subscribe to MQTT restart topic
	fmt.Println("--- Step 2: Subscribe to MQTT restart topic ---")
	restartCh := make(chan map[string]any, 10)
	mqttClient, err := subscribeMQTTRestart(mqttBrokerURL, restartCh)
	if err != nil {
		log.Fatalf("FATAL: MQTT subscribe failed: %v", err)
	}
	defer mqttClient.Disconnect(250)
	pass("Subscribed to safety/site1/cmd/restart")

	// Small delay to ensure subscription is active
	time.Sleep(500 * time.Millisecond)

	// Step 3: Send restart request to web-backend
	fmt.Println("--- Step 3: Send restart request to web-backend ---")
	chainStart := time.Now()

	restartPayload := map[string]string{
		"siteId":   "site1",
		"deviceId": "dev001",
		"reason":   "E2E test restart",
	}
	statusCode, respBody, err := sendRestart(backendURL, token, restartPayload)
	if err != nil {
		fail(fmt.Sprintf("Restart request failed: %v", err))
	} else if statusCode == 200 {
		pass(fmt.Sprintf("Restart request accepted (HTTP %d)", statusCode))
	} else {
		fail(fmt.Sprintf("Restart request returned HTTP %d: %s", statusCode, respBody))
	}

	// Step 4: Verify hw-gateway response
	fmt.Println("--- Step 4: Verify hw-gateway response ---")
	var gwResp map[string]string
	if err := json.Unmarshal([]byte(respBody), &gwResp); err != nil {
		fail(fmt.Sprintf("Failed to parse response: %v", err))
	} else {
		if gwResp["status"] == "sent" && gwResp["topic"] == "safety/site1/cmd/restart" {
			pass(fmt.Sprintf("hw-gateway confirmed: status=%s topic=%s", gwResp["status"], gwResp["topic"]))
		} else {
			fail(fmt.Sprintf("Unexpected response: %v", gwResp))
		}
	}

	// Step 5: Wait for MQTT restart command
	fmt.Println("--- Step 5: Wait for MQTT restart command ---")
	mqttReceived := false
	timeout := time.After(5 * time.Second)
WaitLoop:
	for {
		select {
		case msg := <-restartCh:
			chainDuration := time.Since(chainStart)
			deviceID, _ := msg["deviceId"].(string)
			siteID, _ := msg["siteId"].(string)
			reason, _ := msg["reason"].(string)
			requestedBy, _ := msg["requestedBy"].(string)

			if deviceID == "dev001" && siteID == "site1" {
				pass(fmt.Sprintf("MQTT restart command received in %v", chainDuration))
				mqttReceived = true

				// Step 6: Verify payload contents
				fmt.Println("--- Step 6: Verify MQTT payload ---")
				if reason == "E2E test restart" {
					pass(fmt.Sprintf("Reason correct: %s", reason))
				} else {
					fail(fmt.Sprintf("Reason mismatch: got %q, want %q", reason, "E2E test restart"))
				}
				if requestedBy != "" {
					pass(fmt.Sprintf("RequestedBy present: %s", requestedBy))
				} else {
					fail("RequestedBy is empty")
				}
				if _, ok := msg["timestamp"].(string); ok {
					pass("Timestamp present in payload")
				} else {
					fail("Timestamp missing from payload")
				}
				break WaitLoop
			}
		case <-timeout:
			fail("MQTT restart command NOT received within 5s")
			break WaitLoop
		}
	}

	// Step 7: Timing check
	fmt.Println("--- Step 7: Timing check ---")
	totalDuration := time.Since(chainStart)
	if mqttReceived && totalDuration < 5*time.Second {
		pass(fmt.Sprintf("Full chain completed in %v (< 5s)", totalDuration))
	} else if !mqttReceived {
		fail("Cannot verify timing — MQTT command not received")
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

// subscribeMQTTRestart subscribes to the restart command topic and sends received messages to the channel.
func subscribeMQTTRestart(brokerURL string, ch chan<- map[string]any) (mqtt.Client, error) {
	opts := mqtt.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID("e2e-restart-test").
		SetConnectTimeout(5 * time.Second)

	client := mqtt.NewClient(opts)
	tok := client.Connect()
	if !tok.WaitTimeout(5 * time.Second) {
		return nil, fmt.Errorf("MQTT connect timeout")
	}
	if tok.Error() != nil {
		return nil, fmt.Errorf("MQTT connect: %w", tok.Error())
	}

	subTok := client.Subscribe("safety/site1/cmd/restart", 1, func(_ mqtt.Client, msg mqtt.Message) {
		var payload map[string]any
		if json.Unmarshal(msg.Payload(), &payload) == nil {
			ch <- payload
		}
	})
	if !subTok.WaitTimeout(5 * time.Second) {
		client.Disconnect(250)
		return nil, fmt.Errorf("MQTT subscribe timeout")
	}
	if subTok.Error() != nil {
		client.Disconnect(250)
		return nil, fmt.Errorf("MQTT subscribe: %w", subTok.Error())
	}

	return client, nil
}

// sendRestart sends a restart request to web-backend and returns status code and response body.
func sendRestart(backendURL, token string, payload map[string]string) (int, string, error) {
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", backendURL+"/api/equipment/restart", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("HTTP error: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", fmt.Errorf("read error: %w", err)
	}

	return resp.StatusCode, string(respBody), nil
}
