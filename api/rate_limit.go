package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type ForgotPasswordRateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	history map[string][]time.Time
}

func NewForgotPasswordRateLimiter(limit int, window time.Duration) *ForgotPasswordRateLimiter {
	if limit <= 0 {
		limit = 5
	}
	if window <= 0 {
		window = 15 * time.Minute
	}

	return &ForgotPasswordRateLimiter{
		limit:   limit,
		window:  window,
		history: make(map[string][]time.Time),
	}
}

func (l *ForgotPasswordRateLimiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := now.Add(-l.window)
	requests := l.history[key][:0]
	for _, ts := range l.history[key] {
		if ts.After(cutoff) {
			requests = append(requests, ts)
		}
	}

	if len(requests) >= l.limit {
		l.history[key] = requests
		return false
	}

	l.history[key] = append(requests, now)
	return true
}

func forgotPasswordRateLimitKey(r *http.Request, email string) string {
	return clientIPFromRequest(r) + "|" + strings.ToLower(strings.TrimSpace(email))
}

func clientIPFromRequest(r *http.Request) string {
	if forwardedFor := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwardedFor != "" {
		parts := strings.Split(forwardedFor, ",")
		if candidate := strings.TrimSpace(parts[0]); candidate != "" {
			return candidate
		}
	}

	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}

	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}

	if remote := strings.TrimSpace(r.RemoteAddr); remote != "" {
		return remote
	}

	return "unknown"
}
