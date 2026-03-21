package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

// --- Types ---

type AlertPayload struct {
	DeviceID    string `json:"deviceId"`
	SiteID      string `json:"siteId"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
	Timestamp   string `json:"timestamp"`
}

type Contact struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

type TempLinkResponse struct {
	ID        string `json:"id"`
	Token     string `json:"token"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expiresAt"`
}

type SiteInfo struct {
	ID           int    `json:"id"`
	Address      string `json:"address"`
	ManagerName  string `json:"managerName"`
	ManagerPhone string `json:"managerPhone"`
}

type KakaoSendRequest struct {
	Phone        string `json:"phone"`
	TemplateCode string `json:"templateCode"`
	Variables    map[string]string `json:"variables"`
}

type NotifyResult struct {
	ContactID   int    `json:"contactId"`
	ContactName string `json:"contactName"`
	Channel     string `json:"channel"`
	Success     bool   `json:"success"`
	Error       string `json:"error,omitempty"`
}

type SMSSendRequest struct {
	Body       string   `json:"body"`
	SendNo     string   `json:"sendNo"`
	RecipientList []SMSRecipient `json:"recipientList"`
}

type SMSRecipient struct {
	RecipientNo string `json:"recipientNo"`
}

type AlarmPayload struct {
	Type    string                 `json:"type"`
	Message string                 `json:"message"`
	Details map[string]interface{} `json:"details,omitempty"`
}

// --- Config ---

type Config struct {
	WebBackendURL     string
	FrontendURL       string
	KakaoAPIURL       string
	KakaoAPIKey       string
	KakaoSenderKey    string
	KakaoTemplateCode string
	NHNAppKey         string
	NHNSecretKey      string
	NHNSenderNo       string
}

func loadConfig() Config {
	return Config{
		WebBackendURL:     getEnv("WEB_BACKEND_URL", "http://web-backend:8080"),
		FrontendURL:       getEnv("FRONTEND_URL", "http://localhost:3080"),
		KakaoAPIURL:       getEnv("KAKAO_API_URL", ""),
		KakaoAPIKey:       getEnv("KAKAO_API_KEY", ""),
		KakaoSenderKey:    getEnv("KAKAO_SENDER_KEY", ""),
		KakaoTemplateCode: getEnv("KAKAO_TEMPLATE_CODE", "CRISIS_ALERT"),
		NHNAppKey:         getEnv("NHN_SMS_APP_KEY", ""),
		NHNSecretKey:      getEnv("NHN_SMS_SECRET_KEY", ""),
		NHNSenderNo:       getEnv("NHN_SMS_SENDER_NO", ""),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// --- HTTP Client ---

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		ResponseHeaderTimeout: 5 * time.Second,
	},
}

// --- Contact/Link Fetching ---

func fetchContacts(cfg Config) ([]Contact, error) {
	resp, err := httpClient.Get(cfg.WebBackendURL + "/api/contacts")
	if err != nil {
		return nil, fmt.Errorf("fetch contacts: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetch contacts: status %d, body: %s", resp.StatusCode, string(body))
	}

	var contacts []Contact
	if err := json.NewDecoder(resp.Body).Decode(&contacts); err != nil {
		return nil, fmt.Errorf("decode contacts: %w", err)
	}
	return contacts, nil
}

func requestTempLink(cfg Config, label string) (*TempLinkResponse, error) {
	payload, _ := json.Marshal(map[string]string{"label": label})
	resp, err := httpClient.Post(cfg.WebBackendURL+"/api/links/temp", "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("request temp link: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("request temp link: status %d, body: %s", resp.StatusCode, string(body))
	}

	var link TempLinkResponse
	if err := json.NewDecoder(resp.Body).Decode(&link); err != nil {
		return nil, fmt.Errorf("decode temp link: %w", err)
	}
	return &link, nil
}

// --- KakaoTalk 알림톡 Sending ---

