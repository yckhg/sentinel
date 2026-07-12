package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	_ "modernc.org/sqlite"
)

// -----------------------------------------------------------------------------
// TDD GATES for docs/spec/notification-test-send.md — assertions A–N.
//
// Surface under test (contract per §API 계약 델타 — SSOT interface-web-api.md):
//   GET  /api/notifications/channels        → {email:{usable,reason}, sms:{usable,reason}}
//   POST /api/notifications/test {channel,target} → {outcome}  (sent|failed|not_configured)
// Both are admin-only (same권한 as contacts CUD). channel ∈ {email, sms}.
// Processing order: input/channel validation (400) BEFORE rate-limit; 400 does
// NOT consume a token. (channel,target) rate limit 1/min → 429. notifier
// unreachable → 502 (+upstream_unavailable), never downgraded to not_configured.
//
// ── COMPILE SEAM (RED now, no production code committed) ──────────────────────
// The production handlers do not exist yet, so this file must not reference them
// by name (it would break the build). Instead the tests drive the two package
// vars below. They default to `redNotifStub` (HTTP 501) so every behavioural
// gate is RED via a *failed assertion*, not a build break.
//
//   IMPLEMENTER: after adding the real production handlers (e.g.
//   handleNotificationChannels / handleNotificationTest), wire them here so the
//   gates judge real code — replace the two initialisers:
//        var notifChannelsHandlerFn = handleNotificationChannels
//        var notifTestSendHandlerFn = handleNotificationTest
//   (These handlers proxy to the notifier; the fake notifier below stands in for
//   it so the gates run GREEN without a live stack once wired.)
// -----------------------------------------------------------------------------

var (
	notifChannelsHandlerFn = handleNotificationChannels
	notifTestSendHandlerFn = handleNotificationTest
)

func redNotifStub(_ *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotImplemented,
			map[string]string{"error": "notification-test-send not implemented (RED gate — wire the seam)"})
	}
}

// --- Fake notifier (stands in for the downstream notifier the handler proxies
// to). Counts POST send attempts (§발송 시도 관측점) and can be driven to report
// channel usability / outcome. Tolerant of the exact internal path names, which
// the notifier SSOT owns. GET → channel-status; POST → a send attempt. ---------

type fakeNotifier struct {
	srv         *httptest.Server
	sendCount   int64 // POST test-send / send-email attempts observed
	emailUsable bool
	smsUsable   bool
	outcome     string // "sent" | "failed" | "not_configured"
	mu          sync.Mutex
	targets     []string
}

func reasonFor(usable bool) string {
	if usable {
		return ""
	}
	return "not_configured"
}

func newFakeNotifier(t *testing.T) *fakeNotifier {
	t.Helper()
	fn := &fakeNotifier{outcome: "not_configured"}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{
				"email": map[string]any{"usable": fn.emailUsable, "reason": reasonFor(fn.emailUsable)},
				"sms":   map[string]any{"usable": fn.smsUsable, "reason": reasonFor(fn.smsUsable)},
			})
			return
		}
		atomic.AddInt64(&fn.sendCount, 1)
		body, _ := io.ReadAll(r.Body)
		fn.mu.Lock()
		fn.targets = append(fn.targets, string(body))
		fn.mu.Unlock()
		// Email path reuses notifier POST /api/send-email → 503 when SMTP未설정
		// (§출력 4: the handler maps that 503 to outcome not_configured).
		if strings.Contains(r.URL.Path, "send-email") && !fn.emailUsable {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "smtp not configured"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"outcome": fn.outcome, "reason": "fake"})
	})
	fn.srv = httptest.NewServer(mux)
	t.Cleanup(fn.srv.Close)
	return fn
}

func (fn *fakeNotifier) sends() int64 { return atomic.LoadInt64(&fn.sendCount) }

// withNotifierURL points the package-global notifierURL at u for the duration of
// the test and restores it afterward (tests run sequentially — no t.Parallel).
func withNotifierURL(t *testing.T, u string) {
	t.Helper()
	prev := notifierURL
	notifierURL = u
	t.Cleanup(func() { notifierURL = prev })
}

