package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/metrics"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const invocationWindowBytes = 256 << 10

type invocationStreamStore interface {
	SubscribeAIInvocationEvents(context.Context, string) (<-chan struct{}, func(), error)
	ReadAIInvocationEventPage(context.Context, string, string, int64) (db.AIInvocationEventPage, error)
}
type invocationStreams struct {
	mu      sync.Mutex
	store   invocationStreamStore
	metrics *metrics.Registry
	streams map[string]*invocationStream
}
type invocationStream struct {
	metrics            *metrics.Registry
	mu                 sync.Mutex
	store              invocationStreamStore
	user, id           string
	refs               int // protected by the parent map's lock
	cancel             context.CancelFunc
	notify             chan struct{}
	known              bool
	err                error
	state              string
	head, cursor, base int64
	expires            time.Time
	events             []invocationStreamEvent
	bytes              int
}
type invocationStreamEvent struct {
	sequence int64
	payload  json.RawMessage
	frame    []byte
}
type invocationStreamPage struct {
	events       []invocationStreamEvent
	state        string
	head, cursor int64
	ready        bool
	notify       <-chan struct{}
}

func newInvocationStreams(store invocationStreamStore) *invocationStreams {
	return &invocationStreams{store: store, streams: map[string]*invocationStream{}}
}
func (h *invocationStreams) acquire(user, id string, after int64) (*invocationStream, func()) {
	key := user + "\x00" + id
	h.mu.Lock()
	s := h.streams[key]
	if s != nil {
		s.mu.Lock()
		failed := s.err != nil
		s.mu.Unlock()
		if failed {
			s = nil
		}
	}
	if s == nil {
		ctx, cancel := context.WithCancel(context.Background())
		s = &invocationStream{metrics: h.metrics, store: h.store, user: user, id: id, cancel: cancel, notify: make(chan struct{}), cursor: after, base: after}
		h.streams[key] = s
		s.metrics.AddAIStreamWindow(1)
		go s.run(ctx)
	}
	s.refs++
	h.mu.Unlock()
	var once sync.Once
	return s, func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			s.refs--
			if s.refs == 0 {
				if h.streams[key] == s {
					delete(h.streams, key)
				}
				s.cancel()
				s.metrics.AddAIStreamWindow(-1)
			}
		})
	}
}
func (s *invocationStream) changedLocked() { close(s.notify); s.notify = make(chan struct{}) }
func (s *invocationStream) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
	s.changedLocked()
}
func encodeInvocationStreamEvent(event db.AIInvocationEventRecord) invocationStreamEvent {
	frame := fmt.Appendf(nil, "id: %d\ndata: ", event.Sequence)
	start := len(frame)
	frame = append(frame, event.Payload...)
	end := len(frame)
	frame = append(frame, '\n', '\n')
	return invocationStreamEvent{sequence: event.Sequence, payload: frame[start:end], frame: frame}
}
func (s *invocationStream) accept(page db.AIInvocationEventPage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := !s.known || s.state != page.State || s.head != page.Head || !s.expires.Equal(page.ExpiresAt)
	s.known = true
	s.state = page.State
	s.head = page.Head
	s.expires = page.ExpiresAt
	if s.cursor > page.Head {
		s.events = nil
		s.bytes = 0
		s.cursor = page.Head
		s.base = page.Head
	}
	for _, event := range page.Events {
		if event.Sequence <= s.cursor {
			continue
		}
		encoded := encodeInvocationStreamEvent(event)
		s.cursor = event.Sequence
		s.events = append(s.events, encoded)
		s.bytes += len(encoded.frame)
		changed = true
		for s.bytes > invocationWindowBytes || len(s.events) > db.AIInvocationPageEvents {
			old := s.events[0]
			s.base = old.sequence
			s.bytes -= len(old.frame)
			s.events[0] = invocationStreamEvent{}
			s.events = s.events[1:]
		}
	}
	if changed {
		s.changedLocked()
	}
}
func (s *invocationStream) run(ctx context.Context) {
	hints, stop, err := s.store.SubscribeAIInvocationEvents(ctx, s.id)
	if err != nil {
		s.fail(err)
		return
	}
	defer stop()
	for ctx.Err() == nil {
		select {
		case _, open := <-hints:
			if !open {
				s.fail(errors.New("invocation event subscription closed"))
				return
			}
		default:
		}
		s.mu.Lock()
		cursor := s.cursor
		s.mu.Unlock()
		read, cancel := context.WithTimeout(ctx, 10*time.Second)
		page, err := s.readPage(read, cursor, "live")
		cancel()
		if err != nil {
			s.fail(err)
			return
		}
		s.accept(page)
		s.mu.Lock()
		cursor = s.cursor
		s.mu.Unlock()
		if cursor < page.Head {
			if len(page.Events) == 0 {
				s.fail(errors.New("invocation history gap"))
				return
			}
			continue // Drain bounded pages before waiting for another change.
		}
		if aiInvocationTerminal(page.State) {
			return
		}
		timer := time.NewTimer(time.Until(page.ExpiresAt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case _, open := <-hints:
			timer.Stop()
			if !open {
				s.fail(errors.New("invocation event subscription closed"))
				return
			}
		case <-timer.C:
			s.fail(db.ErrSpaceNotFound)
			return
		}
	}
}
func (s *invocationStream) read(ctx context.Context, after int64) (invocationStreamPage, error) {
	s.mu.Lock()
	out := invocationStreamPage{state: s.state, head: s.head, cursor: after, ready: s.known, notify: s.notify}
	if s.err != nil {
		err := s.err
		s.mu.Unlock()
		return out, err
	}
	if !s.known {
		s.mu.Unlock()
		return out, nil
	}
	if time.Now().After(s.expires) {
		s.mu.Unlock()
		return out, db.ErrSpaceNotFound
	}
	// Do not clamp to this hub's head: it can lag a cursor issued by another
	// instance, and clamping would resend events the viewer already has.
	out.cursor = max(after, 0)
	if out.cursor >= s.base {
		for _, event := range s.events {
			if event.sequence > out.cursor {
				out.events = append(out.events, event)
			}
		}
		s.mu.Unlock()
		return out, nil
	}
	s.mu.Unlock()
	// A slow/new viewer replays a bounded page; it never forces the shared live
	// cursor backwards or enlarges the window held for other viewers.
	read, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	page, err := s.readPage(read, out.cursor, "replay")
	if err != nil {
		return out, err
	}
	out.state = page.State
	out.head = page.Head
	for _, event := range page.Events {
		out.events = append(out.events, encodeInvocationStreamEvent(event))
	}
	return out, nil
}

func (h *invocationStreams) setMetrics(m *metrics.Registry) {
	h.mu.Lock()
	h.metrics = m
	h.mu.Unlock()
}
func (s *invocationStream) readPage(ctx context.Context, cursor int64, source string) (db.AIInvocationEventPage, error) {
	page, err := s.store.ReadAIInvocationEventPage(ctx, s.user, s.id, cursor)
	bytes := 0
	for _, event := range page.Events {
		bytes += len(event.Payload)
	}
	s.metrics.RecordAIStreamRead(source, len(page.Events), bytes, err == nil)
	return page, err
}