func sendKakaoTalk(cfg Config, contact Contact, alert AlertPayload, cctvLink string) error {
	if cfg.KakaoAPIURL == "" || cfg.KakaoAPIKey == "" {
		log.Printf("[kakao] API not configured, skipping KakaoTalk for %s (%s)", contact.Name, contact.Phone)
		return fmt.Errorf("KakaoTalk API not configured")
	}

	variables := map[string]string{
		"description": alert.Description,
		"siteId":      alert.SiteID,
		"deviceId":    alert.DeviceID,
		"severity":    alert.Severity,
		"timestamp":   alert.Timestamp,
		"cctvLink":    cctvLink,
	}

	reqBody := KakaoSendRequest{
		Phone:        contact.Phone,
		TemplateCode: cfg.KakaoTemplateCode,
		Variables:    variables,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal kakao request: %w", err)
	}

	req, err := http.NewRequest("POST", cfg.KakaoAPIURL+"/v1/alimtalk/send", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create kakao request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", cfg.KakaoAPIKey)
	req.Header.Set("X-Sender-Key", cfg.KakaoSenderKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("kakao API call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kakao API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	log.Printf("[kakao] Successfully sent to %s (%s)", contact.Name, contact.Phone)
	return nil
}

// --- NHN Cloud SMS Sending ---

func sendSMS(cfg Config, contact Contact, alert AlertPayload, cctvLink string) error {
	if cfg.NHNAppKey == "" || cfg.NHNSecretKey == "" {
		log.Printf("[sms] NHN Cloud SMS not configured, skipping SMS for %s (%s)", contact.Name, contact.Phone)
		return fmt.Errorf("NHN Cloud SMS not configured")
	}

	body := fmt.Sprintf("[위기알림] %s\n현장: %s\n장비: %s\n심각도: %s\n시각: %s",
		alert.Description, alert.SiteID, alert.DeviceID, alert.Severity, alert.Timestamp)
	if cctvLink != "" {
		body += fmt.Sprintf("\nCCTV: %s", cctvLink)
	}

	smsReq := SMSSendRequest{
		Body:   body,
		SendNo: cfg.NHNSenderNo,
		RecipientList: []SMSRecipient{
			{RecipientNo: contact.Phone},
		},
	}

	payload, err := json.Marshal(smsReq)
	if err != nil {
		return fmt.Errorf("marshal SMS request: %w", err)
	}

	url := fmt.Sprintf("https://api-sms.cloud.toast.com/sms/v3.0/appKeys/%s/sender/sms", cfg.NHNAppKey)
	req, err := http.NewRequest("POST", url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create SMS request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	req.Header.Set("X-Secret-Key", cfg.NHNSecretKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("SMS API call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("SMS API error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("[sms] Successfully sent to %s (%s)", contact.Name, contact.Phone)
	return nil
}

// --- System Alarm ---

func sendSystemAlarm(cfg Config, contact Contact, alert AlertPayload, kakaoErr, smsErr error) {
	alarm := AlarmPayload{
		Type:    "notification_failure",
		Message: fmt.Sprintf("Failed to deliver crisis alert to contact %s (KakaoTalk + SMS both failed)", contact.Name),
		Details: map[string]interface{}{
			"contactId":   contact.ID,
			"contactName": contact.Name,
			"contactPhone": contact.Phone,
			"siteId":      alert.SiteID,
			"deviceId":    alert.DeviceID,
			"kakaoError":  kakaoErr.Error(),
			"smsError":    smsErr.Error(),
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
		},
	}

	payload, err := json.Marshal(alarm)
	if err != nil {
		log.Printf("[alarm] Failed to marshal alarm payload: %v", err)
		return
	}

	resp, err := httpClient.Post(cfg.WebBackendURL+"/api/alarms", "application/json", bytes.NewReader(payload))
	if err != nil {
		log.Printf("[alarm] Failed to send system alarm: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("[alarm] System alarm failed: status %d, body: %s", resp.StatusCode, string(respBody))
		return
	}

	log.Printf("[alarm] System alarm sent for contact %s (%s) — both KakaoTalk and SMS failed", contact.Name, contact.Phone)
}

// --- Notification Dispatch ---

func dispatchNotifications(cfg Config, alert AlertPayload) (int, []NotifyResult) {
	// 1. Fetch contacts from web-backend
	contacts, err := fetchContacts(cfg)
	if err != nil {
		log.Printf("[notify] Failed to fetch contacts: %v", err)
		return 0, nil
	}

	if len(contacts) == 0 {
		log.Printf("[notify] No contacts configured, skipping notification")
		return 0, nil
	}

	log.Printf("[notify] Fetched %d contacts for alert from site %s", len(contacts), alert.SiteID)

	// 2. Request temporary CCTV link (degraded mode if fails)
	cctvLink := ""
	label := fmt.Sprintf("Crisis alert %s", alert.Timestamp)
	tempLink, err := requestTempLink(cfg, label)
	if err != nil {
		log.Printf("[notify] Failed to get temp link (degraded mode): %v", err)
	} else {
		// Construct full URL pointing to /view/{token} on web-frontend
		cctvLink = fmt.Sprintf("%s/view/%s", cfg.FrontendURL, tempLink.Token)
		log.Printf("[notify] Temp CCTV link created: %s (expires %s)", cctvLink, tempLink.ExpiresAt)
	}

	// 3. Send to all contacts in parallel with fallback chain: KakaoTalk → SMS → Web alarm
	var wg sync.WaitGroup
	results := make([]NotifyResult, len(contacts))

	for i, contact := range contacts {
		wg.Add(1)
		go func(idx int, c Contact) {
			defer wg.Done()
			result := NotifyResult{
				ContactID:   c.ID,
				ContactName: c.Name,
			}

			// Step 1: Try KakaoTalk
			kakaoErr := sendKakaoTalk(cfg, c, alert, cctvLink)
			if kakaoErr == nil {
				result.Channel = "kakaotalk"
				result.Success = true
				log.Printf("[notify] KakaoTalk SUCCESS for %s (%s)", c.Name, c.Phone)
				results[idx] = result
				return
			}
			log.Printf("[notify] KakaoTalk FAILED for %s (%s): %v — falling back to SMS", c.Name, c.Phone, kakaoErr)

			// Step 2: Fallback to SMS
			smsErr := sendSMS(cfg, c, alert, cctvLink)
			if smsErr == nil {
				result.Channel = "sms"
				result.Success = true
				log.Printf("[notify] SMS SUCCESS for %s (%s)", c.Name, c.Phone)
				results[idx] = result
				return
			}
			log.Printf("[notify] SMS FAILED for %s (%s): %v — sending system alarm", c.Name, c.Phone, smsErr)

			// Step 3: Both failed — send system alarm to web-backend
			sendSystemAlarm(cfg, c, alert, kakaoErr, smsErr)
			result.Channel = "alarm"
			result.Success = false
			result.Error = fmt.Sprintf("kakao: %s; sms: %s", kakaoErr.Error(), smsErr.Error())
			results[idx] = result
		}(i, contact)
	}

	wg.Wait()
	return len(contacts), results
}

// --- HTTP Handlers ---

func handleNotify(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var alert AlertPayload
		if err := json.NewDecoder(r.Body).Decode(&alert); err != nil {
			http.Error(w, `{"error":"invalid JSON payload"}`, http.StatusBadRequest)
			return
		}

		// Validate required fields
		if alert.SiteID == "" {
			http.Error(w, `{"error":"siteId is required"}`, http.StatusBadRequest)
			return
		}
		if alert.DeviceID == "" {
			http.Error(w, `{"error":"deviceId is required"}`, http.StatusBadRequest)
			return
		}
		if alert.Type == "" {
			http.Error(w, `{"error":"type is required"}`, http.StatusBadRequest)
			return
		}
		if alert.Description == "" {
			http.Error(w, `{"error":"description is required"}`, http.StatusBadRequest)
			return
		}
		if alert.Severity == "" {
			http.Error(w, `{"error":"severity is required"}`, http.StatusBadRequest)
			return
		}
		if alert.Timestamp == "" {
			http.Error(w, `{"error":"timestamp is required"}`, http.StatusBadRequest)
			return
		}

		log.Printf("[notify] Received alert: site=%s device=%s type=%s severity=%s",
			alert.SiteID, alert.DeviceID, alert.Type, alert.Severity)

		// Dispatch notifications asynchronously
		go func() {
			contactCount, results := dispatchNotifications(cfg, alert)
			successCount := 0
			channels := map[string]int{}
			for _, r := range results {
				if r.Success {
					successCount++
					channels[r.Channel]++
				}
			}
			log.Printf("[notify] Dispatch complete: %d/%d contacts notified (kakao:%d sms:%d failed:%d)",
				successCount, contactCount, channels["kakaotalk"], channels["sms"], contactCount-successCount)
		}()

		// Return immediately (accepted, processing async)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":       "accepted",
			"contactCount": 0, // async, count not yet known
		})
	}
}

// --- Main ---

func main() {
	cfg := loadConfig()

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","service":"notifier"}`))
	})

	mux.HandleFunc("POST /api/notify", handleNotify(cfg))

	log.Printf("notifier listening on :8080 (kakao configured: %v, sms configured: %v, frontend: %s)",
		cfg.KakaoAPIURL != "", cfg.NHNAppKey != "", cfg.FrontendURL)
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}
