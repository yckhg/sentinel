package main

import (
	"testing"
	"time"
)

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
