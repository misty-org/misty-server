package api

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type resourceWaitFake struct {
	hints      chan struct{}
	subscribed atomic.Bool
	stopped    atomic.Bool
	claims     atomic.Int32
	claim      func(int32) (bool, time.Duration, error)
}

func (s *resourceWaitFake) SubscribeWorkflowResourceLeaseEvents(context.Context, string) (<-chan struct{}, func(), error) {
	s.subscribed.Store(true)
	return s.hints, func() { s.stopped.Store(true) }, nil
}
func (s *resourceWaitFake) TryWorkflowResourceLease(context.Context, string, string, string, string, time.Duration) (bool, time.Duration, error) {
	if !s.subscribed.Load() {
		return false, 0, errors.New("claim preceded subscription")
	}
	return s.claim(s.claims.Add(1))
}
func resourceResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("wait did not finish")
		return nil
	}
}
func runResourceWait(ctx context.Context, s *resourceWaitFake) <-chan error {
	done := make(chan error, 1)
	go func() { done <- waitForWorkflowResource(ctx, s, "run", "node", "key", "fingerprint", time.Minute) }()
	return done
}
func TestResourceWaitDoesNotPollAndAcquiresOnRelease(t *testing.T) {
	s := &resourceWaitFake{hints: make(chan struct{}, 1)}
	first := make(chan struct{})
	s.claim = func(n int32) (bool, time.Duration, error) {
		if n == 1 {
			close(first)
			return false, time.Hour, nil
		}
		return true, 0, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runResourceWait(ctx, s)
	<-first
	time.Sleep(100 * time.Millisecond)
	if s.claims.Load() != 1 {
		t.Fatal("held lease polled", s.claims.Load())
	}
	s.hints <- struct{}{}
	if err := resourceResult(t, done); err != nil {
		t.Fatal(err)
	}
	if !s.stopped.Load() {
		t.Fatal("subscription leaked")
	}
}
func TestResourceReleaseDuringClaimIsNotLost(t *testing.T) {
	s := &resourceWaitFake{hints: make(chan struct{}, 1)}
	s.claim = func(n int32) (bool, time.Duration, error) {
		if n == 1 {
			s.hints <- struct{}{}
			return false, time.Hour, nil
		}
		return true, 0, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := resourceResult(t, runResourceWait(ctx, s)); err != nil {
		t.Fatal(err)
	}
}
func TestResourceWaitUsesActualExpiryAndStopsOnCancellation(t *testing.T) {
	s := &resourceWaitFake{hints: make(chan struct{}, 1)}
	s.claim = func(n int32) (bool, time.Duration, error) { return n > 1, 100 * time.Millisecond, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runResourceWait(ctx, s)
	select {
	case <-done:
		t.Fatal("lease acquired before expiry")
	case <-time.After(25 * time.Millisecond):
	}
	if err := resourceResult(t, done); err != nil {
		t.Fatal(err)
	}
	s = &resourceWaitFake{hints: make(chan struct{}, 1)}
	first := make(chan struct{})
	s.claim = func(int32) (bool, time.Duration, error) { close(first); return false, time.Hour, nil }
	done = runResourceWait(ctx, s)
	<-first
	cancel()
	if err := resourceResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s.claims.Load() != 1 || !s.stopped.Load() {
		t.Fatal("canceled waiter retried or leaked")
	}
}
func TestResourceWaitFailureNeverReportsOwnership(t *testing.T) {
	for _, closeStream := range []bool{true, false} {
		s := &resourceWaitFake{hints: make(chan struct{}, 1)}
		s.claim = func(int32) (bool, time.Duration, error) {
			if closeStream {
				close(s.hints)
				return false, time.Hour, nil
			}
			return false, 0, errors.New("database unavailable")
		}
		ctx, cancel := context.WithCancel(context.Background())
		err := resourceResult(t, runResourceWait(ctx, s))
		cancel()
		if err == nil {
			t.Fatal("wait failure reported ownership")
		}
	}
}