// notifTestServer mounts the two surfaces behind the real authMiddleware, exactly
// as production does, so 401 (no token) is enforced by the middleware and 403
// (non-admin) by the handler's admin gate.
func notifTestServer(t *testing.T, db *sql.DB) *httptest.Server {
	t.Helper()
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /api/notifications/channels", notifChannelsHandlerFn(db))
	apiMux.HandleFunc("POST /api/notifications/test", notifTestSendHandlerFn(db))
	root := http.NewServeMux()
	root.Handle("/api/", authMiddleware(db, apiMux))
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)
	return srv
}

func adminJWT(t *testing.T) string {
	t.Helper()
	tok, err := generateJWT(1, "admin")
	if err != nil {
		t.Fatalf("admin jwt: %v", err)
	}
	return tok
}

func userJWT(t *testing.T) string {
	t.Helper()
	tok, err := generateJWT(2, "user")
	if err != nil {
		t.Fatalf("user jwt: %v", err)
	}
	return tok
}

// req issues an HTTP request to the test server. token=="" → no Authorization.
func req(t *testing.T, srv *httptest.Server, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	r, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatalf("do request %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m) // best-effort; some bodies are arrays/empty
	return resp.StatusCode, m
}

// -----------------------------------------------------------------------------
// F — 관리자 전용 권한 (non-vacuous). 무인증 → 401, 비-admin(user) → 403; 발송 0.
// (401 is enforced by authMiddleware even now; 403 needs the handler admin gate,
// so this gate is RED until the real handler is wired.)
// -----------------------------------------------------------------------------
func TestNotif_F_AdminOnly(t *testing.T) {
	db := newTestDB(t)
	fn := newFakeNotifier(t)
	withNotifierURL(t, fn.srv.URL)
	fn.emailUsable, fn.smsUsable, fn.outcome = true, true, "sent"
	srv := notifTestServer(t, db)

	surfaces := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/notifications/channels", nil},
		{http.MethodPost, "/api/notifications/test", map[string]string{"channel": "email", "target": "f@example.com"}},
	}
	for _, s := range surfaces {
		if code, _ := req(t, srv, s.method, s.path, "", s.body); code != http.StatusUnauthorized {
			t.Errorf("%s %s no-token: want 401, got %d", s.method, s.path, code)
		}
		if code, _ := req(t, srv, s.method, s.path, userJWT(t), s.body); code != http.StatusForbidden {
			t.Errorf("%s %s user-token: want 403, got %d", s.method, s.path, code)
		}
	}
	if fn.sends() != 0 {
		t.Errorf("F: non-admin/무인증 requests must not trigger any send, got %d", fn.sends())
	}
}

// -----------------------------------------------------------------------------
// B — 미설정 이메일 테스트 → not_configured (무발송·무크래시·유한 응답). non-vacuous.
// -----------------------------------------------------------------------------
func TestNotif_B_UnconfiguredEmail_NotConfigured(t *testing.T) {
	db := newTestDB(t)
	fn := newFakeNotifier(t)
	withNotifierURL(t, fn.srv.URL)
	fn.emailUsable, fn.outcome = false, "not_configured" // SMTP未설정 default stack

	srv := notifTestServer(t, db)
	code, body := req(t, srv, http.MethodPost, "/api/notifications/test", adminJWT(t),
		map[string]string{"channel": "email", "target": "unconfigured@example.com"})

	if code != http.StatusOK {
		t.Fatalf("B: want 200 finite response, got %d (%v)", code, body)
	}
	if got, _ := body["outcome"].(string); got != "not_configured" {
		t.Errorf("B: want outcome=not_configured, got %q (body=%v)", got, body)
	}
}

// -----------------------------------------------------------------------------
// C — SMS 테스트: 미설정 방향 non-vacuous(→not_configured); 설정 방향 SKIP(no-fixture).
// -----------------------------------------------------------------------------
func TestNotif_C_UnconfiguredSMS_NotConfigured(t *testing.T) {
	db := newTestDB(t)
	fn := newFakeNotifier(t)
	withNotifierURL(t, fn.srv.URL)
	fn.smsUsable, fn.outcome = false, "not_configured"

	srv := notifTestServer(t, db)
	code, body := req(t, srv, http.MethodPost, "/api/notifications/test", adminJWT(t),
		map[string]string{"channel": "sms", "target": "010-1234-5678"})

	if code != http.StatusOK {
		t.Fatalf("C: want 200 finite response, got %d (%v)", code, body)
	}
	if got, _ := body["outcome"].(string); got != "not_configured" {
		t.Errorf("C: want outcome=not_configured, got %q (body=%v)", got, body)
	}
}

