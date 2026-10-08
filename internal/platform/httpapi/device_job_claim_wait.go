package api

import (
	"context"
	"errors"
	"sync"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// A device that is running an agent's browser work holds its next claim open
// for a short while (docs/plans/agentic-browser.md, Phase 2: faster device
// hop). The next job of the run is handed over the moment it is queued,
// instead of after a notification reaches the device and it asks again.
// The claim, its lease and every check are the ordinary queue's; when the
// held claim ends empty or drops, the device falls back to notifications.
const maxDeviceClaimWait = 25 * time.Second

type deviceClaimStore interface {
	SubscribeAccountEvents(context.Context, string) (<-chan db.AccountEvent, func(), error)
	ClaimWorkflowDeviceNodeJob(userID, deviceID string, lease time.Duration, protocol ...int) (*db.WorkflowDeviceNodeJob, string, error)
}

// deviceClaimWaits allows one held claim per device; any other claim from the
// same device answers at once.
type deviceClaimWaits struct {
	mu      sync.Mutex
	holding map[string]bool
}

func (w *deviceClaimWaits) acquire(deviceID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.holding == nil {
		w.holding = map[string]bool{}
	}
	if w.holding[deviceID] {
		return false
	}
	w.holding[deviceID] = true
	return true
}

func (w *deviceClaimWaits) release(deviceID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.holding, deviceID)
}

// claimDeviceJobWithin claims the device's next job, waiting up to wait for
// one to be queued. It returns db.ErrAgentJobNotFound when none arrived and
// the context's error when the device went away.
func claimDeviceJobWithin(ctx context.Context, store deviceClaimStore, userID, deviceID string, protocol int, wait time.Duration) (*db.WorkflowDeviceNodeJob, string, error) {
	claim := func() (*db.WorkflowDeviceNodeJob, string, error) {
		return store.ClaimWorkflowDeviceNodeJob(userID, deviceID, time.Minute, protocol)
	}
	if wait <= 0 {
		return claim()
	}
	// Subscribe before the first claim so a job queued in between still wakes it.
	events, stop, err := store.SubscribeAccountEvents(ctx, userID)
	if err != nil {
		return claim()
	}
	defer stop()
	if wait > maxDeviceClaimWait {
		wait = maxDeviceClaimWait
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		// A device that hung up must not be handed a job it will never run.
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		job, token, err := claim()
		if !errors.Is(err, db.ErrAgentJobNotFound) {
			return job, token, err
		}
	wait:
		for {
			select {
			case <-ctx.Done():
				return nil, "", ctx.Err()
			case <-timer.C:
				return nil, "", db.ErrAgentJobNotFound
			case event, open := <-events:
				if !open {
					return nil, "", db.ErrAgentJobNotFound
				}
				if event.Topic == "jobs" || event.Topic == "reset" {
					break wait
				}
			}
		}
	}
}
