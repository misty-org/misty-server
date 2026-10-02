package api

import (
	"context"
	"errors"
	"time"
)

type workflowResourceWaitStore interface {
	SubscribeWorkflowResourceLeaseEvents(context.Context, string) (<-chan struct{}, func(), error)
	TryWorkflowResourceLease(context.Context, string, string, string, string, time.Duration) (bool, time.Duration, error)
}

// Only the actual lease deadline or a hint for this resource causes another
// claim. Subscribe before the first attempt so an early release cannot be lost.
func waitForWorkflowResource(ctx context.Context, store workflowResourceWaitStore, runID, nodeID, key, fingerprint string, duration time.Duration) error {
	hints, stop, err := store.SubscribeWorkflowResourceLeaseEvents(ctx, key)
	if err != nil {
		return err
	}
	defer stop()
	for ctx.Err() == nil {
		// Discard hints already covered by the authoritative claim below; a hint
		// arriving during the transaction remains buffered for the following wait.
		select {
		case _, open := <-hints:
			if !open {
				return errors.New("resource lease event stream closed")
			}
		default:
		}
		acquired, delay, err := store.TryWorkflowResourceLease(ctx, runID, nodeID, key, fingerprint, duration)
		if err != nil {
			return err
		}
		if acquired {
			return nil
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case _, open := <-hints:
			timer.Stop()
			if !open {
				return errors.New("resource lease event stream closed")
			}
		case <-timer.C:
		}
	}
	return ctx.Err()
}
