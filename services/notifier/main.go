package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
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
	Test        bool   `json:"test,omitempty"`
}

type Contact struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Phone       string `json:"phone"`
	Email       string `json:"email"`
	NotifyEmail bool   `json:"notifyEmail"`
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

type SendEmailRequest struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// --- Config ---

type Config struct {
	WebBackendURL     string
	FrontendURL       string
	RecordingURL      string
	KakaoAPIURL       string
	KakaoAPIKey       string
	KakaoSenderKey    string
	KakaoTemplateCode string
	NHNAppKey         string
	NHNSecretKey      string
	NHNSenderNo       string
	SMTPHost          string
	SMTPPort          string
	SMTPUser          string
	SMTPPass          string
	SMTPFrom          string
}

func loadConfig() Config {
	return Config{
		WebBackendURL:     getEnv("WEB_BACKEND_URL", "http://web-backend:8080"),
		FrontendURL:       getEnv("FRONTEND_URL", "http://localhost:3080"),
		RecordingURL:      getEnv("RECORDING_URL", "http://recording:8080"),
		KakaoAPIURL:       getEnv("KAKAO_API_URL", ""),
		KakaoAPIKey:       getEnv("KAKAO_API_KEY", ""),
		KakaoSenderKey:    getEnv("KAKAO_SENDER_KEY", ""),
		KakaoTemplateCode: getEnv("KAKAO_TEMPLATE_CODE", "CRISIS_ALERT"),
		NHNAppKey:         getEnv("NHN_SMS_APP_KEY", ""),
		NHNSecretKey:      getEnv("NHN_SMS_SECRET_KEY", ""),
		NHNSenderNo:       getEnv("NHN_SMS_SENDER_NO", ""),
		SMTPHost:          getEnv("SMTP_HOST", ""),
		SMTPPort:          getEnv("SMTP_PORT", "587"),
		SMTPUser:          getEnv("SMTP_USER", ""),
		SMTPPass:          getEnv("SMTP_PASS", ""),
		SMTPFrom:          getEnv("SMTP_FROM", ""),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// --- Secret Masking (§출력 9 / assertion K) ---
//
// Credentials (KakaoTalk/SMS API + secret keys, SMTP password) must never appear
// in plaintext in any log line, error message or dispatch summary — on both the
// success and the failure path. The known structural leak is that NHN SMS embeds
// NHN_SMS_APP_KEY directly in the request URL, so any net/http transport error
// (an *url.Error carrying the full URL) would print the key when logged with %v.
//
// Defense is two-layered:
//  1. `scrub` replaces every configured secret plaintext with a masked form.
//  2. `logf` routes every log line through `scrub`, so no code path can leak a
//     credential to `docker compose logs notifier`, regardless of how the string
//     was assembled. Error strings that also travel off-box (system-alarm payload)
//     are scrubbed at their source as well.

// secretValues holds the plaintext credential values to redact from all output.
// Populated once at startup from Config; only values long enough to be genuine
// credentials are registered (short/empty values would over-match benign text).
var secretValues []string

func initSecretScrubber(cfg Config) {
	candidates := []string{
		cfg.KakaoAPIKey,
		cfg.KakaoSenderKey,
		cfg.NHNAppKey,
		cfg.NHNSecretKey,
		cfg.SMTPPass,
	}
	secretValues = secretValues[:0]
	for _, c := range candidates {
		if len(c) >= 4 { // avoid redacting trivially short values
			secretValues = append(secretValues, c)
		}
	}
}

// maskSecret keeps a short prefix for diagnostics and replaces the rest with "***".
// The exposed prefix is capped at min(4, len/4) so short credentials never leak a
// large fraction of their bytes (an 8-char secret exposes at most 2 chars; anything
// under 4 chars is fully redacted). §9 permits "keep a leading part, mask the rest".
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	n := len(s) / 4
	if n > 4 {
		n = 4
	}
	if n == 0 {
		return "***"
	}
	return s[:n] + "***"
}

// scrub redacts any occurrence of a configured secret plaintext from s.
func scrub(s string) string {
	for _, sec := range secretValues {
		if sec == "" {
			continue
		}
		s = strings.ReplaceAll(s, sec, maskSecret(sec))
	}
	return s
}

// logf is the single logging entry point; every line is scrubbed of credentials
// before it is written, so no credential can leak into the notifier logs.
func logf(format string, args ...interface{}) {
	log.Print(scrub(fmt.Sprintf(format, args...)))
}

// --- HTTP Client ---

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		ResponseHeaderTimeout: 5 * time.Second,
	},
}

