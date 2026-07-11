package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a goroutine-safe log sink used to observe log ordering while a
// background goroutine may also be writing.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// TestMaskSecret proves the masking transform actually redacts: the original
// plaintext must not survive, and a diagnostic marker ("***") must remain.
func TestMaskSecret(t *testing.T) {
	cases := []string{
		"SPECSMTP_deadbeef05",
		"SPECAPP_deadbeef03",
		"SPECFAKEsmtp_deadbeef01", // synthetic fixture — never a real credential
	}
	for _, s := range cases {
		got := maskSecret(s)
		if strings.Contains(got, s) {
			t.Errorf("maskSecret(%q) = %q — still contains the full plaintext", s, got)
		}
		if !strings.Contains(got, "***") {
			t.Errorf("maskSecret(%q) = %q — missing '***' diagnostic marker", s, got)
		}
		// Prefix (first 4 chars) is kept for diagnostics but the tail is gone.
		if !strings.HasPrefix(got, s[:4]) {
			t.Errorf("maskSecret(%q) = %q — expected 4-char prefix %q", s, got, s[:4])
		}
	}

	// Empty stays empty; very short values are fully redacted.
	if maskSecret("") != "" {
		t.Errorf("maskSecret(\"\") must be empty")
	}
	if got := maskSecret("ab"); strings.Contains(got, "ab") {
		t.Errorf("maskSecret(short) = %q — must not echo a short secret", got)
	}

	// Over-exposure cap (Fix G): the exposed prefix must be at most min(4, len/4),
	// i.e. never more than a quarter of a short secret's bytes. "exposed" is the
	// part of the output before the "***" marker.
	for _, s := range []string{"abcd", "abcde", "pass1234", "secret12", "0123456"} {
		got := maskSecret(s)
		if strings.Contains(got, s) {
			t.Errorf("maskSecret(%q)=%q leaks the full value", s, got)
		}
		if !strings.HasSuffix(got, "***") {
			t.Errorf("maskSecret(%q)=%q missing *** marker", s, got)
		}
		exposed := len(strings.TrimSuffix(got, "***"))
		capLen := len(s) / 4
		if capLen > 4 {
			capLen = 4
		}
		if exposed > capLen {
			t.Errorf("maskSecret(%q)=%q exposes %d chars, cap is %d", s, got, exposed, capLen)
		}
		// Never expose more than a quarter of the bytes.
		if exposed*4 > len(s) {
			t.Errorf("maskSecret(%q)=%q exposes %d/%d (>25%%)", s, got, exposed, len(s))
		}
	}
}

