package file

import (
	"context"
	"time"

	"github.com/misty-org/misty-server/internal/collab"
	"github.com/misty-org/misty-server/internal/collab/wire"
)

func (f *File) reload(s *collab.Session, version int64, keptCopy bool, keptAs, keptName string) {
	proceed := false
	f.host.Do(func() {
		if version == 0 || f.host.Purged() || !f.loaded() || f.saving != nil || f.restarting || f.blocking {
			return
		}
		if f.resolving != nil && !keptCopy {
			return
		}
		if keptCopy {
			if keptAs == "" || f.writer != s {
				return
			}
		} else if f.dirty || !f.mayReload(s) {
			return
		}
		if !f.textStopped && version <= f.knownVersion() {
			return
		}
		proceed = true
	})
	if !proceed || (keptCopy && !f.claimHolds(ClaimKept, version, s)) {
		return
	}
	restart := false
	var current string
	f.host.Do(func() {
		if f.host.Purged() || !f.loaded() || f.saving != nil || f.restarting || f.blocking {
			return
		}
		current = f.fingerprint()
		if keptCopy {
			if f.writer != s || current != keptAs {
				return
			}
		} else if f.dirty || !f.mayReload(s) || current != f.savedFingerprint {
			return
		}
		f.beginRestart(keptCopy, current, keptName)
		restart = true
	})
	if restart {
		f.finishRestart(version, keptCopy, reloadSaveWait)
	}
}

func (f *File) beginRestart(keptCopy bool, current, keptName string) {
	f.generation = newID()
	f.contentMode = false
	if f.resolving != nil && f.resolving.stop != nil {
		f.resolving.stop()
	}
	f.endCheck()
	if f.quietStop != nil {
		f.quietStop()
		f.quietStop = nil
	}
	for _, s := range f.host.Sessions() {
		fields := map[string]any{"type": "file_writer", "writer": false, "dirty": false, "read_only": s.ReadOnly(),
			"loaded": false, "generation": f.generation, "base": 0, "has_writer": false, "kept": keptCopy,
			"fingerprint": current}
		if keptName != "" {
			fields["kept_name"] = keptName
		}
		f.send(s, fields)
	}
	f.blocking = true
	if err := f.host.Doc().Clear(reloadOrigin); err != nil {
		f.host.Log("file_clear_failed", map[string]any{"error": err.Error()})
	}
}

func (f *File) finishRestart(version int64, keptCopy bool, stopWait time.Duration) {
	f.host.StopSaves(stopWait)
	f.waitText(stopWait)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var fresh State
	f.host.Do(func() { fresh = State{Generation: f.generation, Editors: append([]string(nil), f.editors...)} })
	if err := f.host.Forget(ctx); err != nil {
		f.host.Do(func() { f.host.Log("file_reload_delete_failed", map[string]any{"error": err.Error()}) })
	}
	if err := f.closeState(ctx, &fresh); err != nil {
		f.host.Do(func() { f.host.Log("file_reload_state_failed", map[string]any{"error": err.Error()}) })
	}
	f.host.Do(func() {
		f.restarting = true
		f.host.Log("file_room_reloaded", map[string]any{"version": version, "kept": keptCopy})
		f.host.Restart("file_reloaded")
	})
}

func (f *File) resolve(s *collab.Session, version int64) {
	if f.resolving != nil {
		f.send(s, map[string]any{"type": "file_resolving", "version": f.resolving.version})
		return
	}
	if version > 0 && version > f.resolveTries.version {
		f.resolveTries.version, f.resolveTries.count = version, 0
	}
	if version == 0 || f.host.Purged() || f.restarting || !f.loaded() || s != f.writer ||
		f.resolveTries.count >= maxResolveTries || (!f.textStopped && version <= f.knownVersion()) {
		f.send(s, map[string]any{"type": "file_resolve_declined", "version": version})
		return
	}
	f.resolveTries.count++
	current := &resolving{version: version}
	current.stop = f.host.After(resolveBound, func() { f.resolveTimedOut(version) })
	f.resolving = current
	f.endCheck()
	if f.quietStop != nil {
		f.quietStop()
		f.quietStop = nil
	}
	f.host.Log("file_resolving", map[string]any{"version": version, "text": f.isText()})
	for _, other := range f.host.Sessions() {
		f.send(other, map[string]any{"type": "file_resolving", "version": version})
	}
	if f.isText() {
		go f.resolveText(version)
	}
}

