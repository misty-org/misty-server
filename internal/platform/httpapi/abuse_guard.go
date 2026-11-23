package api

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
)

type AbuseGuard struct {
	mu              sync.Mutex
	storeMu         sync.Mutex
	persistSequence uint64
	persistRunning  bool
	persistCtx      context.Context
	persistCancel   context.CancelFunc

	Now     func() time.Time
	total   *SlidingWindowLimiter
	strikes map[string]*abuseRecord
	MaxKeys int
	policy  AbusePolicy
	store   AbuseBlockStore
}

type AbuseBlockStore interface {
	SaveAbuseBlock(ctx context.Context, block db.AbuseBlock) error
	ActiveAbuseBlocks(ctx context.Context) ([]db.AbuseBlock, error)
}

type AbusePolicy struct {
	TotalLimit         int
	TotalWindow        time.Duration
	StrikesBeforeBlock int
	StrikeWindow       time.Duration
	BaseBlock          time.Duration
	MaxBlock           time.Duration
}

type abuseRecord struct {
	strikes            int
	firstStrike        time.Time
	blockedUntil       time.Time
	blockLength        time.Duration
	pendingPersistence uint64
}

func DefaultAbusePolicy() AbusePolicy {
	return AbusePolicy{
		TotalLimit:         600,
		TotalWindow:        time.Minute,
		StrikesBeforeBlock: 60,
		StrikeWindow:       5 * time.Minute,
		BaseBlock:          time.Minute,
		MaxBlock:           30 * time.Minute,
	}
}

func NewAbuseGuard(policy AbusePolicy) *AbuseGuard {
	if policy.TotalLimit <= 0 {
		policy = DefaultAbusePolicy()
	}
	return &AbuseGuard{
		Now:     time.Now,
		total:   NewSlidingWindowLimiter(policy.TotalLimit, policy.TotalWindow),
		strikes: make(map[string]*abuseRecord),
		MaxKeys: defaultLimiterMaxKeys,
		policy:  policy,
	}
}

func (g *AbuseGuard) Blocked(key string) (bool, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	record := g.strikes[key]
	if record == nil {
		return false, 0
	}
	now := g.Now()
	if record.blockedUntil.After(now) {
		return true, record.blockedUntil.Sub(now)
	}
	return false, 0
}

func (g *AbuseGuard) AllowTotal(key string) (bool, time.Duration) {
	return g.total.Allow(key, g.Now())
}

func (g *AbuseGuard) RecordRejection(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.Now()

	record := g.strikes[key]
	if record == nil {
		if len(g.strikes) >= g.MaxKeys {
			g.purgeLocked(now)
		}
		if len(g.strikes) >= g.MaxKeys {
			return
		}
		record = &abuseRecord{firstStrike: now}
		g.strikes[key] = record
	}

	if now.Sub(record.firstStrike) > g.policy.StrikeWindow {
		record.strikes, record.firstStrike = 0, now
	}
	record.strikes++
	if record.strikes < g.policy.StrikesBeforeBlock {
		return
	}

	record.blockLength *= 2
	if record.blockLength < g.policy.BaseBlock {
		record.blockLength = g.policy.BaseBlock
	}
	if record.blockLength > g.policy.MaxBlock {
		record.blockLength = g.policy.MaxBlock
	}
	record.blockedUntil = now.Add(record.blockLength)
	record.strikes, record.firstStrike = 0, now
	g.persistSequence++
	record.pendingPersistence = g.persistSequence
	g.startPersistenceLocked()
}

func (g *AbuseGuard) purgeLocked(now time.Time) {
	for key, record := range g.strikes {
		if record.blockedUntil.After(now) {
			continue
		}
		if now.Sub(record.firstStrike) <= g.policy.StrikeWindow {
			continue
		}
		delete(g.strikes, key)
	}
}

func (g *AbuseGuard) WithStore(ctx context.Context, store AbuseBlockStore) *AbuseGuard {
	if store == nil {
		return g
	}
	g.store = store
	g.persistCtx, g.persistCancel = context.WithCancel(ctx)
	g.Refresh(ctx)
	return g
}

func (g *AbuseGuard) Refresh(ctx context.Context) { _ = g.refresh(ctx) }

func (g *AbuseGuard) refresh(ctx context.Context) error {
	if g.store == nil {
		return nil
	}
	g.storeMu.Lock()
	defer g.storeMu.Unlock()
	blocks, err := g.store.ActiveAbuseBlocks(ctx)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.Now()
	g.purgeLocked(now)
	for _, record := range g.strikes {
		if record.pendingPersistence == 0 {
			record.blockedUntil = time.Time{}
		}
	}
	for _, block := range blocks {
		record := g.strikes[block.Key]
		if record == nil {
			if len(g.strikes) >= g.MaxKeys {
				continue
			}
			record = &abuseRecord{firstStrike: now}
			g.strikes[block.Key] = record
		}
		if block.BlockedUntil.After(record.blockedUntil) {
			record.blockedUntil = block.BlockedUntil
		}
		if stored := time.Duration(block.BlockSeconds) * time.Second; stored > record.blockLength {
			record.blockLength = stored
		}
	}
	return nil
}

func (g *AbuseGuard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || TrustedServiceCallback(r) {
			next.ServeHTTP(w, r)
			return
		}
		key := ClientIPFromRequest(r)

		if blocked, retryAfter := g.Blocked(key); blocked {
			writeAbuseRejection(w, retryAfter, "temporarily blocked")
			return
		}
		if allowed, retryAfter := g.AllowTotal(key); !allowed {
			g.RecordRejection(key)
			writeAbuseRejection(w, retryAfter, "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeAbuseRejection(w http.ResponseWriter, retryAfter time.Duration, message string) {
	w.Header().Set("X-Misty-RateLimit-Scope", "origin")
	seconds := retryAfterSeconds(retryAfter)
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	http.Error(w, message, http.StatusTooManyRequests)
}

func (g *AbuseGuard) TrackedKeys() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.strikes)
}
