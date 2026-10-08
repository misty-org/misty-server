package api

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/kannachi323/misty/server/internal/accounts"
)

// Login is limited per address by the API limiter, which leaves one account
// open to guessing spread across many addresses. This bounds failed attempts
// per email address instead. A success clears the count.
const (
	loginFailureLimit   = 10
	loginFailureWindow  = 15 * time.Minute
	loginFailureMaxKeys = 50000
)

type loginFailureTracker struct {
	mu       sync.Mutex
	failures map[string][]time.Time
}

var loginFailures = &loginFailureTracker{failures: map[string][]time.Time{}}

func loginFailureKey(email string) string {
	sum := sha256.Sum256([]byte(accounts.NormalizeEmail(email)))
	return hex.EncodeToString(sum[:16])
}

func (t *loginFailureTracker) recent(key string, now time.Time) []time.Time {
	cutoff := now.Add(-loginFailureWindow)
	live := t.failures[key][:0]
	for _, at := range t.failures[key] {
		if at.After(cutoff) {
			live = append(live, at)
		}
	}
	if len(live) == 0 {
		delete(t.failures, key)
		return nil
	}
	t.failures[key] = live
	return live
}

// Blocked reports whether the address has used up its failures, and when the
// oldest one expires.
func (t *loginFailureTracker) Blocked(email string, now time.Time) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	live := t.recent(loginFailureKey(email), now)
	if len(live) < loginFailureLimit {
		return false, 0
	}
	return true, live[0].Add(loginFailureWindow).Sub(now)
}

func (t *loginFailureTracker) Fail(email string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := loginFailureKey(email)
	if _, tracked := t.failures[key]; !tracked && len(t.failures) >= loginFailureMaxKeys {
		for other := range t.failures {
			t.recent(other, now)
		}
		if len(t.failures) >= loginFailureMaxKeys {
			return
		}
	}
	t.failures[key] = append(t.recent(key, now), now)
}

func (t *loginFailureTracker) Succeed(email string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.failures, loginFailureKey(email))
}
