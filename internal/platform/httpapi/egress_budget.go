package api

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

// Egress accounting.
//
// Request-count limits do not bound bandwidth: 120 downloads a minute is
// harmless for thumbnails and ruinous for 250 MB originals. Object storage
// bills per operation and per byte, and the VPS itself has a transfer
// allowance, so the quantity worth limiting is bytes — charged to the account,
// because that is the identity an attacker cannot rotate.

// EgressBudget bounds bytes served per identity and across the deployment.
type EgressBudget struct {
	PerIdentityDailyBytes int64
	GlobalDailyBytes      int64
}

// DefaultEgressBudget allows heavy legitimate use — a user pulling a large
// library down to a new device — while stopping a scripted drain.
func DefaultEgressBudget() EgressBudget {
	return EgressBudget{
		PerIdentityDailyBytes: 25 << 30,  // 25 GiB
		GlobalDailyBytes:      200 << 30, // 200 GiB
	}
}

// EgressBudgetFromEnv reads the ceiling, falling back to the safe default.
func EgressBudgetFromEnv() EgressBudget {
	budget := DefaultEgressBudget()
	budget.PerIdentityDailyBytes = positiveEnvBytes("MISTY_EGRESS_MAX_BYTES_PER_IDENTITY_DAY", budget.PerIdentityDailyBytes)
	budget.GlobalDailyBytes = positiveEnvBytes("MISTY_EGRESS_MAX_BYTES_PER_DAY", budget.GlobalDailyBytes)
	return budget
}

func positiveEnvBytes(name string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(envconfig.Getenv(name)), 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

type egressRecord struct {
	bytes     int64
	windowEnd time.Time
}

// EgressGuard tracks bytes served and refuses once a ceiling is reached.
type EgressGuard struct {
	TestingMu      sync.Mutex
	TestingNow     func() time.Time
	budget         EgressBudget
	TestingPerKey  map[string]*egressRecord
	global         egressRecord
	TestingMaxKeys int
	refusals       int64
}

func NewEgressGuard(budget EgressBudget) *EgressGuard {
	if budget.PerIdentityDailyBytes <= 0 || budget.GlobalDailyBytes <= 0 {
		budget = DefaultEgressBudget()
	}
	return &EgressGuard{
		TestingNow:     time.Now,
		budget:         budget,
		TestingPerKey:  make(map[string]*egressRecord),
		TestingMaxKeys: defaultLimiterMaxKeys,
	}
}

// Allow reports whether a transfer of size bytes may start.
//
// The check is made before sending and the full size is charged up front, so a
// caller cannot slip past the ceiling by starting many large transfers at once
// and only being charged as they complete.
func (g *EgressGuard) Allow(key string, size int64) bool {
	if size < 0 {
		size = 0
	}
	g.TestingMu.Lock()
	defer g.TestingMu.Unlock()
	now := g.TestingNow()

	if g.global.windowEnd.Before(now) {
		g.global = egressRecord{windowEnd: now.Add(24 * time.Hour)}
	}
	if g.global.bytes >= g.budget.GlobalDailyBytes {
		g.refusals++
		return false
	}

	record := g.TestingPerKey[key]
	if record == nil || record.windowEnd.Before(now) {
		if record == nil {
			if len(g.TestingPerKey) >= g.TestingMaxKeys {
				g.purgeLocked(now)
			}
			if len(g.TestingPerKey) >= g.TestingMaxKeys {
				// Tracking is saturated; the global ceiling still applies.
				g.global.bytes += size
				return true
			}
		}
		record = &egressRecord{windowEnd: now.Add(24 * time.Hour)}
		g.TestingPerKey[key] = record
	}
	if record.bytes >= g.budget.PerIdentityDailyBytes {
		g.refusals++
		return false
	}

	record.bytes += size
	g.global.bytes += size
	return true
}

func (g *EgressGuard) purgeLocked(now time.Time) {
	for key, record := range g.TestingPerKey {
		if record.windowEnd.Before(now) {
			delete(g.TestingPerKey, key)
		}
	}
}

// BytesServed reports what one identity has been charged in the current window.
func (g *EgressGuard) BytesServed(key string) int64 {
	g.TestingMu.Lock()
	defer g.TestingMu.Unlock()
	record := g.TestingPerKey[key]
	if record == nil || record.windowEnd.Before(g.TestingNow()) {
		return 0
	}
	return record.bytes
}

// Refusals reports how many transfers the ceiling has rejected, for monitoring.
func (g *EgressGuard) Refusals() int64 {
	g.TestingMu.Lock()
	defer g.TestingMu.Unlock()
	return g.refusals
}

// WriteQuotaExceeded reports the refusal to the caller.
func WriteQuotaExceeded(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "3600")
	writeJSON(w, http.StatusTooManyRequests, map[string]string{
		"code":    "egress_quota_exceeded",
		"message": "Download limit reached for today. Try again later.",
	})
}
