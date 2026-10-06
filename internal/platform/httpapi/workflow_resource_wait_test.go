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
