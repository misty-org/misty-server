package api

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

type SlidingWindowLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	history map[string][]time.Time
	MaxKeys int
}

type RateLimitPolicy struct {
	Limit  int
	Window time.Duration
}

type APIRateLimiter struct {
	Mu            sync.Mutex
	Now           func() time.Time
	defaultGET    RateLimitPolicy
	defaultWrite  RateLimitPolicy
	RoutePolicies map[string]RateLimitPolicy
	Limiters      map[string]*SlidingWindowLimiter
	abuse         *AbuseGuard
}

func (l *APIRateLimiter) WithAbuseGuard(guard *AbuseGuard) *APIRateLimiter {
	l.abuse = guard
	return l
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
		MaxKeys: defaultLimiterMaxKeys,
	}
}

const defaultLimiterMaxKeys = 20000

const collaborationTicketLimit = 120

func NewAPIRateLimiter() *APIRateLimiter {
	limiter := &APIRateLimiter{
		Now:          time.Now,
		defaultGET:   RateLimitPolicy{Limit: 120, Window: time.Minute},
		defaultWrite: RateLimitPolicy{Limit: 30, Window: time.Minute},
		RoutePolicies: map[string]RateLimitPolicy{
			"POST /register":                                         {Limit: 10, Window: time.Minute},
			"POST /login":                                            {Limit: 20, Window: time.Minute},
			"POST /auth/confirm-email":                               {Limit: 20, Window: time.Minute},
			"POST /auth/confirm-email/sign-in":                       {Limit: 20, Window: time.Minute},
			"POST /setup-keys/redeem":                                {Limit: 20, Window: time.Minute},
			"GET /operator/v1/accounts":                              {Limit: 600, Window: time.Minute},
			"POST /operator/v1/accounts":                             {Limit: 600, Window: time.Minute},
			"GET /operator/v1/accounts/{id}":                         {Limit: 600, Window: time.Minute},
			"PATCH /operator/v1/accounts/{id}":                       {Limit: 600, Window: time.Minute},
			"POST /operator/v1/accounts/{id}/suspend":                {Limit: 600, Window: time.Minute},
			"POST /operator/v1/accounts/{id}/resume":                 {Limit: 600, Window: time.Minute},
			"POST /operator/v1/accounts/{id}/setup-key":              {Limit: 60, Window: time.Minute},
			"POST /operator/v1/accounts/{id}/export":                 {Limit: 20, Window: time.Hour},
			"POST /auth/google":                                      {Limit: 10, Window: time.Minute},
			"GET /auth/google/start":                                 {Limit: 20, Window: time.Minute},
			"GET /auth/google/callback":                              {Limit: 20, Window: time.Minute},
			"POST /auth/google/complete":                             {Limit: 60, Window: time.Minute},
			"POST /me/account-proof":                                 {Limit: 20, Window: time.Minute},
			"POST /me/web-handoff":                                   {Limit: 10, Window: time.Minute},
			"POST /auth/web-handoff":                                 {Limit: 20, Window: time.Minute},
			"GET /invites/{code}":                                    {Limit: 30, Window: time.Minute},
			"POST /me/export":                                        {Limit: 3, Window: time.Hour},
			"POST /me/deletion":                                      {Limit: 3, Window: time.Hour},
			"POST /friends/requests":                                 {Limit: 10, Window: time.Minute},
			"POST /spaces/{spaceID}/notes/{id}/collaboration-ticket": {Limit: collaborationTicketLimit, Window: time.Minute},
			"POST /spaces/{spaceID}/drive/items/{id}/collaboration-ticket": {
				Limit: collaborationTicketLimit, Window: time.Minute,
			},
			"POST /spaces/{spaceID}/apps/{id}/rooms/{id}/collaboration-ticket": {
				Limit: collaborationTicketLimit, Window: time.Minute,
			},
			"POST /spaces/{spaceID}/notes/{id}/history/{id}/restore": {Limit: 10, Window: time.Minute},
			"GET /spaces/{spaceID}/notes/{id}/history/{id}":          {Limit: 60, Window: time.Minute},
			"POST /spaces/{spaceID}/notes/{id}/assets/uploads":       {Limit: 20, Window: time.Minute},
			"POST /spaces/{spaceID}/notes/{id}/assets/uploads/{id}/finalize": {
				Limit: 30, Window: time.Minute,
			},
		},
		Limiters: make(map[string]*SlidingWindowLimiter),
	}
	addSignInRoutePolicies(limiter.RoutePolicies)
	return limiter
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

	if len(requests) == 0 && len(l.history) >= l.MaxKeys {
		l.purgeExpired(cutoff)
	}
	if len(requests) == 0 && len(l.history) >= l.MaxKeys {
		return false, l.window
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

func (l *SlidingWindowLimiter) purgeExpired(cutoff time.Time) {
	for key, timestamps := range l.history {
		live := false
		for _, timestamp := range timestamps {
			if timestamp.After(cutoff) {
				live = true
				break
			}
		}
		if !live {
			delete(l.history, key)
		}
	}
}

func (l *APIRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || operatorRequest(r) {
			next.ServeHTTP(w, r)
			return
		}

		path := normalizeSignInRateLimitPath(NormalizeRateLimitPath(r.URL.Path))
		policy := l.policyFor(r.Method, path)
		limiter := l.limiterFor(r.Method, path, policy)
		key := ClientIPFromRequest(r)
		if accountKeyedRoutes[path] {
			key = AuthenticatedRateLimitIdentity(r)
		} else if costBearingRoutes[path] {
			key = RateLimitIdentity(r)
		}
		allowed, retryAfter := limiter.Allow(key, l.Now())
		if !allowed {
			w.Header().Set("X-Misty-RateLimit-Scope", "route")
			if l.abuse != nil {
				l.abuse.RecordRejection(key)
			}
			w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (l *APIRateLimiter) policyFor(method, path string) RateLimitPolicy {
	if policy, ok := l.RoutePolicies[method+" "+path]; ok {
		return policy
	}
	if method == http.MethodGet {
		return l.defaultGET
	}
	return l.defaultWrite
}

const MaxTrackedRoutes = 512

func (l *SlidingWindowLimiter) TrackedKeys() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.history)
}
