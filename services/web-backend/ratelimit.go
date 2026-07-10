package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// trustedProxies holds CIDR networks whose X-Forwarded-For / X-Real-IP headers
// are trusted. Empty by default: with no trusted proxy configured, forwarding
// headers are ignored entirely and the TCP peer (RemoteAddr) is used — so a
// client cannot bypass or weaponize the rate limiter by forging X-Forwarded-For.
// Set TRUSTED_PROXIES (comma-separated IPs/CIDRs, e.g. the reverse-proxy address)
// to have the real client IP extracted from forwarding headers behind that proxy.
var trustedProxies []*net.IPNet

// initTrustedProxies parses the TRUSTED_PROXIES env var into CIDR networks.
func initTrustedProxies() {
	trustedProxies = nil
	raw := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES"))
	if raw == "" {
		return
	}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			if strings.Contains(part, ":") {
				part += "/128"
			} else {
				part += "/32"
			}
		}
		_, ipnet, err := net.ParseCIDR(part)
		if err != nil {
			log.Printf("TRUSTED_PROXIES: ignoring invalid entry %q: %v", part, err)
			continue
		}
		trustedProxies = append(trustedProxies, ipnet)
	}
	if len(trustedProxies) > 0 {
		log.Printf("trusted proxies configured: %d network(s)", len(trustedProxies))
	}
}

func isTrustedProxy(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range trustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

type ipRecord struct {
	count     atomic.Int64
	windowEnd atomic.Int64 // unix timestamp
}

type rateLimiter struct {
	maxRequests int64
	window      time.Duration
	entries     sync.Map // map[string]*ipRecord
}

func newRateLimiter(maxRequests int64, window time.Duration) *rateLimiter {
	return &rateLimiter{
		maxRequests: maxRequests,
		window:      window,
	}
}

// allow checks if the IP is within the rate limit. Returns true if allowed.
func (rl *rateLimiter) allow(ip string) bool {
	now := time.Now().Unix()

	val, _ := rl.entries.LoadOrStore(ip, &ipRecord{})
	rec := val.(*ipRecord)

	windowEnd := rec.windowEnd.Load()
	if now >= windowEnd {
		// Window expired — reset
		rec.count.Store(1)
		rec.windowEnd.Store(now + int64(rl.window.Seconds()))
		return true
	}

	// Within current window — increment
	newCount := rec.count.Add(1)
	return newCount <= rl.maxRequests
}

// cleanup removes stale entries older than the window
func (rl *rateLimiter) cleanup() {
	now := time.Now().Unix()
	rl.entries.Range(func(key, value any) bool {
		rec := value.(*ipRecord)
		if now >= rec.windowEnd.Load() {
			rl.entries.Delete(key)
		}
		return true
	})
}

// rateLimitMiddleware wraps an http.HandlerFunc with rate limiting
func rateLimitMiddleware(limiter *rateLimiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !limiter.allow(ip) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many requests, please try again later"})
			return
		}
		next(w, r)
	}
}

// clientIP extracts the client IP used for rate limiting. Forwarding headers
// (X-Forwarded-For / X-Real-IP) are trusted ONLY when the direct TCP peer is a
// configured trusted proxy — otherwise a client could forge them to bypass the
// limiter (rotating IPs) or to throttle a victim. With no trusted proxy set, the
// TCP peer (RemoteAddr) is always used.
func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}

	remoteIP := net.ParseIP(host)
	if !isTrustedProxy(remoteIP) {
		// Untrusted (or unknown) peer — ignore forwarding headers entirely.
		return host
	}

	// Peer is a trusted proxy: derive the real client from X-Forwarded-For,
	// scanning right-to-left and skipping any addresses that are themselves
	// trusted proxies (handles proxy chains). The first non-proxy address is the
	// client as observed by the edge proxy.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			cand := strings.TrimSpace(parts[i])
			ip := net.ParseIP(cand)
			if ip == nil {
				continue
			}
			if isTrustedProxy(ip) {
				continue
			}
			return cand
		}
	}
	// Fall back to X-Real-IP (set by nginx) when no usable XFF entry.
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		if net.ParseIP(xr) != nil {
			return xr
		}
	}
	return host
}

// startRateLimitCleanup starts a goroutine that cleans up stale rate limit entries every 5 minutes
func startRateLimitCleanup(limiters ...*rateLimiter) {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			for _, rl := range limiters {
				rl.cleanup()
			}
			log.Println("rate limit entries cleaned up")
		}
	}()
}
