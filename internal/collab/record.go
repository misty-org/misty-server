package collab

import (
	"context"
	"errors"
	"time"

	"github.com/misty-org/misty-server/internal/collab/wire"
)

const (
	MaxDocumentBytes     = 8 * 1024 * 1024
	DocumentWarningBytes = MaxDocumentBytes * 8 / 10
	Seats                = 40
	EarlyFrameLimit      = 64
	EarlyByteLimit       = 1024 * 1024
)

var (
	ErrNoRoom     = errors.New("collab: no such room")
	ErrNoDocument = errors.New("collab: no saved document")
	ErrTooLarge   = errors.New("collab: document_limit_exceeded")
	ErrStopped    = errors.New("collab: the room is closing")
)

type Record struct {
	ID         string
	SpaceID    string
	Key        string
	Options    Options
	ACLVersion int64
	Purged     bool
	Generation int64
	Owner      string
}

type Document struct {
	State    []byte
	Checksum string
	SavedAt  time.Time
}

type Store interface {
	FindRoom(ctx context.Context, spaceID, key string) (Record, error)
	CreateRoom(ctx context.Context, spaceID, key string, options Options) (Record, error)
	RoomByID(ctx context.Context, roomID string) (Record, error)
	ClaimRoom(ctx context.Context, roomID string) (bool, error)
	ReleaseRoom(ctx context.Context, roomID string) error
	SetRoomOwner(ctx context.Context, roomID, url string) error
	LoadDocument(ctx context.Context, roomID string) (Document, error)
	SaveDocument(ctx context.Context, roomID string, state []byte, checksum string) error
	DeleteDocument(ctx context.Context, roomID string) error
}

type Conn interface {
	ID() string
	Send(frame wire.Frame) error
	Close(code int, reason string)
}

type Moved struct{ URL string }

func (m *Moved) Error() string { return "collab: the room is owned elsewhere" }
