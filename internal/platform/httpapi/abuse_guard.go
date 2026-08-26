package api

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// AbuseGuard is the outermost layer: a total request ceiling per caller, plus
// a temporary block for callers that keep hitting limits.
//
// Per-route limits alone still let a caller spend a little budget on every
// route. This bounds the total, and turns sustained abuse into a cheap
// rejection instead of work the server keeps paying for.
type AbuseGuard struct {
	mu sync.Mutex

	TestingNow     func() time.Time
	total          *SlidingWindowLimiter
	strikes        map[string]*abuseRecord
	TestingMaxKeys int
	policy         AbusePolicy
	// store persists blocks so they survive a restart and apply to every
	// instance. Counters stay in memory: they change on every request and are
	// cheap to rebuild, whereas a lost block is a reopened door.
	store AbuseBlockStore
}

// AbuseBlockStore persists blocks. Implemented by the database; nil keeps the
// guard purely in-process, which is what tests and single-node runs use.
type AbuseBlockStore interface {
	SaveAbuseBlock(ctx context.Context, block db.AbuseBlock) error
	ActiveAbuseBlocks(ctx context.Context) ([]db.AbuseBlock, error)
}

// AbusePolicy tunes the ceiling and the escalation.
type AbusePolicy struct {
	// TotalLimit/TotalWindow bound every request from one caller.
	TotalLimit  int
	TotalWindow time.Duration
	// StrikesBeforeBlock is how many rejections are tolerated inside
	// StrikeWindow before the caller is blocked outright.
	StrikesBeforeBlock int
	StrikeWindow       time.Duration
	// BaseBlock doubles on each repeat block, up to MaxBlock.
	BaseBlock time.Duration
	MaxBlock  time.Duration
}

type abuseRecord struct {
	strikes      int
	firstStrike  time.Time
	blockedUntil time.Time
	blockLength  time.Duration
}

// DefaultAbusePolicy is sized for a desktop client that polls and syncs, while
// still cutting off a caller that is clearly hammering the API.
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
		TestingNow:     time.Now,
		total:          NewSlidingWindowLimiter(policy.TotalLimit, policy.TotalWindow),
		strikes:        make(map[string]*abuseRecord),
		TestingMaxKeys: defaultLimiterMaxKeys,
		policy:         policy,
	}
}

// Blocked reports whether a caller is currently serving a block.
func (g *AbuseGuard) Blocked(key string) (bool, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	record := g.strikes[key]
	if record == nil {
		return false, 0
	}
	now := g.TestingNow()
	if record.blockedUntil.After(now) {
		return true, record.blockedUntil.Sub(now)
	}
	return false, 0
}

// AllowTotal charges one request against the caller's overall ceiling.
func (g *AbuseGuard) AllowTotal(key string) (bool, time.Duration) {
	return g.total.Allow(key, g.TestingNow())
}

// RecordRejection notes that a caller was refused. Enough refusals inside the
// strike window escalates to a block, so an abusive client stops costing the
// server routing and handler work entirely.
func (g *AbuseGuard) RecordRejection(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.TestingNow()

	record := g.strikes[key]
	if record == nil {
		if len(g.strikes) >= g.TestingMaxKeys {
			g.purgeLocked(now)
		}
		if len(g.strikes) >= g.TestingMaxKeys {
			// Tracking is saturated; the per-route limits still apply.
			return
		}
		record = &abuseRecord{firstStrike: now}
		g.strikes[key] = record
	}

	// Strikes are counted inside a rolling window so an occasional 429 from a
	// legitimate burst never accumulates into a block over hours.
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
	g.persistBlock(key, *record)
}

// persistBlock writes the block out so a restart or a sibling instance honours
// it. Done without holding the caller's request: a storage failure must not
// turn into a failed block.
func (g *AbuseGuard) persistBlock(key string, record abuseRecord) {
	if g.store == nil {
		return
	}
	block := db.AbuseBlock{
		Key:          key,
		BlockedUntil: record.blockedUntil,
		BlockSeconds: int(record.blockLength / time.Second),
		Reason:       "rate_limit_abuse",
	}
	if block.BlockSeconds <= 0 {
		block.BlockSeconds = 60
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = g.store.SaveAbuseBlock(ctx, block)
	}()
}

// purgeLocked drops records that are neither blocked nor recently active.
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

// TrackedKeys reports how many callers have strike records.
func (g *AbuseGuard) TrackedKeys() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.strikes)
}

// WithStore attaches persistence and loads any blocks already in force, so a
// freshly started process does not forgive callers the previous one blocked.
func (g *AbuseGuard) WithStore(ctx context.Context, store AbuseBlockStore) *AbuseGuard {
	if store == nil {
		return g
	}
	g.store = store
	g.Refresh(ctx)
	return g
}

// Refresh reloads live blocks from the store. Called on start and on a timer,
// which is how a block raised by one instance reaches the others.
func (g *AbuseGuard) Refresh(ctx context.Context) {
	if g.store == nil {
		return
	}
	blocks, err := g.store.ActiveAbuseBlocks(ctx)
	if err != nil {
		// A database blip must not drop the in-memory blocks already held.
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, block := range blocks {
		record := g.strikes[block.Key]
		if record == nil {
			if len(g.strikes) >= g.TestingMaxKeys {
				continue
			}
			record = &abuseRecord{firstStrike: g.TestingNow()}
			g.strikes[block.Key] = record
		}
		if block.BlockedUntil.After(record.blockedUntil) {
			record.blockedUntil = block.BlockedUntil
		}
		if stored := time.Duration(block.BlockSeconds) * time.Second; stored > record.blockLength {
			record.blockLength = stored
		}
	}
}

// StartRefreshLoop keeps this instance's view of blocks current.
func (g *AbuseGuard) StartRefreshLoop(ctx context.Context, interval time.Duration) {
	if g.store == nil {
		return
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				g.Refresh(ctx)
			}
		}
	}()
}

// Middleware rejects blocked callers before any routing or handler work.
func (g *AbuseGuard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || isAgentRuntimeCallback(r) {
			next.ServeHTTP(w, r)
			return
		}
		key := TestingClientIPFromRequest(r)

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
	seconds := retryAfterSeconds(retryAfter)
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	http.Error(w, message, http.StatusTooManyRequests)
}
