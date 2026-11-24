package file

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/misty-org/misty-server/internal/collab"
	"github.com/misty-org/misty-server/internal/collab/ydoc"
)

var errCopyRefused = errors.New("file_text_copy_refused")

type textTarget struct {
	userID string
	base   int64
}

func (f *File) target() *textTarget {
	loadedFrom, ok := f.meta()
	if !ok {
		return nil
	}
	from, isNumber := number(loadedFrom)
	if !isNumber {
		return nil
	}
	userID := ""
	if f.writerIsHere() {
		userID = f.writer.UserID()
	}
	if userID == "" && len(f.editors) > 0 {
		userID = f.editors[0]
	}
	if userID == "" || f.host.SpaceID() == "" || f.host.Options().File == "" {
		return nil
	}
	base := f.baseVersion
	if base == 0 {
		base = int64(from)
	}
	return &textTarget{userID: userID, base: base}
}

func (f *File) currentText() string {
	text, err := f.host.Doc().Text(f.text)
	if err != nil {
		return ""
	}
	return text.String()
}

func (f *File) Idle(ctx context.Context) bool {
	purged := false
	if !f.host.Do(func() { purged = f.host.Purged() }) || purged {
		return false
	}
	f.settleText(ctx)
	f.forgetIfDone(ctx)
	keep := false
	f.host.Do(func() { keep = f.isText() && f.dirty && !f.restarting })
	return keep
}

func (f *File) settleText(ctx context.Context) {
	if !f.isText() {
		return
	}
	if err := f.saveText(ctx); err != nil {
		f.host.Do(func() {
			f.host.Log("file_text_save_failed", map[string]any{"error": err.Error()})
			f.host.ScheduleRetry()
		})
	}
}

func (f *File) forgetIfDone(ctx context.Context) {
	done := false
	f.host.Do(func() {
		done = !f.host.Busy() && f.saving == nil && !f.dirty && !f.restarting
	})
	if !done {
		return
	}
	if err := f.closeState(ctx, nil); err != nil {
		f.host.Do(func() { f.host.Log("file_room_forget_failed", map[string]any{"error": err.Error()}) })
		return
	}
	if err := f.host.Forget(ctx); err != nil {
		f.host.Do(func() { f.host.Log("file_room_forget_failed", map[string]any{"error": err.Error()}) })
		return
	}
	f.host.Do(func() { f.host.Log("file_room_forgotten", map[string]any{}) })
}