func TestNotif_C_ConfiguredSMS_Send_SKIP(t *testing.T) {
	// 설정된 SMS 채널로 발송 시도 1건 관측 → sent/failed. 실제 SMS 공급자 mock 픽스처가
	// 없으면 outcome=sent/failed 분기가 공허하다.
	t.Skip("SKIP(부적절, no-config/no-gateway): 설정 방향은 SMS mock 공급자 픽스처 필요")
}

// -----------------------------------------------------------------------------
// D — 요청 시점 notifier 조회 (web-backend 재시작 불요). 미설정→usable=false non-vacuous.
//     설정→true 및 notifier-restart 반영 방향은 config-flip 픽스처 필요 → SKIP.
// -----------------------------------------------------------------------------
func TestNotif_D_ChannelStatus_RequestTimeUnconfigured(t *testing.T) {
	db := newTestDB(t)
	fn := newFakeNotifier(t)
	withNotifierURL(t, fn.srv.URL)
	fn.emailUsable, fn.smsUsable = false, false // notifier config: 미설정

	srv := notifTestServer(t, db)
	code, body := req(t, srv, http.MethodGet, "/api/notifications/channels", adminJWT(t), nil)
	if code != http.StatusOK {
		t.Fatalf("D: want 200, got %d (%v)", code, body)
	}
	email, _ := body["email"].(map[string]any)
	if email == nil {
		t.Fatalf("D: response missing email channel (body=%v)", body)
	}
	if usable, _ := email["usable"].(bool); usable {
		t.Errorf("D: notifier未설정이면 email.usable=false 여야 함 (web-backend 자체판정 금지), got true")
	}
	// 미도달을 거짓 not_configured로 강등하지 않았음을 간접 확인: reason 존재.
	if _, ok := email["reason"]; !ok {
		t.Errorf("D: 미사용 채널은 reason을 동반해야 함 (body=%v)", body)
	}
}

func TestNotif_D_NotifierRestartReflection_SKIP(t *testing.T) {
	t.Skip("SKIP(부적절, no-fixture): 설정↔미설정 notifier config 전환 + notifier 재기동 픽스처 필요")
}

// -----------------------------------------------------------------------------
// G — 지원 채널 집합 = 정확히 {email, sms}; channel ∉ 집합(kakao) → 400, 발송 0,
//     레이트리밋보다 먼저 판정(프록시 도달 없음). non-vacuous.
// -----------------------------------------------------------------------------
func TestNotif_G_ChannelSet_KakaoRejected(t *testing.T) {
	db := newTestDB(t)
	fn := newFakeNotifier(t)
	withNotifierURL(t, fn.srv.URL)
	srv := notifTestServer(t, db)

	// status 채널 집합 = {email, sms}, KakaoTalk 없음.
	code, body := req(t, srv, http.MethodGet, "/api/notifications/channels", adminJWT(t), nil)
	if code != http.StatusOK {
		t.Fatalf("G: channels want 200, got %d (%v)", code, body)
	}
	if _, ok := body["email"]; !ok {
		t.Errorf("G: channels must expose email (body=%v)", body)
	}
	if _, ok := body["sms"]; !ok {
		t.Errorf("G: channels must expose sms (body=%v)", body)
	}
	for k := range body {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "kakao") {
			t.Errorf("G: channels must NOT expose KakaoTalk, found key %q", k)
		}
	}

	// channel=kakao 테스트 발송 → 400, 발송 시도 0건.
	before := fn.sends()
	code, _ = req(t, srv, http.MethodPost, "/api/notifications/test", adminJWT(t),
		map[string]string{"channel": "kakao", "target": "someone@example.com"})
	if code != http.StatusBadRequest {
		t.Errorf("G: channel=kakao want 400, got %d", code)
	}
	if fn.sends() != before {
		t.Errorf("G: rejected channel must not reach the send path (sends %d→%d)", before, fn.sends())
	}
}

