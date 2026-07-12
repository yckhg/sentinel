package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
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
	"runtime/debug"
	"strconv"
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

	// DedupWindow is the incident dedup window (§출력 12). <=0 disables dedup.
	DedupWindow time.Duration
	// ChannelRetryMax / ChannelRetryBackoff drive the pre-fallback same-channel
	// retry on fast transient errors (§출력 13).
	ChannelRetryMax     int
	ChannelRetryBackoff time.Duration
	// ArchiveProtectTimeout bounds the background recording-protect HTTP request so a
	// hung/slow recording endpoint cannot accumulate goroutines. <=0 → 5s default.
	ArchiveProtectTimeout time.Duration
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

		// §출력 12: default a conservative short window (60s); 0 disables dedup.
		DedupWindow: time.Duration(getEnvInt("DEDUP_WINDOW_SECONDS", 60)) * time.Second,
		// §출력 13: default a small retry cap (1) + short backoff (200ms) so the
		// retry stays inside the §출력 7 per-channel 12s budget. 0 → immediate fallback.
		ChannelRetryMax:     getEnvInt("CHANNEL_RETRY_MAX", 1),
		ChannelRetryBackoff: time.Duration(getEnvInt("CHANNEL_RETRY_BACKOFF_MS", 200)) * time.Millisecond,
		// Bound the background protect delivery to a few seconds (default 5s) so a
		// recording outage cannot hang/leak goroutines. Alert path stays unaffected.
		ArchiveProtectTimeout: time.Duration(getEnvInt("ARCHIVE_PROTECT_TIMEOUT_MS", 5000)) * time.Millisecond,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getEnvInt reads an integer env var, falling back on unset/unparseable values.
// A literal "0" is honored (returns 0), which is how the dedup/retry knobs are
// explicitly disabled.
func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
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

// maskPhone redacts the middle of a phone number for logs, keeping the leading
// 3 and trailing 4 digits (e.g. "010-****-5678"). Numbers with fewer than 7
// digits are fully masked. Empty input stays empty. PII (phone/email) must not
// be logged in plaintext (#43).
func maskPhone(p string) string {
	if p == "" {
		return ""
	}
	digits := make([]byte, 0, len(p))
	for i := 0; i < len(p); i++ {
		if p[i] >= '0' && p[i] <= '9' {
			digits = append(digits, p[i])
		}
	}
	if len(digits) < 7 {
		return "****"
	}
	d := string(digits)
	return d[:3] + "-****-" + d[len(d)-4:]
}

// maskEmail redacts the local part of an email for logs, keeping only its first
// character (e.g. "j***@example.com"). Empty input stays empty (#43).
func maskEmail(e string) string {
	if e == "" {
		return ""
	}
	at := strings.LastIndex(e, "@")
	if at <= 0 {
		return "***"
	}
	local, domain := e[:at], e[at:]
	if len(local) == 1 {
		return "*" + domain
	}
	return local[:1] + strings.Repeat("*", len(local)-1) + domain
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

// recoverGoroutine is a deferred guard for background goroutines (#58). An
// unrecovered panic in any goroutine (e.g. a nil map/pointer in the dispatch,
// per-contact, or email paths) crashes the entire notifier process, after which
// every subsequent crisis alert is lost. Recovering keeps this safety-critical
// daemon alive; the panic value and stack are logged (scrubbed) for diagnosis.
func recoverGoroutine(label string) {
	if r := recover(); r != nil {
		logf("[panic] recovered in %s: %v\n%s", label, r, debug.Stack())
	}
}

// --- HTTP Client ---

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		ResponseHeaderTimeout: 5 * time.Second,
	},
}

// --- Incident Dedup (§출력 12 / assertion N) ---
//
// Same incident key (siteId,deviceId,type) re-received within DEDUP_WINDOW_SECONDS
// is suppressed: no contact dispatch, no system alarm, no archive-protect. The very
// first event of a key is never suppressed, test:true events are excluded entirely
// (never judged, never recorded — so a test injection cannot poison the cache and
// swallow a real crisis right after), the check-and-record is atomic under a mutex,
// and expired entries are evicted so the cache cannot grow without bound.

type dedupCache struct {
	mu      sync.Mutex
	entries map[string]time.Time
}

func newDedupCache() *dedupCache {
	return &dedupCache{entries: make(map[string]time.Time)}
}

