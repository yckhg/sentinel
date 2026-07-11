package main

import (
	"strings"
	"testing"
	"time"
)

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

// TestNewHTTPServerTimeouts guards the anti-Slowloris hardening (#39): the
// server must never fall back to Go's default 0 (unlimited) header/read/idle
// timeouts, otherwise a slow client can pin goroutines and sockets forever.
func TestNewHTTPServerTimeouts(t *testing.T) {
	srv := newHTTPServer(nil)

	cases := []struct {
		name string
		got  time.Duration
	}{
		{"ReadHeaderTimeout", srv.ReadHeaderTimeout},
		{"ReadTimeout", srv.ReadTimeout},
		{"WriteTimeout", srv.WriteTimeout},
		{"IdleTimeout", srv.IdleTimeout},
	}
	for _, c := range cases {
		if c.got <= 0 {
			t.Errorf("%s must be > 0 to defend against Slowloris, got %v", c.name, c.got)
		}
	}
}