// TestSanitizeHTML locks the /api/send-email body-sanitize contract (spec §입력 2).
// Assertion G exercises this end-to-end but is MANUAL-SKIP (needs a real mailbox),
// so this unit test is the only executable proof of the XSS-sanitize surface.
func TestSanitizeHTML(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		mustLack []string
		mustHave []string
	}{
		{"script dropped whole", `<p>ok</p><script>alert(1)</script>`,
			[]string{"<script", "alert(1)"}, []string{"<p>", "ok"}},
		{"iframe dropped whole", `hi<iframe src="http://evil"></iframe>`,
			[]string{"<iframe"}, []string{"hi"}},
		{"on* handler removed", `<p onclick="steal()">t</p>`,
			[]string{"onclick", "steal()"}, []string{"<p>", "t"}},
		{"javascript URI removed", `<a href="javascript:alert(1)">x</a>`,
			[]string{"javascript:"}, []string{"x"}},
		{"disallowed tag shell removed, text kept", `<div class="x">inside</div>`,
			[]string{"<div"}, []string{"inside"}},
		{"allowed tags preserved", `<strong>b</strong><em>i</em><h1>t</h1><br>`,
			[]string{}, []string{"<strong>", "<em>", "<h1>", "<br"}},
		{"safe http href kept", `<a href="https://ok.example/x">l</a>`,
			[]string{}, []string{"https://ok.example/x"}},

		// --- Adversarial bypass vectors (Fix I): lock the known-blocked cases so a
		//     regression in the sanitizer's case-insensitivity / entity decoding /
		//     attribute parsing is caught. Each asserts no executable script/scheme.
		{"uppercase SCRIPT dropped", `<SCRIPT>alert(11)</SCRIPT>`,
			[]string{"alert(11)", "<SCRIPT", "<script"}, []string{}},
		{"mixed-case script dropped", `<ScRiPt>alert(12)</ScRiPt>`,
			[]string{"alert(12)"}, []string{}},
		{"unterminated script shell removed", `<p>a</p><script>alert(13)`,
			[]string{"<script"}, []string{"a"}},
		{"img onerror removed whole", `<img src=x onerror="alert(14)">z`,
			[]string{"onerror", "alert(14)", "<img"}, []string{"z"}},
		{"entity-encoded javascript scheme dropped", `<a href="&#106;avascript:alert(15)">e</a>`,
			[]string{"avascript:", "alert(15)"}, []string{"e"}},
		{"tab-obfuscated scheme dropped", "<a href=\"java\tscript:alert(16)\">f</a>",
			[]string{"script:alert(16)", "alert(16)"}, []string{"f"}},
		{"data URI dropped", `<a href="data:text/html;base64,PHN2Zz4=">g</a>`,
			[]string{"data:text/html", "base64"}, []string{"g"}},
		{"attr > breakout neutralized", `<img src="x" onerror="y>evil">bad`,
			[]string{"onerror", "evil", "<img"}, []string{"bad"}},
		{"nested script neutralized", `<scr<script>ipt>alert(18)</script>`,
			[]string{"alert(18)"}, []string{}},
		{"uppercase HREF javascript dropped", `<a HREF="JavaScript:alert(19)">h</a>`,
			[]string{"JavaScript:", "alert(19)"}, []string{"h"}},
	}
	for _, c := range cases {
		out := sanitizeHTML(c.in)
		for _, bad := range c.mustLack {
			if strings.Contains(out, bad) {
				t.Errorf("%s: sanitizeHTML(%q)=%q must not contain %q", c.name, c.in, out, bad)
			}
		}
		for _, good := range c.mustHave {
			if !strings.Contains(out, good) {
				t.Errorf("%s: sanitizeHTML(%q)=%q must contain %q", c.name, c.in, out, good)
			}
		}
	}
}

// TestIsInternalIP locks the /api/send-email source-IP gate (spec §입력 2:
// "사설/루프백 IP 대역에서 온 요청만 수용, 외부 IP는 403").
func TestIsInternalIP(t *testing.T) {
	internal := []string{
		"127.0.0.1:8080", "127.0.0.1", "::1",
		"10.1.2.3:9", "192.168.0.5:9",
		"172.16.5.5:9", "172.31.255.255:9",
		"fd00::1", // IPv6 unique-local (fc00::/7)
	}
	external := []string{
		"8.8.8.8:443", "1.1.1.1:443",
		"172.15.0.1:9",         // just below 172.16/12
		"172.32.0.1:9",         // just above 172.16/12
		"2001:4860:4860::8888", // public IPv6
		"notanip",              // unparseable → treated as external
	}
	for _, a := range internal {
		if !isInternalIP(a) {
			t.Errorf("isInternalIP(%q)=false, want true (internal)", a)
		}
	}
	for _, a := range external {
		if isInternalIP(a) {
			t.Errorf("isInternalIP(%q)=true, want false (external)", a)
		}
	}
}

// TestSanitizeHeader locks the SMTP header-injection defense (CRLF stripping) used
// on the To/Subject fields before they enter the email headers.
func TestSanitizeHeader(t *testing.T) {
	cases := []string{
		"victim@x.com\r\nBcc: attacker@evil.com",
		"Subject line\nInjected: header",
		"tab\tand\rcr",
	}
	for _, in := range cases {
		out := sanitizeHeader(in)
		if strings.ContainsAny(out, "\r\n") {
			t.Errorf("sanitizeHeader(%q)=%q still contains CR/LF", in, out)
		}
	}
	// A clean value passes through unchanged.
	if got := sanitizeHeader("plain@ok.com"); got != "plain@ok.com" {
		t.Errorf("sanitizeHeader clean value altered: %q", got)
	}
}