// dedupKey builds the incident dedup key from the alert.
func dedupKey(alert AlertPayload) string {
	return alert.SiteID + "\x1f" + alert.DeviceID + "\x1f" + alert.Type
}

// checkAndRecord atomically decides whether key is a duplicate within window and,
// if not, records now as its latest sighting. Returns true when the event must be
// suppressed (a live entry already exists), false for the first/expired sighting.
// window <= 0 disables dedup (always returns false, records nothing). Expired
// entries are evicted on every call so the map stays bounded.
func (d *dedupCache) checkAndRecord(key string, window time.Duration, now time.Time) bool {
	if window <= 0 {
		return false // dedup disabled — every event is a "first"
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	// Evict everything outside the window first; whatever remains is a live hit.
	for k, t := range d.entries {
		if now.Sub(t) >= window {
			delete(d.entries, k)
		}
	}
	if _, live := d.entries[key]; live {
		return true // duplicate inside window → suppress
	}
	d.entries[key] = now
	return false
}

// --- External-Channel Retry (§출력 13 / assertion O) ---

// isTimeoutErr reports whether err is a request timeout (response-header/overall
// deadline). Timeouts are NOT retried — the time budget is already spent, so a
// retry would only add crisis latency; we fall back immediately instead.
func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return ne.Timeout()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return os.IsTimeout(err)
}

// perChannelCap bounds one external channel's total wall time incl. retries (§출력 7).
const perChannelCap = 12 * time.Second

