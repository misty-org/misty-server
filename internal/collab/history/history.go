package history

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sync"
	"time"

	"github.com/misty-org/misty-server/internal/collab"
	"github.com/misty-org/misty-server/internal/collab/wire"
	"github.com/misty-org/misty-server/internal/collab/ydoc"
)

const (
	Option        = "history"
	RestoreOrigin = "misty:history-restore"
	requestBound  = 30 * time.Second
)

var (
	ErrNoVersion   = errors.New("history: no such version")
	versionPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

type Store interface {
	Versions(ctx context.Context, roomID string) ([]Version, error)
	AddVersion(ctx context.Context, roomID string, version Version, state []byte) error
	ReadVersion(ctx context.Context, roomID, versionID string) (Version, []byte, error)
	DeleteVersions(ctx context.Context, roomID string, versionIDs []string) error
	LoadDocument(ctx context.Context, roomID string) (collab.Document, error)
}

type Limits func(ctx context.Context, spaceID string) (historyDays int, err error)

type History struct {
	host   collab.Host
	store  Store
	limits Limits

	mu         sync.Mutex
	last       time.Time
	previousBy string
}

func Factory(store Store, limits Limits) collab.BindingFactory {
	return func(ctx context.Context, host collab.Host) (collab.Binding, error) {
		if !host.Options().History {
			return nil, nil
		}
		h := &History{host: host, store: store, limits: limits}
		versions, err := store.Versions(ctx, host.RoomID())
		if err != nil {
			return nil, err
		}
		if n := len(versions); n > 0 {
			h.last = versions[n-1].At
		}
		return h, nil
	}
}

func (h *History) Option() string                  { return Option }
func (h *History) Joined(*collab.Session)          {}
func (h *History) Left(*collab.Session)            {}
func (h *History) Changed()                        {}
func (h *History) RefusesUpdates() bool            { return false }
func (h *History) Unsaved() bool                   { return false }
func (h *History) Stop()                           {}
func (h *History) AfterSave(context.Context) error { return nil }

func (h *History) retention(ctx context.Context) int {
	if h.limits == nil {
		return 0
	}
	days, err := h.limits(ctx, h.host.SpaceID())
	if err != nil || days < 1 {
		return 0
	}
	return days
}

func (h *History) BeforeSave(ctx context.Context, now time.Time) {
	var by string
	h.host.Do(func() { by = h.host.LastEditor() })
	h.mu.Lock()
	due := now.Sub(h.last) >= Cadence
	previousBy := h.previousBy
	h.previousBy = by
	h.mu.Unlock()
	if !due {
		return
	}
	previous, err := h.store.LoadDocument(ctx, h.host.RoomID())
	if errors.Is(err, collab.ErrNoDocument) {
		return
	}
	if err == nil {
		err = h.record(ctx, previous.State, previous.SavedAt, previous.Checksum, previousBy)
	}
	if err == nil {
		h.mu.Lock()
		h.last = now
		h.mu.Unlock()
		err = h.prune(ctx, now)
	}
	if err != nil {
		h.host.Log("history_record_failed", map[string]any{"error": err.Error()})
	}
}

func (h *History) record(ctx context.Context, state []byte, at time.Time, checksum, by string) error {
	if len(state) > collab.MaxDocumentBytes {
		return nil
	}
	if checksum == "" {
		sum := sha256.Sum256(state)
		checksum = hex.EncodeToString(sum[:])
	}
	versions, err := h.store.Versions(ctx, h.host.RoomID())
	if err != nil {
		return err
	}
	if n := len(versions); n > 0 && versions[n-1].Checksum == checksum {
		return nil
	}
	id, err := versionID()
	if err != nil {
		return err
	}
	return h.store.AddVersion(ctx, h.host.RoomID(), Version{ID: id, At: at, Size: len(state), By: by, Checksum: checksum}, state)
}

func (h *History) prune(ctx context.Context, now time.Time) error {
	versions, err := h.store.Versions(ctx, h.host.RoomID())
	if err != nil {
		return err
	}
	drop := Thin(versions, now, h.retention(ctx))
	if len(drop) == 0 {
		return nil
	}
	ids := make([]string, 0, len(drop))
	for _, version := range drop {
		ids = append(ids, version.ID)
	}
	return h.store.DeleteVersions(ctx, h.host.RoomID(), ids)
}

func versionID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "v_" + hex.EncodeToString(raw), nil
}

