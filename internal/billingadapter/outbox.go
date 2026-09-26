package billingadapter

import (
	"context"
	"errors"
	"time"
)

// Store durably records a request before delivery and verifies that an existing
// key has the identical payload. Pending returns only due, undelivered entries.
// Concurrent workers are safe because the remote adapter must be idempotent.
type Store interface {
	Enqueue(context.Context, Entry) error
	Pending(context.Context, int) ([]Entry, error)
	Delivered(context.Context, string) error
	Retry(context.Context, string, time.Time) error
}
type Entry struct {
	ID      string
	Action  string
	Request Request
}
type Reliable struct {
	Adapter Adapter
	Store   Store
}

func (r Reliable) Submit(ctx context.Context, action string, req Request) error {
	if action != "settle" && action != "settle_group" && action != "release_group" && action != "release" && action != "refund" && action != "close" {
		return ErrInvalid
	}
	if err := validate(action, req); err != nil {
		return err
	}
	if !r.Adapter.Enabled() {
		return nil
	}
	if r.Store == nil {
		return errors.New("billing outbox is required")
	}
	entry := Entry{ID: keyID(req.AccountID, req.Key), Action: action, Request: req}
	if err := r.Store.Enqueue(ctx, entry); err != nil {
		return err
	}
	// Durable acceptance is sufficient; an outage must not lose completed usage
	// or prevent cancellation. A worker retries with the same transition key.
	_, err := r.Adapter.Do(ctx, action, req)
	if err != nil {
		return nil
	}
	return r.Store.Delivered(ctx, entry.ID)
}
func (r Reliable) Flush(ctx context.Context, limit int) error {
	if !r.Adapter.Enabled() {
		return nil
	}
	if r.Store == nil {
		return errors.New("billing outbox is required")
	}
	entries, err := r.Store.Pending(ctx, limit)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err = r.Adapter.Do(ctx, e.Action, e.Request); err != nil {
			if err = r.Store.Retry(ctx, e.ID, time.Now().Add(time.Minute)); err != nil {
				return err
			}
		} else if err = r.Store.Delivered(ctx, e.ID); err != nil {
			return err
		}
	}
	return nil
}