// sendChannelWithRetry drives a single external channel with immediate same-channel
// retry (§출력 13). `once` performs one send attempt and returns (retryable, err):
// retryable is true only for FAST transient failures (5xx / connection-refused) —
// timeouts and permanent failures (channel disabled, credentials missing) return
// retryable=false and fall back immediately. Retries are capped by max, spaced by
// backoff, and never started if they would breach the §출력 7 12s per-channel cap.
func sendChannelWithRetry(channel, name, phone string, max int, backoff time.Duration, once func() (bool, error)) error {
	if max < 0 {
		max = 0
	}
	start := time.Now()
	var lastErr error
	for attempt := 0; ; attempt++ {
		retryable, err := once()
		if err == nil {
			if attempt > 0 {
				logf("[%s] Retry succeeded on attempt %d for %s (%s)", channel, attempt+1, name, phone)
			}
			return nil
		}
		lastErr = err
		if !retryable {
			// Timeout or permanent failure → no retry, immediate fallback.
			return lastErr
		}
		if attempt >= max {
			if max > 0 {
				logf("[%s] Retries exhausted (max=%d) for %s (%s), falling back: %v", channel, max, name, phone, err)
			}
			return lastErr
		}
		if time.Since(start)+backoff >= perChannelCap {
			logf("[%s] Retry budget (12s) reached for %s (%s), falling back: %v", channel, name, phone, err)
			return lastErr
		}
		logf("[%s] Transient failure for %s (%s), retrying same channel (attempt %d/%d): %v",
			channel, name, phone, attempt+1, max, err)
		time.Sleep(backoff)
	}
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

// sendKakaoTalkOnce performs a single KakaoTalk send attempt and classifies the
// failure for the retry driver (§출력 13): the first return value is `retryable` —
// true only for fast transient errors (5xx or a non-timeout transport error such
// as connection-refused). Timeouts and permanent failures (unconfigured channel)
// return retryable=false so the caller falls back immediately without retrying.
func sendKakaoTalkOnce(cfg Config, contact Contact, alert AlertPayload, cctvLink string) (bool, error) {
	if cfg.KakaoAPIURL == "" || cfg.KakaoAPIKey == "" {
		logf("[kakao] API not configured, skipping KakaoTalk for %s (%s)", contact.Name, maskPhone(contact.Phone))
		return false, fmt.Errorf("KakaoTalk API not configured") // permanent → no retry
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
		return false, fmt.Errorf("marshal kakao request: %w", err)
	}

	req, err := http.NewRequest("POST", cfg.KakaoAPIURL+"/v1/alimtalk/send", bytes.NewReader(payload))
	if err != nil {
		return false, fmt.Errorf("create kakao request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", cfg.KakaoAPIKey)
	req.Header.Set("X-Sender-Key", cfg.KakaoSenderKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		// Transport error: retry only fast failures (connection-refused etc.),
		// never timeouts (§출력 13 — timeout already spent the budget).
		return !isTimeoutErr(err), fmt.Errorf("kakao API call: %s", scrub(err.Error()))
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		// 5xx is a fast transient server error → retryable; 4xx is permanent.
		return resp.StatusCode >= 500, fmt.Errorf("kakao API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	logf("[kakao] Successfully sent to %s (%s)", contact.Name, maskPhone(contact.Phone))
	return false, nil
}

// --- NHN Cloud SMS Sending ---

// sendSMSOnce performs a single SMS send attempt and classifies the failure for
// the retry driver exactly like sendKakaoTalkOnce (§출력 13).
func sendSMSOnce(cfg Config, contact Contact, alert AlertPayload, cctvLink string) (bool, error) {
	if cfg.NHNAppKey == "" || cfg.NHNSecretKey == "" {
		logf("[sms] NHN Cloud SMS not configured, skipping SMS for %s (%s)", contact.Name, maskPhone(contact.Phone))
		return false, fmt.Errorf("NHN Cloud SMS not configured") // permanent → no retry
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
		return false, fmt.Errorf("marshal SMS request: %w", err)
	}

	url := fmt.Sprintf("https://api-sms.cloud.toast.com/sms/v3.0/appKeys/%s/sender/sms", cfg.NHNAppKey)
	req, err := http.NewRequest("POST", url, bytes.NewReader(payload))
	if err != nil {
		return false, fmt.Errorf("create SMS request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	req.Header.Set("X-Secret-Key", cfg.NHNSecretKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		// The request URL embeds NHN_SMS_APP_KEY; a transport error (*url.Error)
		// carries the full URL, so scrub it here so the credential cannot leak
		// through any downstream consumer of this error (logs, system-alarm
		// payload, dispatch result). Retry fast failures, not timeouts (§출력 13).
		return !isTimeoutErr(err), fmt.Errorf("SMS API call: %s", scrub(err.Error()))
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return resp.StatusCode >= 500, fmt.Errorf("SMS API error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	logf("[sms] Successfully sent to %s (%s)", contact.Name, maskPhone(contact.Phone))
	return false, nil
}

// --- Crisis Email ---

func sendCrisisEmail(cfg Config, contact Contact, alert AlertPayload, cctvLink string) error {
	if cfg.SMTPHost == "" || cfg.SMTPFrom == "" {
		logf("[email] SMTP not configured, skipping email for %s (%s)", contact.Name, maskEmail(contact.Email))
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
		return fmt.Errorf("crisis email to %s: %w", maskEmail(contact.Email), err)
	}

	logf("[email] Crisis alert sent to %s (%s)", contact.Name, maskEmail(contact.Email))
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

	logf("[alarm] System alarm sent for contact %s (%s) — both KakaoTalk and SMS failed", contact.Name, maskPhone(contact.Phone))
}

// sendTargetUnavailableAlarm emits a `target_unavailable` system alarm when a valid
// crisis event was accepted but no delivery target could be established — i.e. the
// contact list is empty or the contact fetch failed (§출력 11 / assertions M, J).
// This promotes the "nobody knows about this crisis" state into an observable alarm
// instead of letting the event vanish in a single log line. The attempt result
// (2xx / non-2xx / call failure) is always logged; at least one attempt is made.
func sendTargetUnavailableAlarm(cfg Config, alert AlertPayload, reason string) {
	alarm := AlarmPayload{
		Type:    "target_unavailable",
		Message: fmt.Sprintf("No delivery target for crisis alert (site=%s device=%s): %s", alert.SiteID, alert.DeviceID, reason),
		Details: map[string]interface{}{
			"siteId":    alert.SiteID,
			"deviceId":  alert.DeviceID,
			"type":      alert.Type,
			"reason":    scrub(reason),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		},
	}

	payload, err := json.Marshal(alarm)
	if err != nil {
		logf("[alarm] target_unavailable: failed to marshal payload: %v", err)
		return
	}

	resp, err := httpClient.Post(cfg.WebBackendURL+"/internal/alarms", "application/json", bytes.NewReader(payload))
	if err != nil {
		logf("[alarm] target_unavailable system alarm failed (site=%s device=%s): %v", alert.SiteID, alert.DeviceID, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		logf("[alarm] target_unavailable system alarm failed: status %d, body: %s", resp.StatusCode, string(respBody))
		return
	}

	logf("[alarm] target_unavailable system alarm sent (site=%s device=%s): %s", alert.SiteID, alert.DeviceID, reason)
}

// --- Recording Archive (Two-Phase) ---

// cameraInfo is the notifier's view of a web-backend camera record. Beyond
// {streamKey,enabled} it decodes the per-camera `siteId` so protection can be
// scoped to the event's site (§출력 6 / assertion I). The siteId field contract is
// owned by interface-web-api; absent/empty siteId simply never matches a real event
// site and is left unprotected (safe default — no cross-site over-protection).
type cameraInfo struct {
	StreamKey string `json:"streamKey"`
	Enabled   bool   `json:"enabled"`
	SiteID    string `json:"siteId"`
}

// filterProtectedStreamKeys returns the streamKeys eligible for protection for a
// given event site (§출력 6 / assertion I). It is site-aware ONLY when the camera
// list actually carries site information:
//
//   - If at least one returned camera exposes a non-empty siteId, the list is
//     treated as site-tagged and protection is scoped to the event's site: enabled,
//     non-empty key, and siteId == event site. Other sites' cameras are excluded so
//     a site-A incident never protects site-B segments (multi-site contamination
//     guard). This is the target behavior once the camera contract exposes siteId.
//
//   - If NO camera exposes a siteId (the current single-deployment camera contract —
//     interface-web-api §계약13 returns no per-camera siteId, and web-backend has no
//     camera↔site association), site-scoping is undecidable, so we FALL BACK to
//     protecting all enabled cameras (a safe superset = the prior behavior). This
//     prevents silently protecting ZERO cameras and losing archive protection
//     entirely — a non-regression guarantee for the safety feature.
func filterProtectedStreamKeys(cameras []cameraInfo, siteID string) []string {
	siteTagged := false
	for _, c := range cameras {
		if c.SiteID != "" {
			siteTagged = true
			break
		}
	}

	var streamKeys []string
	for _, c := range cameras {
		if !c.Enabled || c.StreamKey == "" {
			continue
		}
		// Site-scope only when the list is site-tagged; otherwise fall back to all
		// enabled cameras so protection is never silently lost.
		if siteTagged && c.SiteID != siteID {
			continue
		}
		streamKeys = append(streamKeys, c.StreamKey)
	}
	return streamKeys
}

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

	var cameras []cameraInfo
	if err := json.NewDecoder(resp.Body).Decode(&cameras); err != nil {
		logf("[archive] Failed to decode cameras: %v", err)
		return
	}

	// Scope protection to the event's site only (§출력 6 / assertion I): other
	// sites' segments must never be dragged into this incident's protection.
	streamKeys := filterProtectedStreamKeys(cameras, alert.SiteID)

	if len(streamKeys) == 0 {
		logf("[archive] No enabled cameras for site %s, skipping protect", alert.SiteID)
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

	// Emit the protect-request record SYNCHRONOUSLY here — the caller invokes
	// requestArchiveProtect before it launches channel dispatch, so this log's
	// timestamp deterministically precedes the first channel-attempt completion AND
	// the dispatch-complete summary (§출력 6 / assertion P). Ordering is guaranteed by
	// structure, not by a goroutine scheduling race, so it holds even when channels
	// are unconfigured and dispatch finishes almost instantly.
	logf("[archive] Protect request accepted for incident %s (%d cameras)", incidentID, len(streamKeys))

	// Deliver the actual protect request to recording CONCURRENTLY (best-effort):
	// this is fire-and-forget and is never joined by the caller, so it cannot gate or
	// delay dispatch, and recording being down only produces a log line — the alert
	// outcome is unaffected. The request carries a BOUNDED timeout (context) so a
	// hung/blackholed recording endpoint always returns and cannot accumulate
	// goroutines; either the delivered happy-path OR the FAILED sad-path is logged so
	// the two-phase log is never left silent after the synchronous "accepted".
	timeout := cfg.ArchiveProtectTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, "POST", cfg.RecordingURL+"/api/archives/protect", bytes.NewReader(payload))
		if err != nil {
			logf("[archive] Protect request FAILED (incident %s): %v", incidentID, err)
			return
		}
		req.Header.Set("Content-Type", "application/json")

		protectResp, err := httpClient.Do(req)
		if err != nil {
			// Transport error OR context timeout (bounded above) — always logged.
			logf("[archive] Protect request FAILED (incident %s): %v", incidentID, err)
			return
		}
		defer protectResp.Body.Close()

		if protectResp.StatusCode >= 200 && protectResp.StatusCode < 300 {
			logf("[archive] Protect request delivered for incident %s (%d cameras)", incidentID, len(streamKeys))
		} else {
			body, _ := io.ReadAll(protectResp.Body)
			logf("[archive] Protect request FAILED (incident %s): status %d, body: %s", incidentID, protectResp.StatusCode, string(body))
		}
	}()
}

// --- Notification Dispatch ---

// maxConcurrentSends caps how many notification sends run at once (#62),
// bounding both goroutine count and concurrent outbound API connections.
const maxConcurrentSends = 16

// runBounded executes jobs through a fixed worker pool of at most `limit`
// goroutines and blocks until every job has finished. This bounds concurrency
// so a large contact list cannot explode goroutines/outbound connections. Each
// job runs under recoverGoroutine (#58) so one panicking send neither crashes
// the process nor kills its pool worker (which would starve the remaining jobs).
func runBounded(jobs []func(), limit int) {
	if len(jobs) == 0 {
		return
	}
	if limit < 1 {
		limit = 1
	}
	if limit > len(jobs) {
		limit = len(jobs)
	}
	ch := make(chan func())
	var wg sync.WaitGroup
	wg.Add(limit)
	for w := 0; w < limit; w++ {
		go func() {
			defer wg.Done()
			for job := range ch {
				func() {
					defer recoverGoroutine("notification send")
					job()
				}()
			}
		}()
	}
	for _, job := range jobs {
		ch <- job
	}
	close(ch)
	wg.Wait()
}

func dispatchNotifications(cfg Config, alert AlertPayload) (int, []NotifyResult) {
	// 1. Fetch contacts from web-backend
	contacts, err := fetchContacts(cfg)
	if err != nil {
		// §출력 11 / M: a fetch failure must not vanish in one log line — promote it
		// to a target_unavailable system alarm (≥1 attempt, result logged). No
		// external channel send happens on this path.
		logf("[notify] Failed to fetch contacts: %v — emitting target_unavailable system alarm", err)
		sendTargetUnavailableAlarm(cfg, alert, "contact fetch failed: "+scrub(err.Error()))
		return 0, nil
	}

	if len(contacts) == 0 {
		// §출력 11 / J,M: zero contacts → 200 accepted, ZERO external-channel sends,
		// but at least one target_unavailable system-alarm attempt is logged.
		logf("[notify] No contacts configured — emitting target_unavailable system alarm (no external channel send)")
		sendTargetUnavailableAlarm(cfg, alert, "no emergency contacts configured")
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

	results := make([]NotifyResult, len(contacts))

	// Build the per-contact send jobs, then run them through a bounded worker
	// pool (#62). fetchContacts is unbounded and each contact spawns up to two
	// sends (email + KakaoTalk→SMS→alarm chain); a large contact list — or many
	// concurrent /api/notify requests — would explode goroutines and outbound
	// connections, exhausting resources and tripping external-API rate limits.
	// The pool caps how many sends run at once while still driving every contact.
	var jobs []func()
	for i, contact := range contacts {
		i, contact := i, contact
		// Send email in parallel (independent channel, does not affect KakaoTalk/SMS)
		if contact.NotifyEmail && contact.Email != "" {
			jobs = append(jobs, func() {
				c := contact
				if err := sendCrisisEmail(cfg, c, alert, cctvLink); err != nil {
					logf("[notify] Email FAILED for %s (%s): %v", c.Name, maskEmail(c.Email), err)
				} else {
					logf("[notify] Email SUCCESS for %s (%s)", c.Name, maskEmail(c.Email))
				}
			})
		}

		// KakaoTalk → SMS → System alarm fallback chain
		jobs = append(jobs, func() {
			idx, c := i, contact
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
				logf("[notify] KakaoTalk skipped for %s (%s) — channel disabled, proceeding to SMS/fallback", c.Name, maskPhone(c.Phone))
			} else {
				kakaoErr = sendChannelWithRetry("kakao", c.Name, c.Phone, cfg.ChannelRetryMax, cfg.ChannelRetryBackoff,
					func() (bool, error) { return sendKakaoTalkOnce(cfg, c, alert, cctvLink) })
				if kakaoErr == nil {
					result.Channel = "kakaotalk"
					result.Success = true
					logf("[notify] KakaoTalk SUCCESS for %s (%s)", c.Name, maskPhone(c.Phone))
					results[idx] = result
					return
				}
				logf("[notify] KakaoTalk FAILED for %s (%s): %v — falling back to SMS", c.Name, maskPhone(c.Phone), kakaoErr)
			}

			// Step 2: Fallback to SMS (if enabled)
			var smsErr error
			if !smsEnabled {
				// Disabled channel: never call sendSMS (no external send attempt is
				// logged); record the skip and proceed to the system-alarm fallback.
				smsErr = fmt.Errorf("SMS disabled (SMS_ENABLED=false)")
				logf("[notify] SMS skipped for %s (%s) — channel disabled, proceeding to system alarm", c.Name, maskPhone(c.Phone))
			} else {
				smsErr = sendChannelWithRetry("sms", c.Name, c.Phone, cfg.ChannelRetryMax, cfg.ChannelRetryBackoff,
					func() (bool, error) { return sendSMSOnce(cfg, c, alert, cctvLink) })
				if smsErr == nil {
					result.Channel = "sms"
					result.Success = true
					logf("[notify] SMS SUCCESS for %s (%s)", c.Name, maskPhone(c.Phone))
					results[idx] = result
					return
				}
				logf("[notify] SMS FAILED for %s (%s): %v — sending system alarm", c.Name, maskPhone(c.Phone), smsErr)
			}

			// Step 3: Both channels unavailable or failed — send system alarm to web-backend
			sendSystemAlarm(cfg, c, alert, kakaoErr, smsErr)
			result.Channel = "alarm"
			result.Success = false
			result.Error = scrub(fmt.Sprintf("kakao: %s; sms: %s", kakaoErr.Error(), smsErr.Error()))
			results[idx] = result
		})
	}

	runBounded(jobs, maxConcurrentSends)

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

func handleNotify(cfg Config, dedup *dedupCache) http.HandlerFunc {
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

		// §출력 12 / assertion N: dedup. test:true events are excluded entirely (never
		// judged, never recorded), so a test injection cannot poison the cache and
		// swallow the real crisis that follows. A duplicate inside the window is still
		// accepted (200) but its whole dispatch — contact sends, system alarm AND the
		// §출력 6 archive-protect — is suppressed; only the first event proceeds.
		if !alert.Test && dedup.checkAndRecord(dedupKey(alert), cfg.DedupWindow, time.Now()) {
			logf("[dedup] Suppressed duplicate crisis event (site=%s device=%s type=%s) within %s window — no dispatch, no protect",
				alert.SiteID, alert.DeviceID, alert.Type, cfg.DedupWindow)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":     "accepted",
				"suppressed": true,
			})
			return
		}

		// Dispatch notifications asynchronously. Track the goroutine in
		// inflightDispatch so graceful shutdown (#40) can drain crisis-alert
		// dispatches already in progress instead of losing them on SIGTERM.
		inflightDispatch.Add(1)
		go func() {
			defer inflightDispatch.Done()
			defer recoverGoroutine("notify handler dispatch")

			// §출력 6 / assertion P: trigger archive protection FIRST. requestArchiveProtect
			// prepares the request and emits the protect-request log SYNCHRONOUSLY here,
			// then delivers the actual HTTP to recording concurrently in the background.
			// Because the protect log is emitted before channel dispatch even begins, its
			// timestamp deterministically precedes the first channel-attempt completion and
			// the dispatch-complete summary — ordering by structure, not by a goroutine race.
			requestArchiveProtect(cfg, alert)

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
		}()

		// Return immediately (accepted, processing async). No contactCount here: the
		// response is sent before contacts are fetched, so any value would be
		// structurally 0 and misleading — the field is dropped rather than lie.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "accepted",
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

// smtpSendBudget bounds the ENTIRE SMTP exchange (dial + every protocol step) for a
// single email so a configured-but-unresponsive host (accepts TCP, never replies)
// terminates as an error within the per-channel budget (≤12s, §출력 9) instead of
// blocking past the web-backend proxy hop (15s) and being mis-reported as a false
// `502 upstream_unavailable`. Kept comfortably under 12s. net/smtp.SendMail sets no
// deadline at all, so a blackholed host would hang indefinitely — this replaces it
// with an explicit-deadline exchange that never leaks a goroutine.
const smtpSendBudget = 11 * time.Second

func sendEmail(cfg Config, to, subject, body string) error {
	addr := fmt.Sprintf("%s:%s", cfg.SMTPHost, cfg.SMTPPort)

	// Defend SMTP header injection centrally for every email path.
	to = sanitizeHeader(to)
	subject = sanitizeHeader(subject)

	mime := "MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=\"UTF-8\"\r\n"
	msg := []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n%s\r\n%s",
		cfg.SMTPFrom, to, subject, mime, body))

	// Bound dial and the whole exchange under one wall-clock budget. Measuring from
	// `start` before the dial and setting the same absolute deadline on the conn
	// means dial + HELO + STARTTLS + AUTH + MAIL/RCPT/DATA together finish by
	// start+budget — no step can hang past the per-channel budget.
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, smtpSendBudget)
	if err != nil {
		return fmt.Errorf("smtp dial: %s", scrub(err.Error()))
	}
	_ = conn.SetDeadline(start.Add(smtpSendBudget))

	client, err := smtp.NewClient(conn, cfg.SMTPHost)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp connect: %s", scrub(err.Error()))
	}
	defer client.Close() // closes the underlying conn on every return path

	if err := client.Hello("localhost"); err != nil {
		return fmt.Errorf("smtp helo: %s", scrub(err.Error()))
	}
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: cfg.SMTPHost}); err != nil {
			return fmt.Errorf("smtp starttls: %s", scrub(err.Error()))
		}
	}
	if cfg.SMTPUser != "" {
		if ok, _ := client.Extension("AUTH"); ok {
			auth := smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPHost)
			if err := client.Auth(auth); err != nil {
				return fmt.Errorf("smtp auth: %s", scrub(err.Error()))
			}
		}
	}
	if err := client.Mail(cfg.SMTPFrom); err != nil {
		return fmt.Errorf("smtp mail: %s", scrub(err.Error()))
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt: %s", scrub(err.Error()))
	}
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %s", scrub(err.Error()))
	}
	if _, err := wc.Write(msg); err != nil {
		return fmt.Errorf("smtp write: %s", scrub(err.Error()))
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp write close: %s", scrub(err.Error()))
	}
	_ = client.Quit()
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