// TestScrub proves the central scrubber redacts ALL five credential classes,
// not only the one (NHN app key) that reaches a transport url.Error at runtime.
// Only the app key is embedded in a URL today; the API/Sender/Secret keys travel
// as request headers and SMTP_PASS is used post-connect, so they do not currently
// surface in a loggable string on the failure path. But if any of those ever gets
// logged (e.g. a future header dump or a differently-timed error), the central
// scrub must still catch it — this test locks that guarantee at the unit level.
func TestScrub(t *testing.T) {
	cfg := Config{
		KakaoAPIKey:    "SPECKKEY_deadbeef01",
		KakaoSenderKey: "SPECKSENDER_deadbeef02",
		NHNAppKey:      "SPECAPP_deadbeef03",
		NHNSecretKey:   "SPECSEC_deadbeef04",
		SMTPPass:       "SPECSMTP_deadbeef05",
	}
	initSecretScrubber(cfg)

	// Each class embedded in a plausible loggable assembly (URL, header dump, error).
	secrets := map[string]string{
		"KakaoAPIKey":    cfg.KakaoAPIKey,
		"KakaoSenderKey": cfg.KakaoSenderKey,
		"NHNAppKey":      cfg.NHNAppKey,
		"NHNSecretKey":   cfg.NHNSecretKey,
		"SMTPPass":       cfg.SMTPPass,
	}
	for name, val := range secrets {
		// Simulate the value leaking through several string-assembly shapes.
		for _, in := range []string{
			"kakao API call: header X-Api-Key=" + val + " X-Sender-Key set",
			`Post "https://api-sms.cloud.toast.com/sms/v3.0/appKeys/` + val + `/sender/sms": dial tcp: refused`,
			"smtp auth for user with pass " + val + " rejected",
		} {
			out := scrub(in)
			if strings.Contains(out, val) {
				t.Errorf("scrub left %s plaintext in %q", name, out)
			}
			if !strings.Contains(out, val[:4]+"***") {
				t.Errorf("scrub of %s did not emit masked form %q in %q", name, val[:4]+"***", out)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// #60 dedup (§출력 12 / assertion N)
// ---------------------------------------------------------------------------

// TestDedupCache locks the incident-dedup contract: first event passes, a second
// inside the window is suppressed, a window of 0 disables suppression, and an entry
// re-appears as "first" once its window has expired (eviction).
func TestDedupCache(t *testing.T) {
	base := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	key := "site1\x1fDEV\x1fgas"

	t.Run("first passes, second within window suppressed", func(t *testing.T) {
		d := newDedupCache()
		if d.checkAndRecord(key, 60*time.Second, base) {
			t.Fatal("first event must NOT be suppressed")
		}
		if !d.checkAndRecord(key, 60*time.Second, base.Add(30*time.Second)) {
			t.Fatal("second event within window MUST be suppressed")
		}
	})

	t.Run("window=0 disables dedup", func(t *testing.T) {
		d := newDedupCache()
		if d.checkAndRecord(key, 0, base) {
			t.Fatal("window=0: first must pass")
		}
		if d.checkAndRecord(key, 0, base) {
			t.Fatal("window=0: no suppression — second must pass too")
		}
	})

	t.Run("expired entry is evicted and treated as first again", func(t *testing.T) {
		d := newDedupCache()
		if d.checkAndRecord(key, 60*time.Second, base) {
			t.Fatal("first must pass")
		}
		// 61s later — outside the 60s window: must pass as a fresh "first".
		if d.checkAndRecord(key, 60*time.Second, base.Add(61*time.Second)) {
			t.Fatal("post-window event must be treated as first (not suppressed)")
		}
		if len(d.entries) != 1 {
			t.Fatalf("cache must stay bounded after eviction, got %d entries", len(d.entries))
		}
	})

	t.Run("distinct keys are independent", func(t *testing.T) {
		d := newDedupCache()
		if d.checkAndRecord("site1\x1fA\x1fgas", 60*time.Second, base) {
			t.Fatal("key A first must pass")
		}
		if d.checkAndRecord("site1\x1fB\x1fgas", 60*time.Second, base) {
			t.Fatal("key B (distinct) first must pass")
		}
	})
}

// TestDedupCacheAtomicity proves the check-and-record is atomic: when the SAME key
// is presented concurrently by many goroutines, exactly ONE is allowed through as
// "first" and every other is suppressed (no race-induced double pass).
func TestDedupCacheAtomicity(t *testing.T) {
	d := newDedupCache()
	const goroutines = 200
	now := time.Now()
	var allowed int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if !d.checkAndRecord("site1\x1fRACE\x1fgas", time.Minute, now) {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	if allowed != 1 {
		t.Fatalf("exactly one goroutine must pass as first, got %d", allowed)
	}
}

// TestDedupTestEventExclusion documents (at the key/handler-contract level) that
// test:true events are excluded from dedup — the handler skips checkAndRecord for
// them, so they neither get suppressed nor pollute the cache. Here we assert the
// cache itself never auto-excludes (exclusion is the caller's job) AND that a real
// event with the same key is unaffected by any prior test-event handling (since the
// test event was never recorded).
func TestDedupTestEventExclusion(t *testing.T) {
	d := newDedupCache()
	now := time.Now()
	key := dedupKey(AlertPayload{SiteID: "s", DeviceID: "d", Type: "gas", Test: true})
	// A test event is NEVER passed to checkAndRecord by the handler, so the cache
	// stays empty and the subsequent REAL event of the same key passes as first.
	realKey := dedupKey(AlertPayload{SiteID: "s", DeviceID: "d", Type: "gas"})
	if key != realKey {
		t.Fatalf("dedupKey must ignore the test flag: %q vs %q", key, realKey)
	}
	if d.checkAndRecord(realKey, time.Minute, now) {
		t.Fatal("real event after an (excluded) test event must pass as first")
	}
}

// ---------------------------------------------------------------------------
// #61 channel retry vs timeout (§출력 13 / assertion O)
// ---------------------------------------------------------------------------

type fakeNetErr struct{ timeout bool }

func (e fakeNetErr) Error() string   { return "fake net error" }
func (e fakeNetErr) Timeout() bool   { return e.timeout }
func (e fakeNetErr) Temporary() bool { return false }

// TestIsTimeoutErr locks the timeout classifier that decides retry-vs-immediate-fallback.
func TestIsTimeoutErr(t *testing.T) {
	if !isTimeoutErr(fakeNetErr{timeout: true}) {
		t.Error("net.Error with Timeout()=true must be a timeout")
	}
	if isTimeoutErr(fakeNetErr{timeout: false}) {
		t.Error("net.Error with Timeout()=false (e.g. conn refused) must NOT be a timeout")
	}
	if !isTimeoutErr(context.DeadlineExceeded) {
		t.Error("context.DeadlineExceeded must be a timeout")
	}
	if isTimeoutErr(errors.New("some other error")) {
		t.Error("a generic error must NOT be classified as a timeout")
	}
	if isTimeoutErr(nil) {
		t.Error("nil must not be a timeout")
	}
}

// TestSendChannelWithRetry locks the retry driver: fast transient (retryable) errors
// are retried up to max; timeouts/permanent (non-retryable) fall back immediately;
// max=0 means no retry; a mid-sequence success stops early.
func TestSendChannelWithRetry(t *testing.T) {
	cases := []struct {
		name        string
		max         int
		results     []struct {
			retryable bool
			err       error
		}
		wantCalls int
		wantErr   bool
	}{
		{
			name: "retryable error retried up to max then falls back",
			max:  1,
			results: []struct {
				retryable bool
				err       error
			}{
				{true, errors.New("5xx")}, {true, errors.New("5xx")},
			},
			wantCalls: 2, // initial + 1 retry
			wantErr:   true,
		},
		{
			name: "max=0 -> immediate fallback, no retry",
			max:  0,
			results: []struct {
				retryable bool
				err       error
			}{
				{true, errors.New("5xx")},
			},
			wantCalls: 1,
			wantErr:   true,
		},
		{
			name: "timeout (non-retryable) -> immediate fallback even with max>=1",
			max:  1,
			results: []struct {
				retryable bool
				err       error
			}{
				{false, errors.New("timeout")},
			},
			wantCalls: 1,
			wantErr:   true,
		},
		{
			name: "retry succeeds on second attempt",
			max:  1,
			results: []struct {
				retryable bool
				err       error
			}{
				{true, errors.New("5xx")}, {false, nil},
			},
			wantCalls: 2,
			wantErr:   false,
		},
		{
			name: "max=2 retryable retried twice (3 attempts)",
			max:  2,
			results: []struct {
				retryable bool
				err       error
			}{
				{true, errors.New("5xx")}, {true, errors.New("5xx")}, {true, errors.New("5xx")},
			},
			wantCalls: 3,
			wantErr:   true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls := 0
			err := sendChannelWithRetry("test", "n", "p", c.max, 0, func() (bool, error) {
				r := c.results[calls]
				calls++
				return r.retryable, r.err
			})
			if calls != c.wantCalls {
				t.Errorf("attempts = %d, want %d", calls, c.wantCalls)
			}
			if (err != nil) != c.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// #65 site-scoped camera filter (§출력 6 / assertion I)
// ---------------------------------------------------------------------------

// TestFilterProtectedStreamKeys proves protection is scoped to the event's site:
// only enabled, non-empty-key cameras of the SAME siteId are included; other sites'
// cameras are never protected.
func TestFilterProtectedStreamKeys(t *testing.T) {
	cameras := []cameraInfo{
		{StreamKey: "a1", Enabled: true, SiteID: "site-A"},
		{StreamKey: "a2", Enabled: true, SiteID: "site-A"},
		{StreamKey: "b1", Enabled: true, SiteID: "site-B"},
		{StreamKey: "a3", Enabled: false, SiteID: "site-A"}, // disabled → excluded
		{StreamKey: "", Enabled: true, SiteID: "site-A"},    // empty key → excluded
	}

	gotA := filterProtectedStreamKeys(cameras, "site-A")
	if strings.Join(gotA, ",") != "a1,a2" {
		t.Errorf("site-A protection = %v, want [a1 a2] (no site-B, no disabled, no empty)", gotA)
	}
	for _, k := range gotA {
		if k == "b1" {
			t.Fatal("site-A event must NOT protect a site-B camera (cross-site contamination)")
		}
	}

	gotB := filterProtectedStreamKeys(cameras, "site-B")
	if strings.Join(gotB, ",") != "b1" {
		t.Errorf("site-B protection = %v, want [b1]", gotB)
	}

	if got := filterProtectedStreamKeys(cameras, "site-C"); len(got) != 0 {
		t.Errorf("site with no cameras must protect nothing, got %v", got)
	}
}

// TestFilterProtectedStreamKeysFallback locks the non-regression fallback: under the
// CURRENT camera contract (no per-camera siteId — interface-web-api §계약13), every
// camera decodes with an empty siteId. Site-scoping is then undecidable, so the
// filter must fall back to protecting ALL enabled cameras (a safe superset = prior
// behavior) rather than silently protecting ZERO and losing archive protection.
func TestFilterProtectedStreamKeysFallback(t *testing.T) {
	// No camera carries a siteId (the real single-deployment contract today).
	cameras := []cameraInfo{
		{StreamKey: "c1", Enabled: true},
		{StreamKey: "c2", Enabled: true},
		{StreamKey: "c3", Enabled: false}, // disabled → still excluded
		{StreamKey: "", Enabled: true},    // empty key → still excluded
	}

	// Regardless of the event's siteId, all enabled non-empty-key cameras are
	// protected — the fallback must NOT be empty.
	for _, site := range []string{"site1", "anything", ""} {
		got := filterProtectedStreamKeys(cameras, site)
		if strings.Join(got, ",") != "c1,c2" {
			t.Errorf("fallback (no siteId) for event site %q = %v, want [c1 c2] (all enabled protected)", site, got)
		}
	}

	// Mixed: as soon as ANY camera is site-tagged, the list is treated as site-aware
	// and cameras WITHOUT a matching siteId (incl. untagged ones) are excluded.
	mixed := []cameraInfo{
		{StreamKey: "s1", Enabled: true, SiteID: "site1"},
		{StreamKey: "u1", Enabled: true}, // untagged — excluded once list is site-aware
	}
	if got := filterProtectedStreamKeys(mixed, "site1"); strings.Join(got, ",") != "s1" {
		t.Errorf("mixed list must be site-scoped = %v, want [s1] (untagged excluded)", got)
	}
}

// ---------------------------------------------------------------------------
// #64 parallel protect ordering (§출력 6 / assertion P)
// ---------------------------------------------------------------------------

// TestArchiveProtectLogIsSynchronous proves the protect-request log is emitted
// SYNCHRONOUSLY by requestArchiveProtect — before the recording delivery HTTP can
// complete — so it deterministically precedes channel dispatch/summary regardless of
// goroutine scheduling. We block the recording endpoint and assert the "Protect
// request accepted" log already exists the moment requestArchiveProtect returns,
// while the "delivered" outcome log does NOT yet exist (delivery still in flight).
func TestArchiveProtectLogIsSynchronous(t *testing.T) {
	recordingHit := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/internal/cameras":
			// No siteId → fallback protects all enabled cameras (non-regression path).
			_, _ = io.WriteString(w, `[{"streamKey":"cam1","enabled":true}]`)
		case strings.Contains(r.URL.Path, "/api/archives/protect"):
			once.Do(func() { close(recordingHit) })
			<-release // block delivery so it cannot complete before we assert
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	var buf syncBuffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	cfg := Config{WebBackendURL: srv.URL, RecordingURL: srv.URL}
	initSecretScrubber(cfg)

	requestArchiveProtect(cfg, AlertPayload{
		SiteID: "site1", DeviceID: "d", Type: "gas", Timestamp: "2026-07-11T00:00:00Z",
	})

	// requestArchiveProtect has returned; delivery is backgrounded and blocked on
	// `release`. The protect-accept record MUST already be present (synchronous).
	if !strings.Contains(buf.String(), "[archive] Protect request accepted for incident incident_site1_") {
		t.Fatalf("protect-accept log must be emitted synchronously; got:\n%s", buf.String())
	}
	// And it must carry the camera count (assertion I record shape).
	if !strings.Contains(buf.String(), "(1 cameras)") {
		t.Fatalf("protect record must include camera count; got:\n%s", buf.String())
	}
	// The delivery outcome must NOT be logged yet — proving the HTTP is not gated in
	// front of the accept log (it is still blocked).
	if strings.Contains(buf.String(), "Protect request delivered") {
		t.Fatalf("delivery outcome logged before release — accept log is not synchronous-first")
	}

	// Release the blocked delivery and confirm the background HTTP actually fired.
	close(release)
	select {
	case <-recordingHit:
	case <-time.After(2 * time.Second):
		t.Fatal("recording delivery endpoint was never hit")
	}
}

// waitForLog polls a syncBuffer until it contains want or the deadline elapses.
func waitForLog(t *testing.T, buf *syncBuffer, want string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("log %q not seen within %s; got:\n%s", want, d, buf.String())
}

// TestArchiveProtectDeliveryTimeoutLogged locks the MEDIUM robustness fix: when the
// recording endpoint hangs, the background protect delivery must return within its
// bounded timeout and emit a FAILED sad-path log — so a recording outage cannot
// silently leave only the happy-path "accepted" record (false sense of safety) nor
// leak an unbounded goroutine. The synchronous "accepted" record still comes first
// (P must not regress).
func TestArchiveProtectDeliveryTimeoutLogged(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/internal/cameras":
			_, _ = io.WriteString(w, `[{"streamKey":"cam1","enabled":true}]`)
		case strings.Contains(r.URL.Path, "/api/archives/protect"):
			<-release // hang the recording endpoint past the protect timeout
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()
	defer close(release)

	var buf syncBuffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	// Short bound so the test is fast; the real default is 5s.
	cfg := Config{WebBackendURL: srv.URL, RecordingURL: srv.URL, ArchiveProtectTimeout: 100 * time.Millisecond}
	initSecretScrubber(cfg)

	requestArchiveProtect(cfg, AlertPayload{
		SiteID: "site1", DeviceID: "d", Type: "gas", Timestamp: "2026-07-11T00:00:00Z",
	})

	// Accept record is synchronous and precedes any delivery outcome (P intact).
	if !strings.Contains(buf.String(), "[archive] Protect request accepted for incident incident_site1_") {
		t.Fatalf("accepted record must be present synchronously; got:\n%s", buf.String())
	}
	// The bounded delivery must fail (timeout) and log FAILED well inside the deadline.
	waitForLog(t, &buf, "[archive] Protect request FAILED (incident incident_site1_", 2*time.Second)
	if strings.Contains(buf.String(), "Protect request delivered") {
		t.Fatalf("delivery must not report success against a hung endpoint; got:\n%s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// #59 silent-fail: target_unavailable emission (§출력 11 / assertions M, J)
// ---------------------------------------------------------------------------

// TestDispatchTargetUnavailable proves that when the contact list is empty OR the
// contact fetch fails, dispatchNotifications still emits at least one
// target_unavailable system-alarm attempt and performs ZERO external-channel sends.
func TestDispatchTargetUnavailable(t *testing.T) {
	cases := []struct {
		name           string
		contactsStatus int
		contactsBody   string
	}{
		{"zero contacts", http.StatusOK, "[]"},
		{"contact fetch fails", http.StatusInternalServerError, "boom"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var mu sync.Mutex
			var alarms []AlarmPayload
			var kakaoHits, smsHits int

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/internal/contacts":
					w.WriteHeader(c.contactsStatus)
					_, _ = io.WriteString(w, c.contactsBody)
				case r.URL.Path == "/internal/alarms":
					b, _ := io.ReadAll(r.Body)
					var a AlarmPayload
					_ = json.Unmarshal(b, &a)
					mu.Lock()
					alarms = append(alarms, a)
					mu.Unlock()
					w.WriteHeader(http.StatusOK)
				case strings.Contains(r.URL.Path, "alimtalk"):
					mu.Lock()
					kakaoHits++
					mu.Unlock()
					w.WriteHeader(http.StatusOK)
				case strings.Contains(r.URL.Path, "sms"):
					mu.Lock()
					smsHits++
					mu.Unlock()
					w.WriteHeader(http.StatusOK)
				default:
					w.WriteHeader(http.StatusOK)
				}
			}))
			defer srv.Close()

			cfg := Config{WebBackendURL: srv.URL, RecordingURL: ""}
			initSecretScrubber(cfg)

			count, results := dispatchNotifications(cfg, AlertPayload{
				SiteID: "site1", DeviceID: "DEV-1", Type: "gas_leak", Timestamp: "2026-07-11T00:00:00Z",
			})

			if count != 0 || len(results) != 0 {
				t.Errorf("no-target path must return zero contacts/results, got count=%d results=%d", count, len(results))
			}
			mu.Lock()
			defer mu.Unlock()
			if len(alarms) < 1 {
				t.Fatalf("expected >=1 target_unavailable system-alarm attempt, got %d", len(alarms))
			}
			if alarms[0].Type != "target_unavailable" {
				t.Errorf("alarm type = %q, want target_unavailable", alarms[0].Type)
			}
			if kakaoHits != 0 || smsHits != 0 {
				t.Errorf("no-target path must NOT send external channels, got kakao=%d sms=%d", kakaoHits, smsHits)
			}
		})
	}
}
