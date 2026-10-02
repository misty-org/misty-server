package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type fakeInvocationStreamStore struct {
	mu                   sync.Mutex
	events               []db.AIInvocationEventRecord
	state                string
	expiry               time.Time
	hints                map[chan struct{}]struct{}
	cursors              []int64
	reads, subscriptions int
	limit                int
	err                  error
	onRead               func()
}

func newFakeInvocationStore() *fakeInvocationStreamStore {
	return &fakeInvocationStreamStore{state: "running", expiry: time.Now().Add(time.Hour), hints: map[chan struct{}]struct{}{}, limit: 8}
}
func (s *fakeInvocationStreamStore) SubscribeAIInvocationEvents(context.Context, string) (<-chan struct{}, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscriptions++
	ch := make(chan struct{}, 1)
	s.hints[ch] = struct{}{}
	return ch, func() { s.mu.Lock(); delete(s.hints, ch); s.mu.Unlock() }, nil
}
func (s *fakeInvocationStreamStore) ReadAIInvocationEventPage(_ context.Context, user, _ string, after int64) (db.AIInvocationEventPage, error) {
	s.mu.Lock()
	s.reads++
	s.cursors = append(s.cursors, after)
	page := db.AIInvocationEventPage{State: s.state, Head: int64(len(s.events)), ExpiresAt: s.expiry}
	err := s.err
	if user != "owner" {
		err = db.ErrSpaceNotFound
	}
	if err == nil {
		for _, event := range s.events {
			if event.Sequence > after {
				page.Events = append(page.Events, event)
				if len(page.Events) == s.limit {
					break
				}
			}
		}
	}
	hook := s.onRead
	s.onRead = nil
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	return page, err
}
func (s *fakeInvocationStreamStore) publish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.hints {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
func (s *fakeInvocationStreamStore) append(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq := int64(len(s.events) + 1)
	payload, _ := json.Marshal(map[string]any{"id": fmt.Sprint(seq), "type": "assistant.message", "text": text})
	s.events = append(s.events, db.AIInvocationEventRecord{Sequence: seq, EventType: "assistant.message", Payload: payload})
}
func awaitInvocation(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("invocation stream did not reach expected state")
}
func streamCaughtUp(s *invocationStream, head int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.known && s.cursor == head
}
func TestInvocationStreamSharesIncrementalReadsAndEvictsOnLastViewer(t *testing.T) {
	store := newFakeInvocationStore()
	h := newInvocationStreams(store)
	a, stopA := h.acquire("owner", "invocation", 0)
	defer stopA()
	b, stopB := h.acquire("owner", "invocation", 0)
	defer stopB()
	if a != b {
		t.Fatal("viewers did not share a stream")
	}
	awaitInvocation(t, func() bool { return streamCaughtUp(a, 0) })
	store.append("one")
	store.publish()
	awaitInvocation(t, func() bool { return streamCaughtUp(a, 1) })
	for _, s := range []*invocationStream{a, b} {
		page, err := s.read(context.Background(), 0)
		if err != nil || len(page.events) != 1 {
			t.Fatal(page, err)
		}
	}
	store.mu.Lock()
	reads := store.reads
	store.mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	store.mu.Lock()
	if store.reads != reads || store.subscriptions != 1 {
		t.Error("idle/shared reads", store.reads, reads, store.subscriptions)
	}
	store.mu.Unlock()
	stopA()
	h.mu.Lock()
	if len(h.streams) != 1 {
		t.Error("evicted active peer")
	}
	h.mu.Unlock()
	stopB()
	awaitInvocation(t, func() bool { store.mu.Lock(); defer store.mu.Unlock(); return len(store.hints) == 0 })
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.streams) != 0 {
		t.Fatal("retained zero-viewer stream")
	}
}
func TestInvocationStreamRecoversHintDuringReadAndListenerReset(t *testing.T) {
	store := newFakeInvocationStore()
	store.onRead = func() { store.append("during read"); store.publish() }
	s, stop := newInvocationStreams(store).acquire("owner", "invocation", 0)
	defer stop()
	awaitInvocation(t, func() bool { return streamCaughtUp(s, 1) })
	store.append("missed while disconnected")
	store.publish() // reconnect hint
	awaitInvocation(t, func() bool { return streamCaughtUp(s, 2) })
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.cursors[len(store.cursors)-1] != 1 {
		t.Fatal("reloaded history", store.cursors)
	}
}
func TestInvocationStreamBoundsWindowAndReplaysSlowReader(t *testing.T) {
	store := newFakeInvocationStore()
	for i := 0; i < 80; i++ {
		store.append(strings.Repeat("x", 5000))
	}
	store.state = "completed"
	s, stop := newInvocationStreams(store).acquire("owner", "invocation", 0)
	defer stop()
	awaitInvocation(t, func() bool { return streamCaughtUp(s, 80) })
	s.mu.Lock()
	if s.bytes > invocationWindowBytes || s.base == 0 {
		t.Error("unbounded replay window", s.bytes, s.base)
	}
	s.mu.Unlock()
	cursor := int64(0)
	for cursor < 80 {
		page, err := s.read(context.Background(), cursor)
		if err != nil || len(page.events) == 0 {
			t.Fatal(cursor, err)
		}
		for _, e := range page.events {
			if e.sequence != cursor+1 {
				t.Fatal("history skipped", cursor, e.sequence)
			}
			cursor = e.sequence
		}
	}
	page, err := s.read(context.Background(), cursor)
	if err != nil || page.state != "completed" || cursor != page.head {
		t.Fatal(page, err)
	}
}
func TestInvocationStreamOversizedEventAndOwnerIsolation(t *testing.T) {
	store := newFakeInvocationStore()
	store.append(strings.Repeat("large", invocationWindowBytes))
	h := newInvocationStreams(store)
	s, stop := h.acquire("owner", "invocation", 0)
	defer stop()
	awaitInvocation(t, func() bool { return streamCaughtUp(s, 1) })
	s.mu.Lock()
	if s.bytes != 0 {
		t.Error("oversized event retained", s.bytes)
	}
	s.mu.Unlock()
	page, err := s.read(context.Background(), 0)
	if err != nil || len(page.events) != 1 {
		t.Fatal("oversized event not replayed", err)
	}
	other, stopOther := h.acquire("other", "invocation", 0)
	defer stopOther()
	awaitInvocation(t, func() bool { other.mu.Lock(); defer other.mu.Unlock(); return other.err != nil })
	if _, err := other.read(context.Background(), 0); err == nil {
		t.Fatal("another account read cached events")
	}
}
func TestInvocationSSEKeepalivesDoNotReadDatabaseAndTerminalReplayIsComplete(t *testing.T) {
	store := newFakeInvocationStore()
	h := newInvocationStreams(store)
	s, stop := h.acquire("owner", "invocation", 0)
	defer stop()
	awaitInvocation(t, func() bool { return streamCaughtUp(s, 0) })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	w := &invocationSSEWriter{ResponseRecorder: httptest.NewRecorder()}
	serveInvocationStream(ctx, w, s, 0, 10*time.Millisecond)
	store.mu.Lock()
	reads := store.reads
	store.mu.Unlock()
	if reads != 1 || strings.Count(w.Body.String(), "keep-alive") < 2 {
		t.Fatal("keepalive read state or failed to stream", reads, w.Body.String())
	}
	for i := 0; i < 25; i++ {
		store.append(fmt.Sprint(i))
	}
	store.mu.Lock()
	store.state = "completed"
	store.mu.Unlock()
	store.publish()
	w = &invocationSSEWriter{ResponseRecorder: httptest.NewRecorder()}
	finished, cancelDone := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelDone()
	serveInvocationStream(finished, w, s, 0, 10*time.Millisecond)
	if strings.Count(w.Body.String(), "\ndata: ") != 25 {
		t.Fatal("terminal state truncated replay", w.Body.String())
	}
}
func TestInvocationStreamResumesCursorAndExpiresWithoutPolling(t *testing.T) {
	store := newFakeInvocationStore()
	for i := 0; i < 10; i++ {
		store.append("message")
	}
	store.expiry = time.Now().Add(120 * time.Millisecond)
	s, stop := newInvocationStreams(store).acquire("owner", "invocation", 8)
	defer stop()
	awaitInvocation(t, func() bool { return streamCaughtUp(s, 10) })
	page, err := s.read(context.Background(), 8)
	if err != nil || len(page.events) != 2 {
		t.Fatal(page, err)
	}
	awaitInvocation(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.err != nil })
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.reads != 1 || store.cursors[0] != 8 {
		t.Fatal("resume/expiry polled", store.cursors)
	}
}

