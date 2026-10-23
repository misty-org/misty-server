package browsersync

import (
	"errors"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	// ErrSyncReauth: the password given to confirm a destructive request was wrong.
	ErrSyncReauth = errors.New("sync re-authentication failed")
	// ErrSyncRateLimited: too many attempts in a short time.
	ErrSyncRateLimited = errors.New("sync request rate limited")
)

// attemptLimiter allows a few attempts per key within a window.
type attemptLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	events map[string][]time.Time
}

func newAttemptLimiter(max int, window time.Duration) *attemptLimiter {
	return &attemptLimiter{max: max, window: window, events: map[string][]time.Time{}}
}

func (l *attemptLimiter) recent(key string, now time.Time) []time.Time {
	var kept []time.Time
	for _, at := range l.events[key] {
		if now.Sub(at) < l.window {
			kept = append(kept, at)
		}
	}
	if len(kept) == 0 {
		delete(l.events, key)
		return nil
	}
	l.events[key] = kept
	return kept
}

func (l *attemptLimiter) blocked(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(key, now)) >= l.max
}

// note counts one attempt, and reports whether it was within the limit.
func (l *attemptLimiter) note(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.recent(key, now)) >= l.max {
		return false
	}
	l.events[key] = append(l.events[key], now)
	return true
}

func (l *attemptLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.events, key)
}

// Wrong passwords to confirm deleting a vault: five in fifteen minutes.
var reauthFailures = newAttemptLimiter(5, 15*time.Minute)

// Replacing a vault's wrapper: ten requests in ten minutes per account. A valid
// root signature is required anyway; this only bounds how often it is tried.
var keyRotations = newAttemptLimiter(10, 10*time.Minute)

// confirmPassword checks a password given to confirm a destructive request
// against the account's stored hash. An account that signs in another way
// (Google) has no password, so its session is all there is to check. Wrong
// attempts count toward a limit; a correct one clears them.
func confirmPassword(limiter *attemptLimiter, key, provider, hash, password string, now time.Time) error {
	if provider != "misty" {
		return nil
	}
	if limiter.blocked(key, now) {
		return ErrSyncRateLimited
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		limiter.note(key, now)
		return ErrSyncReauth
	}
	limiter.reset(key)
	return nil
}
