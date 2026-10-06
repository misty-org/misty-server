package db

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/lib/pq"
)

// One additional LISTEN connection per API process, shared by every worker.
// Coalesced hints carry a bounded queue name or a resource-key digest, never
// raw resource names, task payloads or account data.
type workerEventHub struct {
	mu          sync.Mutex
	listener    *pq.Listener
	subscribers map[string]map[chan struct{}]struct{}
	closed      bool
}

func workerQueue(kind string) bool {
	switch kind {
	case "library-ai", "library-edit", "library-faces", "note-control", "drawing-control", "drawing-purge", "embedding", "billing", "abuse-blocks", "abuse-retention", "agent-runtime", "agent-tasks", "scheduled", "ai-cleanup", "account-deletion", "rendition-reservations":
		return true
	}
	return false
}

type workerDialer struct{}

func (workerDialer) Dial(network, address string) (net.Conn, error) {
	return workerDialer{}.DialTimeout(network, address, 5*time.Second)
}
func (workerDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	// TCP probes detect a dead path without issuing recurring SQL state queries.
	d := net.Dialer{Timeout: timeout, KeepAliveConfig: net.KeepAliveConfig{
		Enable: true, Idle: 30 * time.Second, Interval: 10 * time.Second, Count: 3,
	}}
	return d.Dial(network, address)
}

func (db *Database) SubscribeWorkerEvents(ctx context.Context, kind string) (<-chan struct{}, func(), error) {
	if !workerQueue(kind) {
		return nil, nil, errors.New("unknown worker queue")
	}
	return db.subscribeWorkerTopic(ctx, kind)
}

func (db *Database) subscribeWorkerTopic(ctx context.Context, kind string) (<-chan struct{}, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	db.workersMu.Lock()
	defer db.workersMu.Unlock()
	if db.workers != nil {
		db.workers.mu.Lock()
		closed := db.workers.closed
		db.workers.mu.Unlock()
		if closed {
			db.workers = nil
		}
	}
	if db.workers == nil {
		listener := pq.NewDialListener(workerDialer{}, db.GetDSN()+" connect_timeout=5 application_name=misty-worker-listener", time.Second, time.Minute, nil)
		ready := make(chan error, 1)
		go func() { ready <- listener.Listen("misty_worker_events") }()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		var err error
		select {
		case err = <-ready:
		case <-ctx.Done():
			err = ctx.Err()
		case <-timer.C:
			err = errors.New("worker event service unavailable")
		}
		if err != nil {
			_ = listener.Close()
			return nil, nil, err
		}
		hub := &workerEventHub{listener: listener, subscribers: map[string]map[chan struct{}]struct{}{}}
		db.workers = hub
		go hub.listen()
	}
	hub := db.workers
	ch := make(chan struct{}, 1)
	hub.mu.Lock()
	if hub.closed {
		hub.mu.Unlock()
		return nil, nil, errors.New("worker event service closed")
	}
	if hub.subscribers[kind] == nil {
		hub.subscribers[kind] = map[chan struct{}]struct{}{}
	}
	hub.subscribers[kind][ch] = struct{}{}
	hub.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			hub.mu.Lock()
			defer hub.mu.Unlock()
			delete(hub.subscribers[kind], ch)
			if len(hub.subscribers[kind]) == 0 {
				delete(hub.subscribers, kind)
			}
		})
	}, nil
}

func (h *workerEventHub) listen() {
	defer func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.closed = true
		for _, subs := range h.subscribers {
			for ch := range subs {
				close(ch)
			}
		}
	}()
	for notification := range h.listener.Notify {
		if notification == nil {
			h.publish("")
		} else if workerQueue(notification.Extra) || resourceLeaseTopic(notification.Extra) || invocationEventTopic(notification.Extra) {
			h.publish(notification.Extra)
		}
	}
}
func (h *workerEventHub) publish(kind string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	if kind != "" {
		deliverWorkerHint(h.subscribers[kind])
		return
	}
	for _, subs := range h.subscribers {
		deliverWorkerHint(subs)
	}
}
func deliverWorkerHint(subs map[chan struct{}]struct{}) {
	for ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