func (f *File) keptForResolve(s *collab.Session, version int64, name, fingerprint string) {
	current := f.resolving
	if current == nil || current.kept != nil || f.isText() {
		return
	}
	if version != current.version || name == "" || fingerprint == "" {
		return
	}
	go func() {
		holds := f.claimHolds(ClaimKept, version, s)
		f.host.Do(func() {
			if !holds || f.resolving != current || current.kept != nil || f.restarting {
				return
			}
			current.kept = &kept{name: name, fingerprint: fingerprint}
			f.tellAll()
		})
	}()
}

func (f *File) askResend() {
	vector, err := f.host.Doc().StateVector()
	if err != nil {
		return
	}
	frame := wire.SyncStep1Frame(vector)
	for _, s := range f.host.Sessions() {
		if !s.ReadOnly() {
			f.host.Send(s, frame)
		}
	}
}

func (f *File) resolveTimedOut(version int64) {
	if f.resolving == nil || f.resolving.version != version {
		return
	}
	f.host.Log("file_resolve_timed_out", map[string]any{"version": version})
	f.endResolve()
}

func (f *File) stillResolving(version int64, generation string) bool {
	return f.resolving != nil && f.resolving.version == version && f.generation == generation &&
		!f.host.Purged() && !f.restarting && f.loaded()
}

func (f *File) resolveText(version int64) {
	var generation string
	f.host.Do(func() { generation = f.generation })
	f.waitText(resolveSaveWait)
	proceed := false
	f.host.Do(func() {
		if !f.stillResolving(version, generation) {
			return
		}
		if !f.textStopped && version <= f.savedVersion() {
			f.dropResolve()
			return
		}
		proceed = true
	})
	if !proceed {
		return
	}
	var target *textTarget
	var text, current string
	var editors []string
	f.host.Do(func() {
		target = f.target()
		if target == nil || !f.stillResolving(version, generation) {
			target = nil
			return
		}
		text, current, editors = f.currentText(), f.fingerprint(), append([]string(nil), f.editors...)
		f.blocking = true
	})
	restarted := false
	if target != nil {
		name, ok := f.keepTextCopy(context.Background(), target, editors, text, time.Now().Add(resolveCopy))
		if !ok {
			f.host.Do(func() { f.host.Log("file_text_copy_failed", map[string]any{"error": "the copy was not kept"}) })
		}
		f.host.Do(func() {
			f.blocking = false
			if !ok || !f.stillResolving(version, generation) {
				return
			}
			f.host.Log("file_text_kept", map[string]any{"resolving": true})
			f.beginRestart(true, current, name)
			restarted = true
		})
		if restarted {
			f.finishRestart(version, true, resolveStopWait)
			return
		}
	}
	f.host.Do(func() {
		if f.resolving != nil && f.resolving.version == version && f.generation == generation {
			f.endResolve()
		}
	})
}

func (f *File) endResolve() {
	current := f.resolving
	if current == nil || f.restarting {
		return
	}
	if current.stop != nil {
		current.stop()
	}
	f.resolving = nil
	f.askResend()
	if f.isText() {
		if f.textStopped {
			f.tellAll()
		} else {
			f.markTextStopped(current.version)
		}
		if !f.host.Busy() {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), textSaveBound)
				defer cancel()
				f.settleText(ctx)
				f.forgetIfDone(ctx)
			}()
		}
		return
	}
	for _, s := range f.host.Sessions() {
		f.send(s, map[string]any{"type": "file_outside"})
	}
	f.tellAll()
	f.checkWriterIfQuiet()
}

func (f *File) dropResolve() {
	current := f.resolving
	if current == nil || f.restarting {
		return
	}
	if current.stop != nil {
		current.stop()
	}
	f.resolving = nil
	f.askResend()
	f.host.Log("file_resolve_dropped", map[string]any{"version": current.version})
	f.tellAll()
	f.checkWriterIfQuiet()
}