// --- Settings Fetching ---

// fetchSiteURL reads site_url from web-backend's internal settings API.
// Falls back to cfg.FrontendURL if the setting is empty or the request fails.
func fetchSiteURL(cfg Config) string {
	resp, err := httpClient.Get(cfg.WebBackendURL + "/internal/settings/site_url")
	if err != nil {
		logf("[settings] Failed to fetch site_url: %v", err)
		return strings.TrimRight(cfg.FrontendURL, "/")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logf("[settings] site_url returned status %d", resp.StatusCode)
		return strings.TrimRight(cfg.FrontendURL, "/")
	}

	var result struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		logf("[settings] Failed to decode site_url response: %v", err)
		return strings.TrimRight(cfg.FrontendURL, "/")
	}

	if result.Value == "" {
		return strings.TrimRight(cfg.FrontendURL, "/")
	}
	return strings.TrimRight(result.Value, "/")
}

// --- Contact/Link Fetching ---

func fetchContacts(cfg Config) ([]Contact, error) {
	resp, err := httpClient.Get(cfg.WebBackendURL + "/internal/contacts")
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
	payload, err := json.Marshal(map[string]string{"label": label})
	if err != nil {
		return nil, fmt.Errorf("marshal temp link request: %w", err)
	}
	resp, err := httpClient.Post(cfg.WebBackendURL+"/internal/links/temp", "application/json", bytes.NewReader(payload))
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
		logf("[kakao] API not configured, skipping KakaoTalk for %s (%s)", contact.Name, contact.Phone)
		return fmt.Errorf("KakaoTalk API not configured")
	}

	description := alert.Description
	if alert.Test {
		description = "[테스트] " + description
	}

	variables := map[string]string{
		"description": description,
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
		return fmt.Errorf("kakao API call: %s", scrub(err.Error()))
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kakao API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	logf("[kakao] Successfully sent to %s (%s)", contact.Name, contact.Phone)
	return nil
}

// --- NHN Cloud SMS Sending ---