func TestInvocationStreamReconnectReplacesFailedHubWithoutOldReleaseEvictingIt(t *testing.T) {
	store := newFakeInvocationStore()
	store.err = fmt.Errorf("database unavailable")
	h := newInvocationStreams(store)
	old, stopOld := h.acquire("owner", "invocation", 0)
	defer stopOld()
	awaitInvocation(t, func() bool { old.mu.Lock(); defer old.mu.Unlock(); return old.err != nil })
	store.mu.Lock()
	store.err = nil
	store.mu.Unlock()
	fresh, stopFresh := h.acquire("owner", "invocation", 0)
	defer stopFresh()
	if old == fresh {
		t.Fatal("reconnect reused failed stream")
	}
	awaitInvocation(t, func() bool { return streamCaughtUp(fresh, 0) })
	stopOld()
	h.mu.Lock()
	if h.streams["owner\x00invocation"] != fresh {
		t.Error("old release evicted replacement")
	}
	h.mu.Unlock()
}
func TestInvocationStreamReadCostDoesNotMultiplyWithLiveViewers(t *testing.T) {
	store := newFakeInvocationStore()
	h := newInvocationStreams(store)
	viewers := make([]*invocationStream, 20)
	for i := range viewers {
		s, stop := h.acquire("owner", "invocation", 0)
		viewers[i] = s
		defer stop()
	}
	awaitInvocation(t, func() bool { return streamCaughtUp(viewers[0], 0) })
	for i := int64(1); i <= 100; i++ {
		store.append("delta")
		store.publish()
		awaitInvocation(t, func() bool { return streamCaughtUp(viewers[0], i) })
		for _, s := range viewers {
			page, err := s.read(context.Background(), i-1)
			if err != nil || len(page.events) != 1 {
				t.Fatal("live read", i, err)
			}
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.reads != 101 || store.subscriptions != 1 {
		t.Fatal("per-viewer read amplification", store.reads, store.subscriptions)
	}
	for i, cursor := range store.cursors {
		if cursor != int64(max(i-1, 0)) {
			t.Fatal("nonincremental read", i, cursor)
		}
	}
}
