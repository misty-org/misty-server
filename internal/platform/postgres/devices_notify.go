package db

import (
	"context"
	"errors"
	"time"

	"github.com/lib/pq"
)

// ListenNotifications streams one Postgres NOTIFY channel's payloads until ctx
// ends or the listener fails; callers reconnect. The device hub uses it to
// reach sockets held by other API processes.
func (db *Database) ListenNotifications(ctx context.Context, channel string) (<-chan string, func(), error) {
	listener := pq.NewListener(db.GetDSN()+" connect_timeout=5", time.Second, time.Minute, nil)
	ready := make(chan error, 1)
	go func() { ready <- listener.Listen(channel) }()
	select {
	case err := <-ready:
		if err != nil {
			_ = listener.Close()
			return nil, nil, err
		}
	case <-ctx.Done():
		_ = listener.Close()
		return nil, nil, ctx.Err()
	case <-time.After(5 * time.Second):
		_ = listener.Close()
		return nil, nil, errors.New("notification listener unavailable")
	}
	out := make(chan string, 64)
	done := make(chan struct{})
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case notification, ok := <-listener.Notify:
				if !ok {
					return
				}
				if notification == nil {
					continue
				}
				select {
				case out <- notification.Extra:
				default:
					// A slow reader drops hints; presence is re-announced every 45s.
				}
			}
		}
	}()
	stopped := false
	return out, func() {
		if !stopped {
			stopped = true
			close(done)
			_ = listener.Close()
		}
	}, nil
}