func sendSMS(cfg Config, contact Contact, alert AlertPayload, cctvLink string) error {
	if cfg.NHNAppKey == "" || cfg.NHNSecretKey == "" {
		logf("[sms] NHN Cloud SMS not configured, skipping SMS for %s (%s)", contact.Name, contact.Phone)
		return fmt.Errorf("NHN Cloud SMS not configured")
	}

	prefix := "[위기알림]"
	if alert.Test {
		prefix = "[테스트][위기알림]"
	}
	body := fmt.Sprintf("%s %s\n현장: %s\n장비: %s\n심각도: %s\n시각: %s",
		prefix, alert.Description, alert.SiteID, alert.DeviceID, alert.Severity, alert.Timestamp)
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
		// The request URL embeds NHN_SMS_APP_KEY; a transport error (*url.Error)
		// carries the full URL, so scrub it here so the credential cannot leak
		// through any downstream consumer of this error (logs, system-alarm
		// payload, dispatch result).
		return fmt.Errorf("SMS API call: %s", scrub(err.Error()))
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("SMS API error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	logf("[sms] Successfully sent to %s (%s)", contact.Name, contact.Phone)
	return nil
}

// --- Crisis Email ---

func sendCrisisEmail(cfg Config, contact Contact, alert AlertPayload, cctvLink string) error {
	if cfg.SMTPHost == "" || cfg.SMTPFrom == "" {
		logf("[email] SMTP not configured, skipping email for %s (%s)", contact.Name, contact.Email)
		return fmt.Errorf("SMTP not configured")
	}

	subjectPrefix := "[위기알림]"
	heading := "위기 알림"
	if alert.Test {
		subjectPrefix = "[테스트][위기알림]"
		heading = "[테스트] 위기 알림"
	}
	// Subject carries externally-sourced values (Type/Description originate from
	// the MQTT alert payload). SMTP header injection (CRLF) is defended centrally
	// in sendEmail via sanitizeHeader; here we only assemble the human-readable line.
	subject := fmt.Sprintf("%s %s — %s", subjectPrefix, alert.Type, alert.Description)

	// HTML-escape every externally-sourced value before embedding it into the
	// HTML email body to prevent stored XSS in the recipient's mail client.
	body := fmt.Sprintf(`<html><body>
<h2>%s</h2>
<p><strong>유형:</strong> %s</p>
<p><strong>설명:</strong> %s</p>
<p><strong>현장:</strong> %s</p>
<p><strong>장비:</strong> %s</p>
<p><strong>심각도:</strong> %s</p>
<p><strong>시각:</strong> %s</p>`,
		html.EscapeString(heading),
		html.EscapeString(alert.Type),
		html.EscapeString(alert.Description),
		html.EscapeString(alert.SiteID),
		html.EscapeString(alert.DeviceID),
		html.EscapeString(alert.Severity),
		html.EscapeString(alert.Timestamp))

	if cctvLink != "" {
		body += fmt.Sprintf(`<p><strong>CCTV:</strong> <a href="%s">실시간 보기</a></p>`, html.EscapeString(cctvLink))
	}
	body += `</body></html>`

	if err := sendEmail(cfg, contact.Email, subject, body); err != nil {
		return fmt.Errorf("crisis email to %s: %w", contact.Email, err)
	}

	logf("[email] Crisis alert sent to %s (%s)", contact.Name, contact.Email)
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
			// Scrub in case an error string carries a credential (defense in depth):
			// this payload travels to web-backend and is broadcast to admin clients.
			"kakaoError":  scrub(kakaoErr.Error()),
			"smsError":    scrub(smsErr.Error()),
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
		},
	}

	payload, err := json.Marshal(alarm)
	if err != nil {
		logf("[alarm] Failed to marshal alarm payload: %v", err)
		return
	}

	resp, err := httpClient.Post(cfg.WebBackendURL+"/internal/alarms", "application/json", bytes.NewReader(payload))
	if err != nil {
		logf("[alarm] Failed to send system alarm: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		logf("[alarm] System alarm failed: status %d, body: %s", resp.StatusCode, string(respBody))
		return
	}

	logf("[alarm] System alarm sent for contact %s (%s) — both KakaoTalk and SMS failed", contact.Name, contact.Phone)
}

// --- Recording Archive (Two-Phase) ---

