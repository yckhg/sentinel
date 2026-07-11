package main

import (
	"strings"
	"testing"
)

// TestMaskPhone locks the PII phone-masking used in logs (#43): the middle
// digits must never survive, while a correlatable head/tail remains.
func TestMaskPhone(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"010-1234-5678", "010-****-5678"},
		{"01012345678", "010-****-5678"},
		{"123456", "****"}, // fewer than 7 digits → fully masked
		{"+82 10 9876 5432", "821-****-5432"},
	}
	for _, c := range cases {
		got := maskPhone(c.in)
		if got != c.want {
			t.Errorf("maskPhone(%q)=%q want %q", c.in, got, c.want)
		}
		if strings.Contains(got, "****") && strings.Contains(got, "1234") {
			t.Errorf("maskPhone(%q)=%q leaked masked middle digits", c.in, got)
		}
	}
}

// TestMaskEmail locks the PII email-masking used in logs (#43): the local part
// is reduced to its first character; the domain is preserved for diagnostics.
func TestMaskEmail(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"john@example.com", "j***@example.com"},
		{"a@b.com", "*@b.com"},
		{"noatsign", "***"},
	}
	for _, c := range cases {
		got := maskEmail(c.in)
		if got != c.want {
			t.Errorf("maskEmail(%q)=%q want %q", c.in, got, c.want)
		}
		if c.in == "john@example.com" && strings.Contains(got, "ohn") {
			t.Errorf("maskEmail(%q)=%q leaked local part", c.in, got)
		}
	}
}
