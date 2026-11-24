package file

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"math"
	"sync"
	"time"

	"github.com/misty-org/misty-server/internal/collab"
	"github.com/misty-org/misty-server/internal/collab/wire"
)

const (
	Option               = "file"
	metaName             = "misty:file"
	reloadOrigin         = "misty:file-reload"
	announceAfter        = 250 * time.Millisecond
	maxFingerprintLength = 128
	maxLegacyVector      = 64 * 1024
	writerQuiet          = 30 * time.Second
	writerAnswer         = 10 * time.Second
	maxNonceLength       = 64
	reloadSaveWait       = 20 * time.Second
	maxEditors           = 5
	resolveBound         = 60 * time.Second
	resolveSaveWait      = 5 * time.Second
	resolveCopy          = 15 * time.Second
	resolveStopWait      = 8 * time.Second
	maxResolveTries      = 2
	maxNameLength        = 255
	maxClaims            = 8
	textSaveBound        = 60 * time.Second
)

type State struct {
	SavedFingerprint  string   `json:"saved_fingerprint,omitempty"`
	BaseVersion       int64    `json:"base_version,omitempty"`
	RenamedTo         int64    `json:"renamed_version,omitempty"`
	Generation        string   `json:"generation"`
	OutsideGeneration string   `json:"outside_generation,omitempty"`
	OutsideVersion    int64    `json:"outside_version,omitempty"`
	ContentGeneration string   `json:"content_generation,omitempty"`
	Editors           []string `json:"editors,omitempty"`
}

type Store interface {
	LoadFileState(ctx context.Context, roomID string) (State, bool, error)
	SaveFileState(ctx context.Context, roomID string, state State) error
	DeleteFileState(ctx context.Context, roomID string) error
}

type told struct {
	dirty, loaded, hasWriter bool
	base, renamed            int64
}

type writerCheck struct {
	nonce  string
	writer *collab.Session
	stop   func()
}

type kept struct{ name, fingerprint string }

type resolving struct {
	version int64
	stop    func()
	kept    *kept
}

type claimCall struct {
	done  chan struct{}
	holds bool
}

type textRun struct {
	done chan struct{}
	err  error
}

type File struct {
	host  collab.Host
	drive Drive
	store Store
	text  string

	writer           *collab.Session
	savedFingerprint string
	editors          []string
	saving           *textRun
	textSaveQueued   bool
	baseVersion      int64
	renamedTo        int64
	generation       string
	contentMode      bool
	revision         int64
	dirty            bool
	told             told
	announceStop     func()
	quietSince       time.Time
	quietStop        func()
	textStopped      bool
	outsideVersion   int64
	answeredAt       int64
	check            *writerCheck
	released         map[*collab.Session]bool
	restarting       bool
	blocking         bool
	resolving        *resolving
	resolveTries     struct{ version, count int64 }
	claims           map[string]*claimCall

	stateMu      sync.Mutex
	stateVersion int64
	stateWritten int64
	stateClosed  bool
}

func Factory(drive Drive, store Store) collab.BindingFactory {
	return func(ctx context.Context, host collab.Host) (collab.Binding, error) {
		options := host.Options()
		if options.File == "" {
			return nil, nil
		}
		f := &File{host: host, drive: drive, store: store, text: options.Text, answeredAt: -1,
			released: map[*collab.Session]bool{}, claims: map[string]*claimCall{}}
		state, found, err := store.LoadFileState(ctx, host.RoomID())
		if err != nil {
			return nil, err
		}
		f.savedFingerprint = state.SavedFingerprint
		f.editors = state.Editors
		if len(f.editors) > maxEditors {
			f.editors = f.editors[:maxEditors]
		}
		f.baseVersion = state.BaseVersion
		f.renamedTo = max(state.RenamedTo, 0)
		f.generation = state.Generation
		if !found || f.generation == "" {
			f.generation = newID()
			if err := store.SaveFileState(ctx, host.RoomID(), f.snapshotState()); err != nil {
				return nil, err
			}
		}
		f.textStopped = state.OutsideGeneration == f.generation
		f.contentMode = state.ContentGeneration == f.generation
		if f.textStopped {
			f.outsideVersion = max(state.OutsideVersion, 0)
		}
		f.dirty = f.loaded() && f.fingerprint() != f.savedFingerprint
		f.told = told{dirty: f.dirty, loaded: f.loaded(), base: f.baseVersion, renamed: f.renamedTo}
		f.quietSince = time.Now()
		return f, nil
	}
}

func newID() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

func (f *File) Option() string { return Option }

func (f *File) isText() bool { return f.text != "" }

func (f *File) RefusesUpdates() bool { return f.resolving != nil || f.blocking }

func (f *File) Unsaved() bool { return f.isText() && f.dirty }

func (f *File) Stop() {
	f.endCheck()
	if f.quietStop != nil {
		f.quietStop()
		f.quietStop = nil
	}
	if f.announceStop != nil {
		f.announceStop()
		f.announceStop = nil
	}
	if f.resolving != nil && f.resolving.stop != nil {
		f.resolving.stop()
	}
}

func (f *File) BeforeSave(context.Context, time.Time) {}

func (f *File) AfterSave(ctx context.Context) error {
	if !f.isText() {
		return nil
	}
	if err := f.saveText(ctx); err != nil {
		f.host.Log("file_text_save_failed", map[string]any{"error": err.Error()})
		return err
	}
	return nil
}