// requestArchiveProtect sends a protect request to the recording service (Phase 1).
// Protects segments from (incident_time - 1h) for all cameras. Finalization happens on incident resolution.
func requestArchiveProtect(cfg Config, alert AlertPayload) {
	if cfg.RecordingURL == "" {
		logf("[archive] Recording URL not configured, skipping protect request")
		return
	}

	// Parse incident timestamp
	incidentTime, err := time.Parse(time.RFC3339, alert.Timestamp)
	if err != nil {
		incidentTime, err = time.Parse("2006-01-02 15:04:05", alert.Timestamp)
		if err != nil {
			logf("[archive] Cannot parse incident timestamp %q: %v", alert.Timestamp, err)
			return
		}
	}

	// Fetch camera list to get all stream keys
	resp, err := httpClient.Get(cfg.WebBackendURL + "/internal/cameras")
	if err != nil {
		logf("[archive] Failed to fetch cameras: %v", err)
		return
	}
	defer resp.Body.Close()

	var cameras []struct {
		StreamKey string `json:"streamKey"`
		Enabled   bool   `json:"enabled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cameras); err != nil {
		logf("[archive] Failed to decode cameras: %v", err)
		return
	}

	var streamKeys []string
	for _, c := range cameras {
		if c.Enabled && c.StreamKey != "" {
			streamKeys = append(streamKeys, c.StreamKey)
		}
	}

	if len(streamKeys) == 0 {
		logf("[archive] No enabled cameras, skipping protect")
		return
	}

	// Build incident ID from alert info
	incidentID := fmt.Sprintf("incident_%s_%s", alert.SiteID, incidentTime.UTC().Format("20060102_150405"))

	protectReq := map[string]any{
		"incidentId":   incidentID,
		"streamKeys":   streamKeys,
		"incidentTime": incidentTime.UTC().Format(time.RFC3339),
	}

	payload, err := json.Marshal(protectReq)
	if err != nil {
		logf("[archive] Failed to marshal protect request: %v", err)
		return
	}

	protectResp, err := httpClient.Post(cfg.RecordingURL+"/api/archives/protect", "application/json", bytes.NewReader(payload))
	if err != nil {
		logf("[archive] Failed to send protect request: %v", err)
		return
	}
	defer protectResp.Body.Close()

	if protectResp.StatusCode >= 200 && protectResp.StatusCode < 300 {
		logf("[archive] Protect request accepted for incident %s (%d cameras)", incidentID, len(streamKeys))
	} else {
		body, _ := io.ReadAll(protectResp.Body)
		logf("[archive] Protect request failed: status %d, body: %s", protectResp.StatusCode, string(body))
	}
}

// --- Notification Dispatch ---

func dispatchNotifications(cfg Config, alert AlertPayload) (int, []NotifyResult) {
	// 1. Fetch contacts from web-backend
	contacts, err := fetchContacts(cfg)
	if err != nil {
		logf("[notify] Failed to fetch contacts: %v", err)
		return 0, nil
	}

	if len(contacts) == 0 {
		logf("[notify] No contacts configured, skipping notification")
		return 0, nil
	}

	logf("[notify] Fetched %d contacts for alert from site %s", len(contacts), alert.SiteID)

	// 2. Request temporary CCTV link (degraded mode if fails)
	cctvLink := ""
	label := fmt.Sprintf("Crisis alert %s", alert.Timestamp)
	tempLink, err := requestTempLink(cfg, label)
	if err != nil {
		logf("[notify] Failed to get temp link (degraded mode): %v", err)
	} else {
		// Construct full URL using site_url from settings (falls back to FRONTEND_URL)
		siteURL := fetchSiteURL(cfg)
		cctvLink = fmt.Sprintf("%s/view/%s", siteURL, tempLink.Token)
		logf("[notify] Temp CCTV link created: %s (expires %s)", cctvLink, tempLink.ExpiresAt)
	}

	// 3. Send to all contacts in parallel with fallback chain: KakaoTalk → SMS → Web alarm
	//    Email is an independent channel — sent in parallel for contacts with notifyEmail=true
	kakaoEnabled := os.Getenv("KAKAO_ENABLED") == "true"
	smsEnabled := os.Getenv("SMS_ENABLED") == "true"
	if !kakaoEnabled {
		logf("[notify] KakaoTalk disabled (KAKAO_ENABLED=false), skipping Kakao channel")
	}
	if !smsEnabled {
		logf("[notify] SMS disabled (SMS_ENABLED=false), skipping SMS channel")
	}

	var wg sync.WaitGroup
	results := make([]NotifyResult, len(contacts))

	for i, contact := range contacts {
		// Send email in parallel (independent channel, does not affect KakaoTalk/SMS)
		if contact.NotifyEmail && contact.Email != "" {
			wg.Add(1)
			go func(c Contact) {
				defer wg.Done()
				if err := sendCrisisEmail(cfg, c, alert, cctvLink); err != nil {
					logf("[notify] Email FAILED for %s (%s): %v", c.Name, c.Email, err)
				} else {
					logf("[notify] Email SUCCESS for %s (%s)", c.Name, c.Email)
				}
			}(contact)
		}

		// KakaoTalk → SMS → System alarm fallback chain
		wg.Add(1)
		go func(idx int, c Contact) {
			defer wg.Done()
			result := NotifyResult{
				ContactID:   c.ID,
				ContactName: c.Name,
			}

			// Step 1: Try KakaoTalk (if enabled)
			var kakaoErr error
			if !kakaoEnabled {
				// Disabled channel: never call sendKakaoTalk (no external send
				// attempt is logged); record the skip and proceed to the next step.
				kakaoErr = fmt.Errorf("KakaoTalk disabled (KAKAO_ENABLED=false)")
				logf("[notify] KakaoTalk skipped for %s (%s) — channel disabled, proceeding to SMS/fallback", c.Name, c.Phone)
			} else {
				kakaoErr = sendKakaoTalk(cfg, c, alert, cctvLink)
				if kakaoErr == nil {
					result.Channel = "kakaotalk"
					result.Success = true
					logf("[notify] KakaoTalk SUCCESS for %s (%s)", c.Name, c.Phone)
					results[idx] = result
					return
				}
				logf("[notify] KakaoTalk FAILED for %s (%s): %v — falling back to SMS", c.Name, c.Phone, kakaoErr)
			}

			// Step 2: Fallback to SMS (if enabled)
			var smsErr error
			if !smsEnabled {
				// Disabled channel: never call sendSMS (no external send attempt is
				// logged); record the skip and proceed to the system-alarm fallback.
				smsErr = fmt.Errorf("SMS disabled (SMS_ENABLED=false)")
				logf("[notify] SMS skipped for %s (%s) — channel disabled, proceeding to system alarm", c.Name, c.Phone)
			} else {
				smsErr = sendSMS(cfg, c, alert, cctvLink)
				if smsErr == nil {
					result.Channel = "sms"
					result.Success = true
					logf("[notify] SMS SUCCESS for %s (%s)", c.Name, c.Phone)
					results[idx] = result
					return
				}
				logf("[notify] SMS FAILED for %s (%s): %v — sending system alarm", c.Name, c.Phone, smsErr)
			}

			// Step 3: Both channels unavailable or failed — send system alarm to web-backend
			sendSystemAlarm(cfg, c, alert, kakaoErr, smsErr)
			result.Channel = "alarm"
			result.Success = false
			result.Error = scrub(fmt.Sprintf("kakao: %s; sms: %s", kakaoErr.Error(), smsErr.Error()))
			results[idx] = result
		}(i, contact)
	}

	wg.Wait()

	// Log email dispatch summary
	emailCount := 0
	for _, c := range contacts {
		if c.NotifyEmail && c.Email != "" {
			emailCount++
		}
	}
	if emailCount > 0 {
		logf("[notify] Email dispatched to %d contacts", emailCount)
	}

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
		if alert.Timestamp == "" {
			http.Error(w, `{"error":"timestamp is required"}`, http.StatusBadRequest)
			return
		}

		// Fallback: description/severity are optional in the MQTT contract.
		// Inject sensible defaults so downstream template variables are never empty.
		if alert.Description == "" {
			alert.Description = alert.Type + " at " + alert.SiteID
			logf("[notify] description missing, using fallback: %q", alert.Description)
		}
		if alert.Severity == "" {
			alert.Severity = "unknown"
			logf("[notify] severity missing, using fallback: %q", alert.Severity)
		}

		logf("[notify] Received alert: site=%s device=%s type=%s severity=%s",
			alert.SiteID, alert.DeviceID, alert.Type, alert.Severity)

		// Dispatch notifications asynchronously. Track the goroutine in
		// inflightDispatch so graceful shutdown (#40) can drain crisis-alert
		// dispatches already in progress instead of losing them on SIGTERM.
		inflightDispatch.Add(1)
		go func() {
			defer inflightDispatch.Done()
			contactCount, results := dispatchNotifications(cfg, alert)
			successCount := 0
			channels := map[string]int{}
			for _, r := range results {
				if r.Success {
					successCount++
					channels[r.Channel]++
				}
			}
			logf("[notify] Dispatch complete: %d/%d contacts notified (kakao:%d sms:%d failed:%d)",
				successCount, contactCount, channels["kakaotalk"], channels["sms"], contactCount-successCount)

			// Request segment protection for this incident (Phase 1 of two-phase archiving)
			requestArchiveProtect(cfg, alert)
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

// --- Internal IP Check ---

func isInternalIP(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	// Docker bridge networks use private ranges
	privateRanges := []struct {
		network *net.IPNet
	}{
		{mustParseCIDR("10.0.0.0/8")},
		{mustParseCIDR("172.16.0.0/12")},
		{mustParseCIDR("192.168.0.0/16")},
		{mustParseCIDR("127.0.0.0/8")},
		{mustParseCIDR("::1/128")},
		{mustParseCIDR("fc00::/7")},
	}

	for _, r := range privateRanges {
		if r.network.Contains(ip) {
			return true
		}
	}
	return false
}

func mustParseCIDR(s string) *net.IPNet {
	_, network, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return network
}

// --- HTML Sanitization ---

var (
	// Tags whose content is dangerous and must be dropped whole (tag + content).
	reScriptTag = regexp.MustCompile(`(?is)<script[\s>].*?</script>`)
	reIframeTag = regexp.MustCompile(`(?is)<iframe[\s>].*?</iframe>`)
	reStyleTag  = regexp.MustCompile(`(?is)<style[\s>].*?</style>`)
	// Whole-tag matcher. Quoted attribute values are consumed as units so that a
	// '>' inside an attribute value cannot terminate the tag early.
	reHTMLTag = regexp.MustCompile(`(?s)<(/?)\s*([a-zA-Z][a-zA-Z0-9]*)((?:"[^"]*"|'[^']*'|[^>"'])*)>`)
	// Attribute name (+ optional value) extractor over a tag's attribute section.
	reAttr = regexp.MustCompile(`([a-zA-Z_:][-a-zA-Z0-9_:.]*)(?:\s*=\s*("[^"]*"|'[^']*'|[^\s"'>]+))?`)
)

// allowedTags is the set of safe HTML tags (bare shells of others are removed,
// their text content preserved).
var allowedTags = map[string]bool{
	"p": true, "a": true, "br": true, "strong": true, "em": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"html": true, "head": true, "body": true,
}

// allowedAttrs is a per-tag attribute whitelist. Any attribute not listed here
// (including every on* event handler) is dropped. This structurally removes the
// on*-handler and dangerous-URI vectors instead of pattern-matching them.
var allowedAttrs = map[string]map[string]bool{
	"a": {"href": true},
}

// urlAttrs are attributes whose value is a URL and must pass a scheme check.
var urlAttrs = map[string]bool{"href": true, "src": true, "action": true}

// isSafeURL returns true only for relative URLs or the http/https/mailto/tel
// schemes. HTML entities and control/whitespace obfuscation (e.g. `&#106;avascript:`
// or `java\tscript:`) are decoded/stripped before the scheme is inspected, closing
// the quoted/unquoted `javascript:`, `data:` and `vbscript:` bypasses.
func isSafeURL(raw string) bool {
	v := raw
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		v = v[1 : len(v)-1]
	}
	v = html.UnescapeString(v)
	// Drop all control/whitespace chars used to obfuscate the scheme.
	v = strings.Map(func(r rune) rune {
		if r <= ' ' || r == 0x7f {
			return -1
		}
		return r
	}, v)
	v = strings.ToLower(v)
	for i := 0; i < len(v); i++ {
		switch v[i] {
		case ':':
			switch v[:i] {
			case "http", "https", "mailto", "tel":
				return true
			}
			return false // any other explicit scheme (javascript/data/vbscript/...) is unsafe
		case '/', '?', '#':
			return true // path/query/fragment reached first → relative URL, safe
		}
	}
	return true // no scheme delimiter → relative, safe
}

// normalizeAttrValue returns the attribute value always double-quoted, with any
// embedded double quote escaped so it cannot break out of the attribute.
func normalizeAttrValue(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		v = v[1 : len(v)-1]
	}
	return `"` + strings.ReplaceAll(v, `"`, "&quot;") + `"`
}

