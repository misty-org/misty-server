package collab

import (
	"context"
	"encoding/json"
	"time"

	"github.com/misty-org/misty-server/internal/collab/wire"
	"github.com/misty-org/misty-server/internal/collab/ydoc"
)

type Binding interface {
	Option() string
	Joined(s *Session)
	Left(s *Session)
	Message(s *Session, message wire.OptionMessage, fields map[string]json.RawMessage)
	Changed()
	RefusesUpdates() bool
	Unsaved() bool
	Stop()
}

type Persister interface {
	BeforeSave(ctx context.Context, now time.Time)
	AfterSave(ctx context.Context) error
}

type Idler interface {
	Idle(ctx context.Context) bool
}

type BindingFactory func(ctx context.Context, host Host) (Binding, error)

type Host interface {
	RoomID() string
	SpaceID() string
	Key() string
	Options() Options
	Doc() *ydoc.Doc
	Sessions() []*Session
	Send(s *Session, frame wire.Frame)
	Broadcast(frame wire.Frame, except *Session)
	Do(fn func()) bool
	After(delay time.Duration, fn func()) func()
	Save(ctx context.Context) error
	StopSaves(bound time.Duration)
	ScheduleRetry()
	Restart(reason string)
	Forget(ctx context.Context) error
	Busy() bool
	Purged() bool
	ApplyLocal(update []byte, origin any) error
	LastEditor() string
	Log(event string, details map[string]any)
}