// -----------------------------------------------------------------------------
// J — 입력 검증 (레이트리밋보다 선행, 400은 토큰 미소모). non-vacuous.
// -----------------------------------------------------------------------------
func TestNotif_J_InputValidation_BeforeRateLimit(t *testing.T) {
	db := newTestDB(t)
	fn := newFakeNotifier(t)
	withNotifierURL(t, fn.srv.URL)
	fn.emailUsable, fn.outcome = true, "sent"
	srv := notifTestServer(t, db)

	bad := []struct {
		name string
		body map[string]string
	}{
		{"email empty target", map[string]string{"channel": "email", "target": ""}},
		{"email malformed target", map[string]string{"channel": "email", "target": "not-an-email"}},
		{"sms empty target", map[string]string{"channel": "sms", "target": ""}},
		{"sms malformed target", map[string]string{"channel": "sms", "target": "12345"}},
	}
	before := fn.sends()
	for _, c := range bad {
		if code, _ := req(t, srv, http.MethodPost, "/api/notifications/test", adminJWT(t), c.body); code != http.StatusBadRequest {
			t.Errorf("J: %s want 400, got %d", c.name, code)
		}
	}
	if fn.sends() != before {
		t.Errorf("J: 400-rejected requests must not send (sends %d→%d)", before, fn.sends())
	}

	// 400은 토큰 미소모: 잘못된 입력 직후 같은 (channel,target)로 온 유효 요청은 429가 아니다.
	inv := map[string]string{"channel": "email", "target": "not-an-email"}
	_, _ = req(t, srv, http.MethodPost, "/api/notifications/test", adminJWT(t), inv)
	valid := map[string]string{"channel": "email", "target": "j-token-not-consumed@example.com"}
	if code, _ := req(t, srv, http.MethodPost, "/api/notifications/test", adminJWT(t), valid); code == http.StatusTooManyRequests {
		t.Errorf("J: 400 must not consume a rate-limit token — following valid request became 429")
	}
}

// -----------------------------------------------------------------------------
// K — status 채널 계약: email·sms 각각 usable(bool)+reason, 두 채널만. non-vacuous.
//     "ENABLED 꺼짐 + 자격증명 존재 → usable=false" 세부는 config 픽스처 필요 → SKIP.
// -----------------------------------------------------------------------------
func TestNotif_K_ChannelStatusContract(t *testing.T) {
	db := newTestDB(t)
	fn := newFakeNotifier(t)
	withNotifierURL(t, fn.srv.URL)
	fn.emailUsable, fn.smsUsable = false, true
	srv := notifTestServer(t, db)

	code, body := req(t, srv, http.MethodGet, "/api/notifications/channels", adminJWT(t), nil)
	if code != http.StatusOK {
		t.Fatalf("K: want 200, got %d (%v)", code, body)
	}
	for _, ch := range []string{"email", "sms"} {
		entry, ok := body[ch].(map[string]any)
		if !ok {
			t.Errorf("K: channel %q missing or not an object (body=%v)", ch, body)
			continue
		}
		if _, ok := entry["usable"].(bool); !ok {
			t.Errorf("K: channel %q must carry usable(bool) (entry=%v)", ch, entry)
		}
	}
	// exactly the two in-scope channels (G 정합) — no extra channel keys.
	allowed := map[string]bool{"email": true, "sms": true}
	for k := range body {
		if !allowed[k] {
			t.Errorf("K: unexpected channel key %q (only email/sms allowed)", k)
		}
	}
}

func TestNotif_K_UsableRule_EnabledOffWithCreds_SKIP(t *testing.T) {
	t.Skip("SKIP(부적절, no-config/no-gateway): SMS_ENABLED=false + 자격증명 존재 조합은 notifier config 픽스처 필요")
}

// -----------------------------------------------------------------------------
// M — 레이트리밋 (channel,target) 분당 1건. non-vacuous (리미터 리셋 전제).
// Uses distinct targets per sub-case so the (channel,target) buckets start empty
// (spec §레이트리밋 픽스처 전제: 서로 다른 target OR 리셋). 발송 0 관측: notifier
// sendCount는 429된 요청에 대해 증가하지 않는다.
// -----------------------------------------------------------------------------
func TestNotif_M_RateLimit_ChannelTargetScope(t *testing.T) {
	db := newTestDB(t)
	fn := newFakeNotifier(t)
	withNotifierURL(t, fn.srv.URL)
	fn.emailUsable, fn.outcome = true, "sent"
	srv := notifTestServer(t, db)

	targetA := map[string]string{"channel": "email", "target": "m-rl-a@example.com"}

	// 1st valid request → not 429, and it reaches the send path.
	if code, _ := req(t, srv, http.MethodPost, "/api/notifications/test", adminJWT(t), targetA); code == http.StatusTooManyRequests {
		t.Fatalf("M: 1st request must not be 429")
	}
	afterFirst := fn.sends()
	if afterFirst != 1 {
		t.Errorf("M: 1st valid request should reach the send path once, sends=%d", afterFirst)
	}

	// 2nd request same (channel,target) within the minute → 429, 발송 0 (sendCount 불변).
	if code, _ := req(t, srv, http.MethodPost, "/api/notifications/test", adminJWT(t), targetA); code != http.StatusTooManyRequests {
		t.Errorf("M: 2nd same-(channel,target) request within 1min want 429, got %d", code)
	}
	if fn.sends() != afterFirst {
		t.Errorf("M: 429-limited request must not send (sends %d→%d)", afterFirst, fn.sends())
	}

	// Different target → not limited (scope is (channel,target), not channel-global).
	targetB := map[string]string{"channel": "email", "target": "m-rl-b@example.com"}
	if code, _ := req(t, srv, http.MethodPost, "/api/notifications/test", adminJWT(t), targetB); code == http.StatusTooManyRequests {
		t.Errorf("M: different target must NOT be limited, got 429")
	}
}