func sanitizeHTML(input string) string {
	// 1. Drop script/iframe/style tags together with their content.
	result := reScriptTag.ReplaceAllString(input, "")
	result = reIframeTag.ReplaceAllString(result, "")
	result = reStyleTag.ReplaceAllString(result, "")

	// 2. Rewrite every remaining tag: drop disallowed tags (keeping their text),
	//    and for allowed tags emit only whitelisted, scheme-checked attributes.
	result = reHTMLTag.ReplaceAllStringFunc(result, func(tag string) string {
		m := reHTMLTag.FindStringSubmatch(tag)
		if m == nil {
			return ""
		}
		closing := m[1] == "/"
		name := strings.ToLower(m[2])
		if !allowedTags[name] {
			return "" // strip the tag shell, preserve surrounding content
		}
		if closing {
			return "</" + name + ">"
		}

		var b strings.Builder
		b.WriteString("<" + name)
		attrWhitelist := allowedAttrs[name]
		for _, a := range reAttr.FindAllStringSubmatch(m[3], -1) {
			an := strings.ToLower(a[1])
			if !attrWhitelist[an] {
				continue // not whitelisted (covers on* handlers, style, etc.)
			}
			val := a[2]
			if urlAttrs[an] && !isSafeURL(val) {
				continue // dangerous URI scheme
			}
			if val == "" {
				b.WriteString(" " + an)
			} else {
				b.WriteString(" " + an + "=" + normalizeAttrValue(val))
			}
		}
		b.WriteString(">")
		return b.String()
	})

	return result
}