func (h *History) reply(s *collab.Session, request *int64, fields map[string]any) {
	fields["option"] = Option
	if request != nil {
		fields["request"] = *request
	}
	h.host.Do(func() { h.host.Send(s, wire.TextFrame(fields)) })
}

func (h *History) fail(s *collab.Session, request *int64, code string) {
	h.reply(s, request, map[string]any{"type": "error", "code": code})
}

func (h *History) Message(s *collab.Session, message wire.OptionMessage, fields map[string]json.RawMessage) {
	var version string
	if raw, ok := fields["version"]; ok {
		_ = json.Unmarshal(raw, &version)
	}
	readOnly := s.ReadOnly()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), requestBound)
		defer cancel()
		switch message.Type {
		case "list":
			h.list(ctx, s, message.Request)
		case "read":
			h.read(ctx, s, message.Request, version)
		case "restore":
			if readOnly {
				h.fail(s, message.Request, "read_only")
				return
			}
			h.restore(ctx, s, message.Request, version)
		default:
			h.fail(s, message.Request, "unknown_type")
		}
	}()
}

func (h *History) list(ctx context.Context, s *collab.Session, request *int64) {
	versions, err := h.store.Versions(ctx, h.host.RoomID())
	if err != nil {
		h.fail(s, request, "history_failed")
		return
	}
	now, days := time.Now(), h.retention(ctx)
	out := make([]map[string]any, 0, len(versions))
	for i := len(versions) - 1; i >= 0; i-- {
		version := versions[i]
		if !Kept(version, now, days) {
			continue
		}
		out = append(out, map[string]any{"id": version.ID, "at": version.At.UTC().Format(time.RFC3339Nano),
			"size": version.Size, "by": version.By})
	}
	h.reply(s, request, map[string]any{"type": "list", "versions": out})
}

func (h *History) find(ctx context.Context, s *collab.Session, request *int64, id string) (Version, []byte, bool) {
	if !versionPattern.MatchString(id) {
		h.fail(s, request, "not_found")
		return Version{}, nil, false
	}
	version, state, err := h.store.ReadVersion(ctx, h.host.RoomID(), id)
	if errors.Is(err, ErrNoVersion) || (err == nil && !Kept(version, time.Now(), h.retention(ctx))) {
		h.fail(s, request, "not_found")
		return Version{}, nil, false
	}
	if err != nil {
		h.fail(s, request, "history_failed")
		return Version{}, nil, false
	}
	return version, state, true
}

func (h *History) read(ctx context.Context, s *collab.Session, request *int64, id string) {
	version, state, ok := h.find(ctx, s, request, id)
	if !ok {
		return
	}
	h.reply(s, request, map[string]any{"type": "read", "version": version.ID,
		"update": base64.StdEncoding.EncodeToString(state)})
}

func (h *History) restore(ctx context.Context, s *collab.Session, request *int64, id string) {
	version, state, ok := h.find(ctx, s, request, id)
	if !ok {
		return
	}
	var current []byte
	var by string
	code := ""
	h.host.Do(func() {
		doc := h.host.Doc()
		rehearsal, err := doc.Copy()
		if err != nil {
			code = "history_failed"
			return
		}
		defer rehearsal.Destroy()
		if err := rehearsal.Restore(state, RestoreOrigin); err != nil {
			code = "history_failed"
			return
		}
		if size, err := rehearsal.Size(); err != nil || size > collab.MaxDocumentBytes {
			code = "document_too_large"
			return
		}
		current, err = doc.Update()
		if err != nil {
			code = "history_failed"
		}
		by = h.host.LastEditor()
	})
	if code != "" {
		h.fail(s, request, code)
		return
	}
	now := time.Now()
	err := h.record(ctx, current, now, "", by)
	if err == nil {
		h.mu.Lock()
		h.last = now
		h.mu.Unlock()
		err = h.prune(ctx, now)
	}
	if err != nil {
		h.host.Log("history_record_failed", map[string]any{"error": err.Error()})
	}
	var restoreErr error
	h.host.Do(func() { restoreErr = h.host.Doc().Restore(state, RestoreOrigin) })
	if restoreErr != nil {
		if errors.Is(restoreErr, ydoc.ErrPanic) {
			h.host.Log("history_restore_failed", map[string]any{"error": restoreErr.Error()})
		}
		h.fail(s, request, "history_failed")
		return
	}
	if err := h.host.Save(ctx); err != nil && !errors.Is(err, collab.ErrStopped) {
		h.host.Log("history_restore_save_failed", map[string]any{"error": err.Error()})
	}
	h.reply(s, request, map[string]any{"type": "restored", "version": version.ID})
}