// --- Channel Test-Send (docs/spec/notification-test-send.md) ---
//
// Two internal endpoints back the admin test-send surface proxied by web-backend:
//   GET  /internal/channel-status → {email:{usable,reason}, sms:{usable,reason}}
//   POST /internal/test-send {channel,target} → {outcome} (sent|failed|not_configured)
//
// The usability rule (§출력 2) is the SINGLE judgment shared by status AND send, so
// the two can never disagree: email is usable when SMTP_HOST and SMTP_FROM are both
// present; SMS is usable when SMS_ENABLED=="true" AND its credentials exist (ENABLED
// off ⇒ usable=false even with creds). A configured channel that then fails to send
// reports `failed` (not not_configured); an unconfigured channel is never attempted
// and reports not_configured (§출력 3·4). The single specified target is tried once —
// the crisis fallback chain and contact fan-out are bypassed (§출력 5·6).

type channelUsability struct {
	Usable bool   `json:"usable"`
	Reason string `json:"reason"`
}

// emailChannelUsable reports whether the email channel can attempt a send (§출력 2).
func emailChannelUsable(cfg Config) bool {
	return cfg.SMTPHost != "" && cfg.SMTPFrom != ""
}

// smsChannelUsable reports whether the SMS channel can attempt a send (§출력 2):
// SMS_ENABLED=="true" AND credentials present. ENABLED off ⇒ false even with creds.
func smsChannelUsable(cfg Config) bool {
	return os.Getenv("SMS_ENABLED") == "true" && cfg.NHNAppKey != "" && cfg.NHNSecretKey != ""
}