// --- Email Sending ---

// sanitizeHeader strips CR, LF and other control characters from a value that
// will be placed into an SMTP header (To / Subject), preventing header injection
// (extra recipients/headers or body smuggling) from attacker-controlled input.
func sanitizeHeader(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

func sendEmail(cfg Config, to, subject, body string) error {
	addr := fmt.Sprintf("%s:%s", cfg.SMTPHost, cfg.SMTPPort)

	// Defend SMTP header injection centrally for every email path.
	to = sanitizeHeader(to)
	subject = sanitizeHeader(subject)

	mime := "MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=\"UTF-8\"\r\n"
	msg := []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n%s\r\n%s",
		cfg.SMTPFrom, to, subject, mime, body))

	auth := smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPHost)
	if err := smtp.SendMail(addr, auth, cfg.SMTPFrom, []string{to}, msg); err != nil {
		return fmt.Errorf("smtp send: %s", scrub(err.Error()))
	}
	return nil
}

func handleSendEmail(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check source IP — only allow internal Docker network
		if !isInternalIP(r.RemoteAddr) {
			logf("[email] Rejected request from external IP: %s", r.RemoteAddr)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "access denied"})
			return
		}

		if cfg.SMTPHost == "" || cfg.SMTPFrom == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": "email not configured"})
			return
		}

		// Limit request body to 1MB
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

		var req SendEmailRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid JSON payload or body too large"}`, http.StatusBadRequest)
			return
		}

		if req.To == "" {
			http.Error(w, `{"error":"to is required"}`, http.StatusBadRequest)
			return
		}
		if req.Subject == "" {
			http.Error(w, `{"error":"subject is required"}`, http.StatusBadRequest)
			return
		}
		if req.Body == "" {
			http.Error(w, `{"error":"body is required"}`, http.StatusBadRequest)
			return
		}

		// Sanitize HTML body
		req.Body = sanitizeHTML(req.Body)

		if err := sendEmail(cfg, req.To, req.Subject, req.Body); err != nil {
			logf("[email] Failed to send email to %s: %v", req.To, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			// §9: error messages must not leak credentials — scrub before returning
			// the SMTP error to the caller (this response bypasses logf/scrub otherwise).
			json.NewEncoder(w).Encode(map[string]string{"error": scrub(fmt.Sprintf("failed to send email: %v", err))})
			return
		}

		logf("[email] Successfully sent email to %s (subject: %s)", req.To, req.Subject)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
	}
}