func (f *File) saveText(ctx context.Context) error {
	var run *textRun
	starting := false
	if !f.host.Do(func() {
		if f.saving != nil {
			f.textSaveQueued = true
			run = f.saving
			return
		}
		run = &textRun{done: make(chan struct{})}
		f.saving = run
		starting = true
	}) {
		return nil
	}
	if !starting {
		select {
		case <-run.done:
			return run.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	var err error
	for {
		f.host.Do(func() { f.textSaveQueued = false })
		if err = f.saveTextOnce(ctx); err != nil {
			break
		}
		again := false
		f.host.Do(func() { again = f.textSaveQueued && f.dirty })
		if !again {
			break
		}
	}
	run.err = err
	f.host.Do(func() { f.saving = nil })
	close(run.done)
	return err
}

func (f *File) waitText(bound time.Duration) {
	var run *textRun
	f.host.Do(func() { run = f.saving })
	if run == nil {
		return
	}
	timer := time.NewTimer(bound)
	defer timer.Stop()
	select {
	case <-run.done:
	case <-timer.C:
	}
}

func (f *File) saveTextOnce(ctx context.Context) error {
	var target *textTarget
	var generation, text string
	var snapshot []byte
	skip, keepStopped := true, false
	f.host.Do(func() {
		if !f.dirty || f.host.Purged() || f.resolving != nil {
			return
		}
		if f.textStopped {
			keepStopped = !f.host.Busy()
			return
		}
		if target = f.target(); target == nil {
			return
		}
		var err error
		if snapshot, err = f.host.Doc().Snapshot(); err != nil {
			return
		}
		generation, text, skip = f.generation, f.currentText(), false
	})
	if keepStopped {
		return f.keepStoppedText(ctx)
	}
	if skip {
		return nil
	}
	fingerprint := ydoc.FingerprintOf(snapshot)
	saveCtx, cancel := context.WithTimeout(ctx, textSaveBound)
	version, err := f.drive.SaveText(saveCtx, f.host.SpaceID(), f.host.Options().File, target.userID, text, target.base)
	cancel()
	var stale *Stale
	var failure error
	needKeep := false
	f.host.Do(func() {
		if generation != f.generation || f.restarting {
			return
		}
		if errors.As(err, &stale) {
			f.markTextStopped(stale.Version)
			needKeep = !f.host.Busy()
			return
		}
		if err != nil {
			failure = err
			return
		}
		if version > 0 {
			f.baseVersion = max(f.baseVersion, version)
			f.resolveTries.count = 0
			if meta, metaErr := f.host.Doc().Map(metaName); metaErr == nil {
				recorded, has := meta.Get("saved")
				current, isNumber := number(recorded)
				if !has || !isNumber || current < float64(version) {
					now, _ := f.host.Doc().Snapshot()
					clean := bytes.Equal(now, snapshot)
					_ = f.host.Doc().Change(nil, func(tx *ydoc.Tx) { tx.Map(metaName).Set(tx, "saved", version) })
					if clean {
						fingerprint = f.fingerprint()
					}
				}
			}
		}
		if generation != f.generation || f.restarting {
			return
		}
		f.savedFingerprint = fingerprint
		f.persistState()
		f.settleDirty()
	})
	if failure != nil {
		return failure
	}
	if needKeep {
		return f.keepStoppedText(ctx)
	}
	return nil
}

func (f *File) keepStoppedText(ctx context.Context) error {
	var target *textTarget
	var generation, text string
	var snapshot []byte
	var editors []string
	f.host.Do(func() {
		if target = f.target(); target == nil {
			return
		}
		snapshot, _ = f.host.Doc().Snapshot()
		generation, text, editors = f.generation, f.currentText(), append([]string(nil), f.editors...)
	})
	if target == nil {
		return nil
	}
	fingerprint := ydoc.FingerprintOf(snapshot)
	if _, ok := f.keepTextCopy(ctx, target, editors, text, time.Time{}); !ok {
		return errCopyRefused
	}
	f.host.Do(func() {
		if generation != f.generation {
			return
		}
		f.host.Log("file_text_kept", map[string]any{})
		f.savedFingerprint = fingerprint
		f.persistState()
		f.settleDirty()
	})
	return nil
}

func (f *File) keepTextCopy(ctx context.Context, target *textTarget, editors []string, text string, deadline time.Time) (string, bool) {
	seen := map[string]bool{}
	users := []string{}
	for _, userID := range append([]string{target.userID}, editors...) {
		if userID != "" && !seen[userID] {
			seen[userID] = true
			users = append(users, userID)
		}
	}
	for _, userID := range users {
		callCtx := ctx
		cancel := func() {}
		if !deadline.IsZero() {
			left := time.Until(deadline)
			if left < time.Second {
				return "", false
			}
			callCtx, cancel = context.WithTimeout(ctx, left)
		}
		name, err := f.drive.KeepCopy(callCtx, f.host.SpaceID(), f.host.Options().File, userID, text)
		cancel()
		if err == nil {
			return nameOf(name, true), true
		}
		if !errors.Is(err, ErrRefused) {
			return "", false
		}
	}
	return "", false
}

func (f *File) markTextStopped(version int64) {
	if f.textStopped {
		return
	}
	f.textStopped = true
	f.outsideVersion = version
	f.host.Log("file_text_outside", map[string]any{"version": version})
	f.persistState()
	f.tellAll()
}

func (f *File) stopTextSaves(s *collab.Session, version int64) {
	var generation string
	proceed := false
	f.host.Do(func() {
		if version == 0 || f.textStopped || f.host.Purged() || !f.loaded() {
			return
		}
		generation, proceed = f.generation, true
	})
	if !proceed {
		return
	}
	f.waitText(textSaveBound)
	proceed = false
	f.host.Do(func() {
		proceed = generation == f.generation && !f.textStopped && f.loaded() && version > f.knownVersion()
	})
	if !proceed || !f.claimHolds(ClaimOutside, version, s) {
		return
	}
	f.host.Do(func() {
		if generation != f.generation || f.textStopped || f.host.Purged() || !f.loaded() || version <= f.knownVersion() {
			return
		}
		f.markTextStopped(version)
	})
}