func usabilityFor(usable bool) channelUsability {
	if usable {
		return channelUsability{Usable: true, Reason: ""}
	}
	return channelUsability{Usable: false, Reason: "not_configured"}
}

// handleChannelStatus serves GET /internal/channel-status. It reports each in-scope
// channel's current runtime usability from THIS process's live env config, so a
// notifier restart with new config is reflected on the next request without a
// web-backend restart (§출력 7). KakaoTalk is not in the in-scope set (§출력 12).
func handleChannelStatus(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isInternalIP(r.RemoteAddr) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "access denied"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]channelUsability{
			"email": usabilityFor(emailChannelUsable(cfg)),
			"sms":   usabilityFor(smsChannelUsable(cfg)),
		})
	}
}

// handleTestSend serves POST /internal/test-send {channel,target}. It attempts a
// single synchronous send on exactly one channel to exactly the given target,
// bypassing the crisis fallback chain and contact fan-out, and returns the closed
// outcome set {sent, failed, not_configured} (§출력 3·5·6). Unconfigured channels
// are never attempted (not_configured); a configured channel that errors is
// `failed` (§출력 4). Uses the operational credentials/transport (sendEmail /
// sendSMSOnce), not a mock path.
func handleTestSend(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isInternalIP(r.RemoteAddr) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "access denied"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var req struct {
			Channel string `json:"channel"`
			Target  string `json:"target"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid JSON payload"}`, http.StatusBadRequest)
			return
		}
		req.Channel = strings.TrimSpace(req.Channel)
		req.Target = strings.TrimSpace(req.Target)

		writeOutcome := func(outcome, reason string) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			out := map[string]string{"outcome": outcome}
			if reason != "" {
				out["reason"] = reason
			}
			json.NewEncoder(w).Encode(out)
		}

		switch req.Channel {
		case "email":
			if !emailChannelUsable(cfg) {
				writeOutcome("not_configured", "")
				return
			}
			// Single test email via the operational transport (fan-out bypassed).
			subject := "[Sentinel] 테스트 이메일"
			body := `<html><body><p>Sentinel 알림 채널 테스트 이메일입니다.</p></body></html>`
			if err := sendEmail(cfg, req.Target, subject, sanitizeHTML(body)); err != nil {
				logf("[test-send] email FAILED to %s: %v", maskEmail(req.Target), err)
				writeOutcome("failed", scrub(err.Error()))
				return
			}
			logf("[test-send] email sent to %s", maskEmail(req.Target))
			writeOutcome("sent", "")
		case "sms":
			if !smsChannelUsable(cfg) {
				writeOutcome("not_configured", "")
				return
			}
			// Single test SMS via the operational transport (fallback chain bypassed).
			contact := Contact{Phone: req.Target, Name: "test"}
			alert := AlertPayload{Description: "Sentinel 알림 채널 테스트", Type: "test", SiteID: "-", DeviceID: "-", Severity: "info", Timestamp: time.Now().UTC().Format(time.RFC3339), Test: true}
			if _, err := sendSMSOnce(cfg, contact, alert, ""); err != nil {
				logf("[test-send] sms FAILED to %s: %v", maskPhone(req.Target), err)
				writeOutcome("failed", scrub(err.Error()))
				return
			}
			logf("[test-send] sms sent to %s", maskPhone(req.Target))
			writeOutcome("sent", "")
		default:
			http.Error(w, `{"error":"unsupported channel"}`, http.StatusBadRequest)
		}
	}
}

// --- Main ---

func main() {
	cfg := loadConfig()
	initSecretScrubber(cfg)
	dedup := newDedupCache()

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","service":"notifier"}`))
	})

	mux.HandleFunc("POST /api/notify", handleNotify(cfg, dedup))
	mux.HandleFunc("POST /api/send-email", handleSendEmail(cfg))

	// Channel test-send (admin surface, proxied by web-backend over the internal
	// Docker network) — runtime channel status + single synchronous test send.
	mux.HandleFunc("GET /internal/channel-status", handleChannelStatus(cfg))
	mux.HandleFunc("POST /internal/test-send", handleTestSend(cfg))

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

	logf("notifier listening on :8080 (kakao configured: %v, sms configured: %v, smtp configured: %v, frontend: %s, dedup: %s, retryMax: %d)",
		cfg.KakaoAPIURL != "", cfg.NHNAppKey != "", cfg.SMTPHost != "", cfg.FrontendURL, cfg.DedupWindow, cfg.ChannelRetryMax)
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
