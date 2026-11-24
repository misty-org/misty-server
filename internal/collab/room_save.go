package collab

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

const (
	saveDebounce = 2 * time.Second
	saveMaxWait  = 10 * time.Second
	saveBound    = 30 * time.Second
)

var saveRetryDelays = []time.Duration{2 * time.Second, 10 * time.Second, 60 * time.Second, 300 * time.Second}

type saver struct {
	changes      int64
	savedChanges int64
	retryStep    int
	firstDirty   time.Time
	debounce     *time.Timer
	retry        *time.Timer
	stopped      bool
	running      chan struct{}
}

func (s *saver) stopTimers() {
	if s.debounce != nil {
		s.debounce.Stop()
		s.debounce = nil
	}
	if s.retry != nil {
		s.retry.Stop()
		s.retry = nil
	}
}

func (r *Room) scheduleSave() {
	if r.save.stopped || r.purged {
		return
	}
	now := time.Now()
	if r.save.debounce == nil {
		r.save.firstDirty = now
	} else {
		r.save.debounce.Stop()
	}
	delay := saveDebounce
	if deadline := r.save.firstDirty.Add(saveMaxWait); now.Add(delay).After(deadline) {
		delay = max(deadline.Sub(now), 0)
	}
	r.save.debounce = time.AfterFunc(delay, func() {
		ctx, cancel := context.WithTimeout(context.Background(), saveBound)
		defer cancel()
		if err := r.Save(ctx); err != nil && !errors.Is(err, ErrStopped) {
			r.log("save_failed", map[string]any{"error": err.Error()})
		}
	})
}

func (r *Room) unsavedLocked() bool {
	if r.save.changes != r.save.savedChanges {
		return true
	}
	for _, binding := range r.bindings {
		if binding.Unsaved() {
			return true
		}
	}
	return false
}

func (r *Room) runningSaves() chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.save.running == nil {
		r.save.running = make(chan struct{}, 1)
	}
	return r.save.running
}

func (r *Room) Save(ctx context.Context) error {
	running := r.runningSaves()
	select {
	case running <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-running }()
	var state []byte
	var changes int64
	var encodeErr error
	skip := false
	ran := r.Do(func() {
		if r.save.debounce != nil {
			r.save.debounce.Stop()
			r.save.debounce = nil
		}
		if r.purged || r.save.stopped || r.restarting || r.forgotten {
			skip = true
			return
		}
		changes = r.save.changes
		state, encodeErr = r.doc.Update()
		if encodeErr == nil {
			r.gate.measured(len(state))
		}
	})
	if !ran || skip {
		return ErrStopped
	}
	if encodeErr != nil {
		r.Do(func() { r.fail(encodeErr) })
		return encodeErr
	}
	err := r.persist(ctx, state)
	r.Do(func() {
		if err != nil {
			code := "document_persistence_failed"
			if errors.Is(err, ErrTooLarge) {
				code = "document_limit_exceeded"
			}
			r.Broadcast(statusFrame("error", len(state), code), nil)
			r.log(code, map[string]any{"document_bytes": len(state)})
			r.scheduleRetry()
			return
		}
		r.save.savedChanges = changes
		r.save.retryStep = 0
		status := "saved"
		if len(state) >= DocumentWarningBytes {
			status = "warning"
		}
		r.Broadcast(statusFrame(status, len(state), ""), nil)
	})
	return err
}

func (r *Room) persist(ctx context.Context, state []byte) error {
	bindings := r.bindingsCopy()
	now := time.Now()
	for _, binding := range bindings {
		if persister, ok := binding.(Persister); ok {
			persister.BeforeSave(ctx, now)
		}
	}
	var failure error
	if len(state) > MaxDocumentBytes {
		failure = ErrTooLarge
	} else {
		sum := sha256.Sum256(state)
		failure = r.rooms.store.SaveDocument(ctx, r.rec.ID, state, hex.EncodeToString(sum[:]))
	}
	for _, binding := range bindings {
		persister, ok := binding.(Persister)
		if !ok {
			continue
		}
		if err := persister.AfterSave(ctx); err != nil && failure == nil {
			failure = err
		}
	}
	return failure
}

func (r *Room) bindingsCopy() []Binding {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Binding(nil), r.bindings...)
}

func (r *Room) ScheduleRetry() { r.scheduleRetry() }

func (r *Room) scheduleRetry() {
	if r.save.retry != nil || r.purged || r.save.stopped {
		return
	}
	if r.save.retryStep >= len(saveRetryDelays) {
		r.log("save_retry_gave_up", map[string]any{"attempts": r.save.retryStep})
		return
	}
	wait := saveRetryDelays[r.save.retryStep]
	r.save.retryStep++
	r.save.retry = time.AfterFunc(wait, func() {
		unsaved := false
		r.Do(func() {
			r.save.retry = nil
			unsaved = !r.purged && r.unsavedLocked()
		})
		if !unsaved {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), saveBound)
		defer cancel()
		if err := r.Save(ctx); err != nil && !errors.Is(err, ErrStopped) {
			r.log("save_retry_failed", map[string]any{"error": err.Error()})
		}
		r.Do(r.idleIfEmpty)
	})
}

func (r *Room) StopSaves(bound time.Duration) {
	running := r.runningSaves()
	r.Do(func() {
		r.save.stopped = true
		r.save.stopTimers()
	})
	timer := time.NewTimer(bound)
	defer timer.Stop()
	select {
	case running <- struct{}{}:
		<-running
	case <-timer.C:
	}
}
