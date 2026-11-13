// Package workqueue runs durable queues from pushed hints and real deadlines.
package workqueue

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

var errSubscriptionClosed = errors.New("worker notification subscription closed")

type Queue struct {
	Subscribe func(context.Context) (<-chan struct{}, func(), error)
	// Next returns a database-clock-relative deadline, or pending=false when empty.
	Next    func(context.Context) (delay time.Duration, pending bool, err error)
	Process func(context.Context) (int, error)
	Observe func(reason string)
	OnError func(error)
}

func (q Queue) Run(ctx context.Context) {
	failures := 0
	for ctx.Err() == nil {
		wake, stop, err := q.Subscribe(ctx)
		if err == nil {
			failures = 0
			q.observe("startup")
			err = q.runSubscribed(ctx, wake)
			stop()
		}
		if ctx.Err() != nil {
			return
		}
		q.report(err)
		q.observe("error")
		failures++
		if !retryWait(ctx, failures) {
			return
		}
	}
}

func (q Queue) runSubscribed(ctx context.Context, wake <-chan struct{}) error {
	failures, emptyClaims := 0, 0
	for ctx.Err() == nil {
		// Collapse hints already covered by the following authoritative read. A hint
		// arriving during that read/process remains buffered for a trailing read.
		select {
		case _, open := <-wake:
			if !open {
				return errSubscriptionClosed
			}
		default:
		}
		delay, pending, err := q.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			q.report(err)
			failures++
			q.observe("error")
			if !retryWait(ctx, failures) {
				return ctx.Err()
			}
			continue
		}
		if !pending || delay > 0 {
			emptyClaims = 0
			failures = 0
			reason, err := wait(ctx, wake, delay, pending)
			if err != nil {
				return err
			}
			q.observe(reason)
			continue
		}
		if emptyClaims > 0 {
			// Another replica may hold a ready row's lock. Back off only while a
			// durable ready row exists; an empty queue never has a retry timer.
			q.observe("contention")
			if !retryWait(ctx, emptyClaims) {
				return ctx.Err()
			}
		}
		count, err := q.Process(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			q.report(err)
			failures++
			q.observe("error")
			if !retryWait(ctx, failures) {
				return ctx.Err()
			}
			continue
		}
		failures = 0
		if count == 0 {
			emptyClaims++
		} else {
			emptyClaims = 0
		}
	}
	return ctx.Err()
}

func wait(ctx context.Context, wake <-chan struct{}, delay time.Duration, pending bool) (string, error) {
	var deadline <-chan time.Time
	if pending {
		timer := time.NewTimer(max(0, delay))
		defer timer.Stop()
		deadline = timer.C
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case _, open := <-wake:
		if !open {
			return "", errSubscriptionClosed
		}
		return "notification", nil
	case <-deadline:
		return "deadline", nil
	}
}

func retryWait(ctx context.Context, failures int) bool {
	delay := time.Second * time.Duration(1<<min(max(failures-1, 0), 6))
	delay = min(delay, time.Minute)
	timer := time.NewTimer(delay + time.Duration(rand.Int64N(int64(delay/4))))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func (q Queue) observe(reason string) {
	if q.Observe != nil {
		q.Observe(reason)
	}
}
func (q Queue) report(err error) {
	if err != nil && q.OnError != nil {
		q.OnError(err)
	}
}
