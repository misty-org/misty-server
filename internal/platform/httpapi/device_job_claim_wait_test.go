package api

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/db"
)

type fakeClaimStore struct {
	mu     sync.Mutex
	queued []string
	claims int
	events chan db.AccountEvent
}

func (f *fakeClaimStore) SubscribeAccountEvents(context.Context, string) (<-chan db.AccountEvent, func(), error) {
	return f.events, func() {}, nil
}

func (f *fakeClaimStore) ClaimWorkflowDeviceNodeJob(_, _ string, _ time.Duration, _ ...int) (*db.WorkflowDeviceNodeJob, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims++
	if len(f.queued) == 0 {
		return nil, "", db.ErrAgentJobNotFound
	}
	id := f.queued[0]
	f.queued = f.queued[1:]
	return &db.WorkflowDeviceNodeJob{ID: id}, "token", nil
}

func (f *fakeClaimStore) queue(id string) {
	f.mu.Lock()
	f.queued = append(f.queued, id)
	f.mu.Unlock()
	f.events <- db.AccountEvent{Topic: "jobs"}
}

func TestHeldClaimTakesTheNextJobWhenItIsQueued(t *testing.T) {
	store := &fakeClaimStore{events: make(chan db.AccountEvent, 4)}
	go func() {
		time.Sleep(20 * time.Millisecond)
		store.events <- db.AccountEvent{Topic: "invocations"} // unrelated: keeps waiting
		store.queue("devicejob_next")
	}()
	started := time.Now()
	job, token, err := claimDeviceJobWithin(context.Background(), store, "user", "device", 2, 5*time.Second)
	if err != nil || job.ID != "devicejob_next" || token != "token" {
		t.Fatalf("job=%v token=%q err=%v", job, token, err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("the held claim must answer as soon as the job is queued")
	}
	if store.claims != 2 {
		t.Fatalf("claims = %d, want one before the wait and one on the jobs event", store.claims)
	}
}

func TestHeldClaimEndsEmptyAfterItsWait(t *testing.T) {
	store := &fakeClaimStore{events: make(chan db.AccountEvent)}
	_, _, err := claimDeviceJobWithin(context.Background(), store, "user", "device", 2, 30*time.Millisecond)
	if !errors.Is(err, db.ErrAgentJobNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestClaimWithoutWaitAnswersAtOnce(t *testing.T) {
	store := &fakeClaimStore{events: make(chan db.AccountEvent)}
	_, _, err := claimDeviceJobWithin(context.Background(), store, "user", "device", 2, 0)
	if !errors.Is(err, db.ErrAgentJobNotFound) || store.claims != 1 {
		t.Fatalf("err = %v claims = %d", err, store.claims)
	}
}

func TestHeldClaimStopsWhenTheDeviceHangsUp(t *testing.T) {
	store := &fakeClaimStore{events: make(chan db.AccountEvent)}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, _, err := claimDeviceJobWithin(ctx, store, "user", "device", 2, 5*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestOneHeldClaimPerDevice(t *testing.T) {
	var waits deviceClaimWaits
	if !waits.acquire("user:device") || waits.acquire("user:device") {
		t.Fatal("a device may hold only one claim")
	}
	if !waits.acquire("user:other") {
		t.Fatal("other devices hold their own claim")
	}
	waits.release("user:device")
	if !waits.acquire("user:device") {
		t.Fatal("a released device may hold a claim again")
	}
}