// -----------------------------------------------------------------------------
// N — notifier 도달불가 → 유한시간 내 502(강등 없음). non-vacuous (in-process
// dead-address fixture: notifierURL을 닫힌 주소로 지정). test-send outcome을
// not_configured/sent로, status를 거짓 not_configured로 강등하지 않는다.
// -----------------------------------------------------------------------------
func TestNotif_N_NotifierUnreachable_502(t *testing.T) {
	db := newTestDB(t)
	// A closed loopback port makes the proxy hop fail (connection refused) —
	// simulates notifier 중단/재시작 창 without a live stack.
	withNotifierURL(t, "http://127.0.0.1:1")
	srv := notifTestServer(t, db)

	// status 읽기
	if code, body := req(t, srv, http.MethodGet, "/api/notifications/channels", adminJWT(t), nil); code != http.StatusBadGateway {
		t.Errorf("N(status): notifier 미도달 want 502, got %d (%v)", code, body)
	}
	// test-send: outcome을 만들지 않고 502로 종결, not_configured/sent 강등 금지.
	code, body := req(t, srv, http.MethodPost, "/api/notifications/test", adminJWT(t),
		map[string]string{"channel": "email", "target": "n@example.com"})
	if code != http.StatusBadGateway {
		t.Errorf("N(test-send): notifier 미도달 want 502, got %d (%v)", code, body)
	}
	if got, _ := body["outcome"].(string); got == "not_configured" || got == "sent" {
		t.Errorf("N: notifier 미도달을 outcome=%q로 강등하면 안 됨 (must be 502, no outcome)", got)
	}
}

// -----------------------------------------------------------------------------
// A·E·H·I·L — 설정된 채널의 실제 sent/failed/timeout 및 팬아웃-격리 분기는 실제/mock
// SMTP·SMS 공급자, 실패·지연 주입, 등록-연락처 픽스처가 있어야 non-vacuous하다.
// 기본 스택에서는 공허하므로 OK로 위장하지 않고 SKIP으로 선언한다 (§검증 스킵 선언).
// -----------------------------------------------------------------------------
func TestNotif_A_ConfiguredEmail_Sent_SKIP(t *testing.T) {
	t.Skip("SKIP(부적절, no-config/no-gateway): mock SMTP 픽스처 필요 (sent + 이메일 시도 1건)")
}
func TestNotif_E_ChannelIndependence_SKIP(t *testing.T) {
	t.Skip("SKIP(부적절, no-config/no-gateway): 이메일+SMS 설정 픽스처 필요 (교차 발동 없음 관측)")
}
func TestNotif_H_FailedReportedAsFailed_SKIP(t *testing.T) {
	t.Skip("SKIP(부적절, no-config/no-gateway): 실패 주입 공급자 픽스처 필요 (설정됨→failed)")
}
func TestNotif_I_FiniteDelay_SKIP(t *testing.T) {
	t.Skip("SKIP(부적절, no-config/no-gateway): 무응답(지연) 공급자 픽스처 필요 (≤12s 내 failed)")
}
func TestNotif_L_TargetIsolation_NoFanout_SKIP(t *testing.T) {
	t.Skip("SKIP(부적절, no-config/no-gateway): 설정 + 등록 연락처(N≥1) 픽스처 필요 (팬아웃 0)")
}
