package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type eventBlockStore struct {
	mu                  sync.Mutex
	blocks              map[string]db.AbuseBlock
	hints               chan struct{}
	subscribed          chan struct{}
	reads, writes       int
	failRead, failWrite bool
	saveGate            <-chan struct{}
}

func newEventBlockStore() *eventBlockStore {
	return &eventBlockStore{blocks: map[string]db.AbuseBlock{}, hints: make(chan struct{}, 1), subscribed: make(chan struct{})}
}
func (s *eventBlockStore) hint() {
	select {
	case s.hints <- struct{}{}:
	default:
	}
}
func (s *eventBlockStore) SubscribeAbuseBlockEvents(context.Context) (<-chan struct{}, func(), error) {
	close(s.subscribed)
	return s.hints, func() {}, nil
}
func (s *eventBlockStore) ActiveAbuseBlocks(context.Context) ([]db.AbuseBlock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.failRead {
		return nil, errors.New("read unavailable")
	}
	out := []db.AbuseBlock{}
	for _, b := range s.blocks {
		out = append(out, b)
	}
	return out, nil
}
func (s *eventBlockStore) SaveAbuseBlock(ctx context.Context, b db.AbuseBlock) error {
	s.mu.Lock()
	s.writes++
	fail, gate := s.failWrite, s.saveGate
	s.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if fail {
		return errors.New("write unavailable")
	}
	s.mu.Lock()
	s.blocks[b.Key] = b
	s.mu.Unlock()
	s.hint()
	return nil
}
func awaitAbuse(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("abuse guard did not reach expected state")
}
func startEventGuard(t *testing.T, s *eventBlockStore) *AbuseGuard {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	p := DefaultAbusePolicy()
	p.StrikesBeforeBlock = 1
	g := NewAbuseGuard(p).WithStore(ctx, s)
	stopped := make(chan struct{})
	go func() { defer close(stopped); g.Run(ctx, nil) }()
	t.Cleanup(func() {
		cancel()
		g.Close()
		select {
		case <-stopped:
		case <-time.After(6 * time.Second):
			t.Error("guard did not stop")
		}
	})
	<-s.subscribed
	awaitAbuse(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.reads >= 2 })
	return g
}
func TestAbuseEventsStayIdleAndApplyBlockUnblockAndReset(t *testing.T) {
	s := newEventBlockStore()
	g := startEventGuard(t, s)
	s.mu.Lock()
	reads := s.reads
	s.mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	s.mu.Lock()
	if s.reads != reads {
		t.Error("idle guard polled", s.reads)
	}
	s.mu.Unlock()
	s.mu.Lock()
	s.blocks["remote"] = db.AbuseBlock{Key: "remote", BlockedUntil: time.Now().Add(time.Minute), BlockSeconds: 60}
	s.mu.Unlock()
	s.hint()
	awaitAbuse(t, func() bool { blocked, _ := g.Blocked("remote"); return blocked })
	s.mu.Lock()
	delete(s.blocks, "remote")
	s.mu.Unlock()
	s.hint()
	awaitAbuse(t, func() bool { blocked, _ := g.Blocked("remote"); return !blocked })
	// A reconnect hint performs the same authoritative read, recovering a missed change.
	s.mu.Lock()
	s.blocks["missed"] = db.AbuseBlock{Key: "missed", BlockedUntil: time.Now().Add(time.Minute), BlockSeconds: 60}
	s.mu.Unlock()
	s.hint()
	awaitAbuse(t, func() bool { blocked, _ := g.Blocked("missed"); return blocked })
}
func TestAbuseEventsFailurePreservesBlocksAndRetriesWithoutAnotherHint(t *testing.T) {
	s := newEventBlockStore()
	g := startEventGuard(t, s)
	s.mu.Lock()
	s.blocks["remote"] = db.AbuseBlock{Key: "remote", BlockedUntil: time.Now().Add(time.Minute), BlockSeconds: 60}
	s.mu.Unlock()
	g.Refresh(context.Background())
	s.mu.Lock()
	delete(s.blocks, "remote")
	s.failRead = true
	reads := s.reads
	s.mu.Unlock()
	s.hint()
	awaitAbuse(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.reads > reads })
	if blocked, _ := g.Blocked("remote"); !blocked {
		t.Fatal("failed read forgave block")
	}
	s.mu.Lock()
	s.failRead = false
	s.mu.Unlock()
	awaitAbuse(t, func() bool { blocked, _ := g.Blocked("remote"); return !blocked })
}
func TestAbusePersistenceIsBoundedAndSnapshotCannotForgivePendingWrites(t *testing.T) {
	s := newEventBlockStore()
	gate := make(chan struct{})
	s.saveGate = gate
	g := startEventGuard(t, s)
	for i := 0; i < 100; i++ {
		g.RecordRejection(fmt.Sprint(i))
	}
	awaitAbuse(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.writes == 1 })
	time.Sleep(30 * time.Millisecond)
	s.mu.Lock()
	if s.writes != 1 {
		t.Error("concurrent persistence writers", s.writes)
	}
	s.mu.Unlock()
	if blocked, _ := g.Blocked("99"); !blocked {
		t.Fatal("pending block was not enforced")
	}
	close(gate)
	awaitAbuse(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.blocks) == 100 })
	awaitAbuse(t, func() bool { g.mu.Lock(); defer g.mu.Unlock(); return !g.persistRunning })
	s.mu.Lock()
	delete(s.blocks, "99")
	s.mu.Unlock()
	s.hint()
	awaitAbuse(t, func() bool { blocked, _ := g.Blocked("99"); return !blocked })
}
func TestAbusePersistenceFailureKeepsLocalBlockAndRetries(t *testing.T) {
	s := newEventBlockStore()
	s.failWrite = true
	g := startEventGuard(t, s)
	g.RecordRejection("local")
	awaitAbuse(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.writes > 0 })
	g.Refresh(context.Background())
	if blocked, _ := g.Blocked("local"); !blocked {
		t.Fatal("snapshot forgave failed local write")
	}
	s.mu.Lock()
	s.failWrite = false
	s.mu.Unlock()
	awaitAbuse(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.blocks) == 1 })
}
