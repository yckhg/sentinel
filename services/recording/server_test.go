package main

import (
	"testing"
	"time"
)

// TestNewHTTPServerTimeouts guards the anti-Slowloris hardening (#39). The
// read/header/idle timeouts must be positive so a slow client cannot pin
// goroutines and sockets forever. WriteTimeout is deliberately 0 (unlimited)
// because this service streams large video downloads that must not be
// truncated by a hard write deadline.
func TestNewHTTPServerTimeouts(t *testing.T) {
	srv := newHTTPServer(nil)

	positive := []struct {
		name string
		got  time.Duration
	}{
		{"ReadHeaderTimeout", srv.ReadHeaderTimeout},
		{"ReadTimeout", srv.ReadTimeout},
		{"IdleTimeout", srv.IdleTimeout},
	}
	for _, c := range positive {
		if c.got <= 0 {
			t.Errorf("%s must be > 0 to defend against Slowloris, got %v", c.name, c.got)
		}
	}

	if srv.WriteTimeout != 0 {
		t.Errorf("WriteTimeout must stay 0 (unlimited) for large streamed downloads, got %v", srv.WriteTimeout)
	}
}
