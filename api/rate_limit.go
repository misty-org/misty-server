package api

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SlidingWindowLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	history map[string][]time.Time
}

type ForgotPasswordRateLimiter struct {
	*SlidingWindowLimiter
}

type RateLimitPolicy struct {
	Limit  int
	Window time.Duration
}

type APIRateLimiter struct {
	mu            sync.Mutex
	now           func() time.Time
	defaultGET    RateLimitPolicy
	defaultWrite  RateLimitPolicy
	routePolicies map[string]RateLimitPolicy
	limiters      map[string]*SlidingWindowLimiter
}

func NewSlidingWindowLimiter(limit int, window time.Duration) *SlidingWindowLimiter {
	if limit <= 0 {
		limit = 5
	}
	if window <= 0 {
		window = time.Minute
	}

	return &SlidingWindowLimiter{
		limit:   limit,
		window:  window,
		history: make(map[string][]time.Time),
	}
}

func NewForgotPasswordRateLimiter(limit int, window time.Duration) *ForgotPasswordRateLimiter {
	return &ForgotPasswordRateLimiter{
		SlidingWindowLimiter: NewSlidingWindowLimiter(limit, window),
	}
}

func NewAPIRateLimiter() *APIRateLimiter {
	return &APIRateLimiter{
		now:          time.Now,
		defaultGET:   RateLimitPolicy{Limit: 120, Window: time.Minute},
		defaultWrite: RateLimitPolicy{Limit: 30, Window: time.Minute},
		routePolicies: map[string]RateLimitPolicy{
			"POST /register":           {Limit: 10, Window: time.Minute},
			"POST /login":              {Limit: 20, Window: time.Minute},
			"POST /waitlist":           {Limit: 10, Window: time.Minute},
			"POST /auth/forgot":        {Limit: 8, Window: time.Minute},
			"GET /auth/reset/start":    {Limit: 20, Window: time.Minute},
			"GET /auth/reset/validate": {Limit: 20, Window: time.Minute},
			"POST /auth/reset":         {Limit: 10, Window: time.Minute},
			"POST /license/validate":   {Limit: 30, Window: time.Minute},
			"POST /stripe/webhook":     {Limit: 120, Window: time.Minute},
			"GET /subscription":        {Limit: 60, Window: time.Minute},
		},
		limiters: make(map[string]*SlidingWindowLimiter),
	}
}

func (l *SlidingWindowLimiter) Allow(key string, now time.Time) (bool, time.Duration) {
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
		retryAfter := requests[0].Add(l.window).Sub(now)
		if retryAfter < 0 {
			retryAfter = 0
		}
		return false, retryAfter
	}

	l.history[key] = append(requests, now)
	return true, 0
}

func (l *APIRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		path := normalizeRateLimitPath(r.URL.Path)
		policy := l.policyFor(r.Method, path)
		limiter := l.limiterFor(r.Method, path, policy)
		allowed, retryAfter := limiter.Allow(clientIPFromRequest(r)+"|"+r.Method+"|"+path, l.now())
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (l *APIRateLimiter) policyFor(method, path string) RateLimitPolicy {
	if policy, ok := l.routePolicies[method+" "+path]; ok {
		return policy
	}
	if method == http.MethodGet {
		return l.defaultGET
	}
	return l.defaultWrite
}

func (l *APIRateLimiter) limiterFor(method, path string, policy RateLimitPolicy) *SlidingWindowLimiter {
	key := method + " " + path

	l.mu.Lock()
	defer l.mu.Unlock()

	limiter, ok := l.limiters[key]
	if !ok {
		limiter = NewSlidingWindowLimiter(policy.Limit, policy.Window)
		l.limiters[key] = limiter
	}
	return limiter
}

func normalizeRateLimitPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "/"
	}
	if strings.HasPrefix(trimmed, "/api/") {
		return trimmed[len("/api"):]
	}
	if trimmed == "/api" {
		return "/"
	}
	return trimmed
}

func retryAfterSeconds(duration time.Duration) int {
	if duration <= 0 {
		return 1
	}
	seconds := int(duration / time.Second)
	if duration%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return seconds
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
