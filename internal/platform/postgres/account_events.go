package db

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/kannachi323/misty/server/internal/platform/transport"
	"sync"
	"time"

	"github.com/lib/pq"
)

// AccountEvent is an invalidation, never a grant or an effect receipt. Clients
// reconcile durable, authorized snapshots on initial connect and reconnect.
type AccountEvent = transport.AccountEvent

type accountEventHub struct {
	mu       sync.Mutex
	listener *pq.Listener
	subs     map[chan AccountEvent]string
}

func (h *accountEventHub) publish(event AccountEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch, user := range h.subs {
		if event.UserID != "" && event.UserID != user {
			continue
		}
		select {
		case ch <- event:
		default:
			// A slow consumer gets a full reconciliation instead of silently
			// losing the one notification that completed its task.
		drain:
			for {
				select {
				case <-ch:
				default:
					break drain
				}
			}
			ch <- AccountEvent{Topic: "reset"}
		}
	}
}

func (db *Database) SubscribeAccountEvents(ctx context.Context, userID string) (<-chan AccountEvent, func(), error) {
	if userID == "" {
		return nil, nil, errors.New("account required")
	}
	db.eventsMu.Lock()
	defer db.eventsMu.Unlock()
	if db.events == nil {
		listener := pq.NewListener(db.GetDSN()+" connect_timeout=5", time.Second, time.Minute, nil)
		ready := make(chan error, 1)
		go func() { ready <- listener.Listen("misty_account_events") }()
		var err error
		select {
		case err = <-ready:
		case <-ctx.Done():
			err = ctx.Err()
		case <-time.After(5 * time.Second):
			err = errors.New("event service unavailable")
		}
		if err != nil {
			_ = listener.Close()
			return nil, nil, err
		}
		hub := &accountEventHub{listener: listener, subs: make(map[chan AccountEvent]string)}
		db.events = hub
		go func() {
			for notification := range listener.Notify {
				if notification == nil {
					hub.publish(AccountEvent{Topic: "reset"})
					continue
				}
				var event AccountEvent
				if json.Unmarshal([]byte(notification.Extra), &event) == nil && event.UserID != "" {
					hub.publish(event)
				}
			}
		}()
	}
	hub := db.events
	ch := make(chan AccountEvent, 32)
	hub.mu.Lock()
	hub.subs[ch] = userID
	ch <- AccountEvent{Topic: "reset"}
	hub.mu.Unlock()
	return ch, func() { hub.mu.Lock(); delete(hub.subs, ch); hub.mu.Unlock() }, nil
}