func (f *File) snapshotState() State {
	state := State{SavedFingerprint: f.savedFingerprint, BaseVersion: f.baseVersion, RenamedTo: f.renamedTo,
		Generation: f.generation, Editors: append([]string(nil), f.editors...)}
	if f.textStopped {
		state.OutsideGeneration = f.generation
		state.OutsideVersion = f.outsideVersion
	}
	if f.contentMode {
		state.ContentGeneration = f.generation
	}
	return state
}

func (f *File) persistState() {
	state := f.snapshotState()
	f.stateMu.Lock()
	f.stateVersion++
	version := f.stateVersion
	f.stateMu.Unlock()
	go f.writeState(state, version)
}

func (f *File) writeState(state State, version int64) {
	f.stateMu.Lock()
	defer f.stateMu.Unlock()
	if f.stateClosed || version <= f.stateWritten {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f.store.SaveFileState(ctx, f.host.RoomID(), state); err != nil {
		f.host.Log("file_state_save_failed", map[string]any{"error": err.Error()})
		return
	}
	f.stateWritten = version
}

func (f *File) closeState(ctx context.Context, replacement *State) error {
	f.stateMu.Lock()
	defer f.stateMu.Unlock()
	f.stateClosed = true
	if replacement != nil {
		return f.store.SaveFileState(ctx, f.host.RoomID(), *replacement)
	}
	return f.store.DeleteFileState(ctx, f.host.RoomID())
}

func (f *File) meta() (any, bool) {
	meta, err := f.host.Doc().Map(metaName)
	if err != nil {
		return nil, false
	}
	return meta.Get("loaded")
}

func (f *File) loaded() bool {
	_, ok := f.meta()
	return ok
}

func number(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, !math.IsNaN(v) && !math.IsInf(v, 0)
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	}
	return 0, false
}

func versionOf(value any) int64 {
	n, ok := number(value)
	if !ok || n <= 0 || n != math.Trunc(n) || n > 9007199254740991 {
		return 0
	}
	return int64(n)
}

func rawVersion(fields map[string]json.RawMessage, name string) int64 {
	raw, ok := fields[name]
	if !ok {
		return 0
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return 0
	}
	return versionOf(value)
}

func rawString(fields map[string]json.RawMessage, name string) (string, bool) {
	raw, ok := fields[name]
	if !ok {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func rawBool(fields map[string]json.RawMessage, name string) bool {
	var value bool
	if raw, ok := fields[name]; ok && json.Unmarshal(raw, &value) == nil {
		return value
	}
	return false
}

func nameOf(value string, ok bool) string {
	if ok && len(value) > 0 && len(value) <= maxNameLength {
		return value
	}
	return ""
}

func validFingerprint(value string, ok bool) string {
	if ok && len(value) > 0 && len(value) <= maxFingerprintLength {
		return value
	}
	return ""
}

func (f *File) fingerprint() string {
	fingerprint, err := f.host.Doc().Fingerprint()
	if err != nil {
		return ""
	}
	return fingerprint
}

func (f *File) savedVersion() int64 {
	loaded, _ := f.meta()
	return max(f.baseVersion, versionOf(loaded))
}

func (f *File) knownVersion() int64 {
	if f.contentMode {
		return f.savedVersion()
	}
	return max(f.savedVersion(), f.renamedTo)
}

func (f *File) send(s *collab.Session, fields map[string]any) {
	fields["option"] = Option
	f.host.Send(s, wire.TextFrame(fields))
}

func (f *File) present(s *collab.Session) bool {
	if s == nil {
		return false
	}
	for _, held := range f.host.Sessions() {
		if held == s {
			return true
		}
	}
	return false
}

func (f *File) writerIsHere() bool { return f.present(f.writer) }

func (f *File) writerIsUser(userID string) bool {
	return f.writerIsHere() && f.writer.UserID() == userID
}

func (f *File) tellAll() {
	f.told = told{dirty: f.dirty, loaded: f.loaded(), base: f.baseVersion, renamed: f.renamedTo, hasWriter: f.writerIsHere()}
	for _, s := range f.host.Sessions() {
		f.tell(s)
	}
}

func (f *File) tell(s *collab.Session) {
	fields := map[string]any{"type": "file_writer", "writer": s == f.writer, "dirty": f.dirty,
		"read_only": s.ReadOnly(), "loaded": f.loaded(), "generation": f.generation, "base": f.baseVersion,
		"has_writer": f.writerIsHere()}
	if f.renamedTo > 0 {
		fields["renamed"] = f.renamedTo
	}
	if f.resolving != nil {
		fields["resolving"] = f.resolving.version
		if f.resolving.kept != nil {
			fields["kept_name"] = f.resolving.kept.name
			fields["kept_fingerprint"] = f.resolving.kept.fingerprint
		}
	}
	if f.textStopped {
		fields["outside"] = true
		if f.outsideVersion > 0 {
			fields["drive_version"] = f.outsideVersion
		}
	}
	f.send(s, fields)
}

func (f *File) announceSoon() {
	if f.announceStop != nil {
		return
	}
	f.announceStop = f.host.After(announceAfter, func() {
		f.announceStop = nil
		f.announce()
	})
}

func (f *File) announce() {
	if f.loaded() == f.told.loaded && f.dirty == f.told.dirty && f.baseVersion == f.told.base &&
		f.renamedTo == f.told.renamed && f.writerIsHere() == f.told.hasWriter {
		return
	}
	f.tellAll()
}
