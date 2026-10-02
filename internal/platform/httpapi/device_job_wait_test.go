package api

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type fakeJobWaitStore struct {
	events     chan db.AccountEvent
	subscribed bool
	stopped    bool
	read       func() (*db.WorkflowDeviceNodeJob, error)
}

func (s *fakeJobWaitStore) SubscribeAccountEvents(context.Context, string) (<-chan db.AccountEvent, func(), error) {
	s.subscribed = true
	return s.events, func() { s.stopped = true }, nil
}
func (s *fakeJobWaitStore) WorkflowDeviceNodeJob(context.Context, string, string) (*db.WorkflowDeviceNodeJob, error) {
	return s.read()
}

func TestDeviceJobWaitSubscribesBeforeReadAndRecoversReset(t *testing.T) {
	s := &fakeJobWaitStore{events: make(chan db.AccountEvent, 4)}
	calls := 0
	s.read = func() (*db.WorkflowDeviceNodeJob, error) {
		if !s.subscribed {
			t.Fatal("read before subscribe loses completions")
		}
		calls++
		if calls == 1 {
			s.events <- db.AccountEvent{Topic: "job-state", ID: "unrelated"}
			s.events <- db.AccountEvent{Topic: "reset"} // Completion hint lost during a reconnect.
			return &db.WorkflowDeviceNodeJob{State: "executing"}, nil
		}
		return &db.WorkflowDeviceNodeJob{State: "completed"}, nil
	}
	job, err := waitForDeviceJob(context.Background(), s, "owner", "job", time.Now().Add(time.Second))
	if err != nil || job.State != "completed" || calls != 2 || !s.stopped {
		t.Fatal(job, err, calls, s.stopped)
	}
}

func TestDeviceJobWaitDoesNotPollAndWakesOnlyForItsJob(t *testing.T) {
	s := &fakeJobWaitStore{events: make(chan db.AccountEvent, 4)}
	var reads atomic.Int32
	var completed atomic.Bool
	ready := make(chan struct{})
	s.read = func() (*db.WorkflowDeviceNodeJob, error) {
		if reads.Add(1) == 1 {
			close(ready)
		}
		if completed.Load() {
			return &db.WorkflowDeviceNodeJob{State: "completed"}, nil
		}
		return &db.WorkflowDeviceNodeJob{State: "executing"}, nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := waitForDeviceJob(context.Background(), s, "owner", "job", time.Now().Add(2*time.Second))
		done <- err
	}()
	<-ready
	s.events <- db.AccountEvent{Topic: "job-state", ID: "different"}
	s.events <- db.AccountEvent{Topic: "settings-profiles"}
	time.Sleep(300 * time.Millisecond)
	if reads.Load() != 1 {
		t.Fatalf("idle/unrelated event performed %d reads", reads.Load())
	}
	completed.Store(true)
	s.events <- db.AccountEvent{Topic: "job-state", ID: "job"}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 2 || !s.stopped {
		t.Fatal("completion did not wake or unsubscribe")
	}
}

func TestDeviceJobWaitDeadlineReadsFinalState(t *testing.T) {
	for _, completed := range []bool{false, true} {
		s := &fakeJobWaitStore{events: make(chan db.AccountEvent)}
		reads := 0
		s.read = func() (*db.WorkflowDeviceNodeJob, error) {
			reads++
			state := "executing"
			if reads == 2 && completed {
				state = "completed"
			}
			return &db.WorkflowDeviceNodeJob{State: state}, nil
		}
		job, err := waitForDeviceJob(context.Background(), s, "owner", "job", time.Now().Add(-time.Second))
		if completed && (err != nil || job.State != "completed") {
			t.Fatal(job, err)
		}
		if !completed && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		if !s.stopped {
			t.Fatal("subscription leaked")
		}
	}
}

func TestDeviceJobWaitCancellation(t *testing.T) {
	s := &fakeJobWaitStore{events: make(chan db.AccountEvent)}
	s.read = func() (*db.WorkflowDeviceNodeJob, error) { return &db.WorkflowDeviceNodeJob{State: "executing"}, nil }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := waitForDeviceJob(ctx, s, "owner", "job", time.Now().Add(time.Hour))
	if !errors.Is(err, context.Canceled) || !s.stopped {
		t.Fatal(err)
	}
}
