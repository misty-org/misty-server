package api

import (
	"context"
	"log"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/workqueue"
)

// One temporary writer per guard drains the bounded strike map. Rejections
// never create a goroutine per caller or hold the request while PostgreSQL waits.
// Called with g.mu held. The writer exits as soon as no writes remain.
func (g *AbuseGuard) startPersistenceLocked() {
	if g.store == nil || g.persistRunning || g.persistCtx.Err() != nil {
		return
	}
	g.persistRunning = true
	go func() {
		ctx, cancel := context.WithCancel(g.persistCtx)
		defer cancel()
		q := workqueue.Queue{
			Subscribe: func(context.Context) (<-chan struct{}, func(), error) { return make(chan struct{}), func() {}, nil },
			Next: func(context.Context) (time.Duration, bool, error) {
				g.mu.Lock()
				defer g.mu.Unlock()
				now := g.TestingNow()
				for _, record := range g.strikes {
					if record.pendingPersistence != 0 && record.blockedUntil.After(now) {
						return 0, true, nil
					}
				}
				// A later rejection starts its own writer under this same lock.
				g.persistRunning = false
				cancel()
				return 0, false, nil
			},
			Process: g.persistNextBlock,
			OnError: func(err error) { log.Printf("abuse block persistence: %v", err) },
		}
		q.Run(ctx)
	}()
}

func (g *AbuseGuard) persistNextBlock(ctx context.Context) (int, error) {
	// Serializing snapshots with successful writes prevents a stale snapshot
	// from forgiving an in-flight block, and permits explicit operator unblocks.
	g.storeMu.Lock()
	defer g.storeMu.Unlock()
	g.mu.Lock()
	key := ""
	var record abuseRecord
	for candidate, r := range g.strikes {
		if r.pendingPersistence != 0 && r.blockedUntil.After(g.TestingNow()) {
			key, record = candidate, *r
			break
		}
	}
	g.mu.Unlock()
	if key == "" {
		return 0, nil
	}
	seconds := int(record.blockLength / time.Second)
	if seconds <= 0 {
		seconds = 60
	}
	write, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := g.store.SaveAbuseBlock(write, db.AbuseBlock{
		Key: key, BlockedUntil: record.blockedUntil, BlockSeconds: seconds, Reason: "rate_limit_abuse",
	}); err != nil {
		return 0, err
	}
	g.mu.Lock()
	if current := g.strikes[key]; current != nil && current.pendingPersistence == record.pendingPersistence {
		current.pendingPersistence = 0
	}
	g.mu.Unlock()
	return 1, nil
}

// Close stops pending persistence retries. Run calls it when the server stops.
func (g *AbuseGuard) Close() {
	if g.persistCancel != nil {
		g.persistCancel()
	}
}