// --- Main ---

func main() {
	cfg := loadConfig()
	initSecretScrubber(cfg)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","service":"notifier"}`))
	})

	mux.HandleFunc("POST /api/notify", handleNotify(cfg))
	mux.HandleFunc("POST /api/send-email", handleSendEmail(cfg))

	srv := newHTTPServer(maxBytesMiddleware(mux))

	// Graceful shutdown (#40): on SIGTERM/SIGINT stop accepting new requests
	// (srv.Shutdown), then wait for crisis-alert dispatch goroutines already in
	// flight so they are not truncated/lost. New /api/notify requests can no
	// longer start a dispatch once Shutdown returns, so draining terminates.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		logf("shutting down: draining in-flight notifications...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logf("http shutdown: %v", err)
		}
		if !waitTimeout(&inflightDispatch, 15*time.Second) {
			logf("shutdown: timed out waiting for in-flight notifications")
		}
	}()

	logf("notifier listening on :8080 (kakao configured: %v, sms configured: %v, smtp configured: %v, frontend: %s)",
		cfg.KakaoAPIURL != "", cfg.NHNAppKey != "", cfg.SMTPHost != "", cfg.FrontendURL)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// inflightDispatch tracks crisis-alert dispatch goroutines spawned by
// /api/notify so graceful shutdown can wait for them to finish.
var inflightDispatch sync.WaitGroup

// waitTimeout waits for wg with an upper bound, returning true if it completed
// and false if the deadline elapsed first.
func waitTimeout(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// maxRequestBodyBytes caps request bodies (#41). Previously only /api/send-email
// bounded its body (1 MB) while its sibling /api/notify did not; this middleware
// applies the same limit uniformly so an unbounded body cannot exhaust memory.
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
// open indefinitely (Slowloris).
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
