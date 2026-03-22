package main

import (
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

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

// clientIP extracts the client IP from the request, checking X-Forwarded-For first
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP (original client)
		if idx := strings.IndexByte(xff, ','); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	// Fall back to RemoteAddr (strip port)
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
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
