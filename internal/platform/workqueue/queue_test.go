package workqueue

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func runTestQueue(t *testing.T, q Queue) (context.CancelFunc, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); q.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("worker did not stop")
		}
	})
	return cancel, done
}
func await(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not make progress")
	}
}
func TestIdleQueueDoesNotPollAndDrainsBoundedBatches(t *testing.T) {
	wake := make(chan struct{}, 1)
	var subscribed, stopped atomic.Bool
	var reads, items, processed atomic.Int32
	planned, finished := make(chan struct{}), make(chan struct{})
	q := Queue{
		Subscribe: func(context.Context) (<-chan struct{}, func(), error) {
			subscribed.Store(true)
			return wake, func() { stopped.Store(true) }, nil
		},
		Next: func(context.Context) (time.Duration, bool, error) {
			if !subscribed.Load() {
				t.Error("read before subscription")
			}
			if reads.Add(1) == 1 {
				close(planned)
			}
			return 0, items.Load() > 0, nil
		},
		Process: func(context.Context) (int, error) {
			n := min(items.Load(), 2)
			items.Add(-n)
			processed.Add(n)
			if items.Load() == 0 {
				close(finished)
			}
			return int(n), nil
		},
	}
	cancel, done := runTestQueue(t, q)
	await(t, planned)
	time.Sleep(100 * time.Millisecond)
	if reads.Load() != 1 {
		t.Fatal("empty queue polled", reads.Load())
	}
	items.Store(7)
	wake <- struct{}{}
	await(t, finished)
	if processed.Load() != 7 {
		t.Fatal("failed to drain more than one batch")
	}
	cancel()
	await(t, done)
	if !stopped.Load() {
		t.Fatal("subscription leaked")
	}
}
func TestNotificationDuringPlanIsNotLost(t *testing.T) {
	wake := make(chan struct{}, 1)
	finished := make(chan struct{})
	reads := 0
	q := Queue{
		Subscribe: func(context.Context) (<-chan struct{}, func(), error) { return wake, func() {}, nil },
		Next: func(context.Context) (time.Duration, bool, error) {
			reads++
			if reads == 1 {
				wake <- struct{}{}
				return 0, false, nil
			}
			return 0, reads == 2, nil
		},
		Process: func(context.Context) (int, error) { close(finished); return 1, nil },
	}
	runTestQueue(t, q)
	await(t, finished)
}
func TestDeadlineWakesWithoutNotification(t *testing.T) {
	wake := make(chan struct{}, 1)
	finished := make(chan struct{})
	deadline := time.Now().Add(100 * time.Millisecond)
	processed := false
	q := Queue{
		Subscribe: func(context.Context) (<-chan struct{}, func(), error) { return wake, func() {}, nil },
		Next:      func(context.Context) (time.Duration, bool, error) { return time.Until(deadline), !processed, nil },
		Process:   func(context.Context) (int, error) { processed = true; close(finished); return 1, nil },
	}
	runTestQueue(t, q)
	select {
	case <-finished:
		t.Fatal("ran before due time")
	case <-time.After(20 * time.Millisecond):
	}
	await(t, finished)
}
func TestErrorsBackOffDespiteNotificationStormAndCancelPromptly(t *testing.T) {
	wake := make(chan struct{}, 1)
	attempted := make(chan struct{})
	var reads atomic.Int32
	q := Queue{
		Subscribe: func(context.Context) (<-chan struct{}, func(), error) { return wake, func() {}, nil },
		Next: func(context.Context) (time.Duration, bool, error) {
			if reads.Add(1) == 1 {
				close(attempted)
			}
			return 0, false, errors.New("offline")
		},
	}
	cancel, done := runTestQueue(t, q)
	await(t, attempted)
	for i := 0; i < 100; i++ {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	time.Sleep(50 * time.Millisecond)
	if reads.Load() != 1 {
		t.Fatal("hints bypassed error backoff")
	}
	cancel()
	await(t, done)
}

func TestShutdownDoesNotReportQueryOrProcessCancellation(t *testing.T) {
	for _, phase := range []string{"query", "process"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			q := Queue{
				Subscribe: func(context.Context) (<-chan struct{}, func(), error) { return make(chan struct{}), func() {}, nil },
				Next: func(context.Context) (time.Duration, bool, error) {
					if phase == "query" {
						cancel()
						return 0, false, ctx.Err()
					}
					return 0, true, nil
				},
				Process: func(context.Context) (int, error) { cancel(); return 0, ctx.Err() },
				OnError: func(err error) { t.Errorf("shutdown reported an error: %v", err) },
			}
			q.Run(ctx)
		})
	}
}

func TestContendedReadyRowsBackOff(t *testing.T) {
	wake := make(chan struct{}, 1)
	attempted := make(chan struct{})
	var claims atomic.Int32
	q := Queue{
		Subscribe: func(context.Context) (<-chan struct{}, func(), error) { return wake, func() {}, nil },
		Next:      func(context.Context) (time.Duration, bool, error) { return 0, true, nil },
		Process: func(context.Context) (int, error) {
			if claims.Add(1) == 1 {
				close(attempted)
			}
			return 0, nil
		},
	}
	cancel, done := runTestQueue(t, q)
	await(t, attempted)
	wake <- struct{}{}
	time.Sleep(50 * time.Millisecond)
	if claims.Load() != 1 {
		t.Fatal("contended queue did not back off", claims.Load())
	}
	cancel()
	await(t, done)
}
