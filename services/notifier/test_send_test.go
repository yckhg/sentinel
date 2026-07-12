package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Tests for the channel test-send internal endpoints (docs/spec/notification-test-send.md).
// Non-vacuous against the default (unconfigured) config: usable=false / not_configured.
// The configured→sent/failed direction needs a live/mock SMTP·SMS provider fixture
// and is exercised by web-backend's SKIP-authored gates (A/C-configured/H/I).

func newTestSendServer(t *testing.T, cfg Config) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/channel-status", handleChannelStatus(cfg))
	mux.HandleFunc("POST /internal/test-send", handleTestSend(cfg))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestChannelStatus_UnconfiguredShape(t *testing.T) {
	srv := newTestSendServer(t, Config{}) // no SMTP, no SMS creds
	resp, err := http.Get(srv.URL + "/internal/channel-status")
	if err != nil {
		t.Fatalf("get channel-status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("channel-status want 200, got %d", resp.StatusCode)
	}
	var body map[string]channelUsability
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Exactly the two in-scope channels, no KakaoTalk.
	if len(body) != 2 {
		t.Errorf("want exactly {email,sms}, got %d keys: %v", len(body), body)
	}
	for _, ch := range []string{"email", "sms"} {
		entry, ok := body[ch]
		if !ok {
			t.Errorf("missing channel %q", ch)
			continue
		}
		if entry.Usable {
			t.Errorf("channel %q should be usable=false when unconfigured", ch)
		}
		if entry.Reason == "" {
			t.Errorf("unusable channel %q must carry a reason", ch)
		}
	}
	if _, ok := body["kakao"]; ok {
		t.Errorf("channel-status must NOT expose kakao")
	}
}

func TestChannelStatus_UsableRule(t *testing.T) {
	// Email usable = SMTP_HOST + SMTP_FROM both present.
	cfg := Config{SMTPHost: "smtp.example.com", SMTPFrom: "noreply@example.com"}
	if !emailChannelUsable(cfg) {
		t.Errorf("email should be usable with host+from")
	}
	// Missing SMTP_FROM ⇒ not usable.
	if emailChannelUsable(Config{SMTPHost: "smtp.example.com"}) {
		t.Errorf("email must NOT be usable without SMTP_FROM")
	}
	// SMS_ENABLED off + creds present ⇒ usable=false (ENABLED gate, §출력 2).
	t.Setenv("SMS_ENABLED", "false")
	if smsChannelUsable(Config{NHNAppKey: "k", NHNSecretKey: "s"}) {
		t.Errorf("sms must NOT be usable when SMS_ENABLED=false even with creds")
	}
	// SMS_ENABLED on + creds ⇒ usable=true.
	t.Setenv("SMS_ENABLED", "true")
	if !smsChannelUsable(Config{NHNAppKey: "k", NHNSecretKey: "s"}) {
		t.Errorf("sms should be usable with SMS_ENABLED=true + creds")
	}
	// SMS_ENABLED on but no creds ⇒ usable=false.
	if smsChannelUsable(Config{}) {
		t.Errorf("sms must NOT be usable without creds")
	}
}

func postTestSend(t *testing.T, srv *httptest.Server, channel, target string) (int, map[string]string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"channel": channel, "target": target})
	resp, err := http.Post(srv.URL+"/internal/test-send", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("post test-send: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func TestTestSend_UnconfiguredEmail_NotConfigured(t *testing.T) {
	srv := newTestSendServer(t, Config{})
	code, body := postTestSend(t, srv, "email", "someone@example.com")
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
	if body["outcome"] != "not_configured" {
		t.Errorf("unconfigured email want not_configured, got %q", body["outcome"])
	}
}

func TestTestSend_UnconfiguredSMS_NotConfigured(t *testing.T) {
	t.Setenv("SMS_ENABLED", "true") // ENABLED on but no creds → still not_configured
	srv := newTestSendServer(t, Config{})
	code, body := postTestSend(t, srv, "sms", "010-1234-5678")
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
	if body["outcome"] != "not_configured" {
		t.Errorf("unconfigured sms want not_configured, got %q", body["outcome"])
	}
}

func TestTestSend_UnsupportedChannel_400(t *testing.T) {
	srv := newTestSendServer(t, Config{})
	code, _ := postTestSend(t, srv, "kakao", "someone@example.com")
	if code != http.StatusBadRequest {
		t.Errorf("unsupported channel want 400, got %d", code)
	}
}
