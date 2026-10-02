package api

import (
	"context"
	"errors"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type deviceJobWaitStore interface {
	SubscribeAccountEvents(context.Context, string) (<-chan db.AccountEvent, func(), error)
	WorkflowDeviceNodeJob(context.Context, string, string) (*db.WorkflowDeviceNodeJob, error)
}

// Subscribe before reading so a completion between setup and the first read
// cannot be missed. Listener reconnect/overflow sends reset; durable state is
// authoritative. The only timer is the job's actual deadline, never a poll.
func waitForDeviceJob(ctx context.Context, store deviceJobWaitStore, userID, jobID string, deadline time.Time) (*db.WorkflowDeviceNodeJob, error) {
	events, stop, err := store.SubscribeAccountEvents(ctx, userID)
	if err != nil {
		return nil, err
	}
	defer stop()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	expired := false
	for {
		job, err := store.WorkflowDeviceNodeJob(ctx, userID, jobID)
		if err != nil {
			return nil, err
		}
		switch job.State {
		case "completed", "failed", "canceled", "uncertain":
			return job, nil
		}
		if expired {
			return nil, context.DeadlineExceeded
		}
	wait:
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timer.C:
				expired = true // One final authoritative read resolves a completion at the deadline.
				break wait
			case event, open := <-events:
				if !open {
					return nil, errors.New("device job event stream closed")
				}
				if event.Topic == "reset" || (event.Topic == "job-state" && event.ID == jobID) {
					break wait
				}
			}
		}
	}
}
