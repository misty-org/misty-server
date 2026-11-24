package collab

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/misty-org/misty-server/internal/collab/admit"
	"github.com/misty-org/misty-server/internal/collab/wire"
)

type memoryStore struct {
	mu      sync.Mutex
	rooms   map[string]Record
	docs    map[string]Document
	held    map[string]bool
	refuse  bool
	nextID  int
	created int
}

func newMemoryStore() *memoryStore {
	return &memoryStore{rooms: map[string]Record{}, docs: map[string]Document{}, held: map[string]bool{}}
}

func (s *memoryStore) FindRoom(_ context.Context, spaceID, key string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range s.rooms {
		if rec.SpaceID == spaceID && rec.Key == key {
			return rec, nil
		}
	}
	return Record{}, ErrNoRoom
}

func (s *memoryStore) CreateRoom(ctx context.Context, spaceID, key string, options Options) (Record, error) {
	if rec, err := s.FindRoom(ctx, spaceID, key); err == nil {
		return rec, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	s.created++
	rec := Record{ID: "rm_" + strings.Repeat("a", 25) + string(rune('a'+s.nextID%26)), SpaceID: spaceID, Key: key, Options: options}
	s.rooms[rec.ID] = rec
	return rec, nil
}

func (s *memoryStore) RoomByID(_ context.Context, roomID string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.rooms[roomID]
	if !ok {
		return Record{}, ErrNoRoom
	}
	return rec, nil
}

func (s *memoryStore) ClaimRoom(_ context.Context, roomID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refuse {
		return false, nil
	}
	s.held[roomID] = true
	return true, nil
}

func (s *memoryStore) ReleaseRoom(_ context.Context, roomID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.held, roomID)
	return nil
}

func (s *memoryStore) SetRoomOwner(_ context.Context, roomID, url string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.rooms[roomID]
	rec.Owner = url
	s.rooms[roomID] = rec
	return nil
}

func (s *memoryStore) LoadDocument(_ context.Context, roomID string) (Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.docs[roomID]
	if !ok {
		return Document{}, ErrNoDocument
	}
	return doc, nil
}

func (s *memoryStore) SaveDocument(_ context.Context, roomID string, state []byte, checksum string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[roomID] = Document{State: append([]byte(nil), state...), Checksum: checksum, SavedAt: time.Now()}
	return nil
}

func (s *memoryStore) DeleteDocument(_ context.Context, roomID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.docs, roomID)
	return nil
}

func (s *memoryStore) document(roomID string) (Document, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.docs[roomID]
	return doc, ok
}

type fakeConn struct {
	id     string
	frames chan wire.Frame
	mu     sync.Mutex
	code   int
	reason string
	closed chan struct{}
	once   sync.Once
}

func newFakeConn(id string) *fakeConn {
	return &fakeConn{id: id, frames: make(chan wire.Frame, 4096), closed: make(chan struct{})}
}

func (c *fakeConn) ID() string { return c.id }

func (c *fakeConn) Send(frame wire.Frame) error {
	c.frames <- frame
	return nil
}

func (c *fakeConn) Close(code int, reason string) {
	c.once.Do(func() {
		c.mu.Lock()
		c.code, c.reason = code, reason
		c.mu.Unlock()
		close(c.closed)
	})
}

func (c *fakeConn) waitClosed(t *testing.T) int {
	t.Helper()
	select {
	case <-c.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the connection was not closed")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.code
}

func (c *fakeConn) next(t *testing.T) wire.Frame {
	t.Helper()
	select {
	case frame := <-c.frames:
		return frame
	case <-time.After(5 * time.Second):
		t.Fatal("no frame arrived")
	}
	return wire.Frame{}
}

func (c *fakeConn) text(t *testing.T, want string) map[string]any {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case frame := <-c.frames:
			if !frame.Text {
				continue
			}
			fields := map[string]any{}
			if json.Unmarshal(frame.Data, &fields) == nil && (fields["type"] == want) {
				return fields
			}
		case <-deadline:
			t.Fatalf("no %s message arrived", want)
		}
	}
}

type testWorld struct {
	store   *memoryStore
	rooms   *Rooms
	members map[string]bool
	files   map[string]admit.FileAccess
	hold    chan struct{}
}

func newWorld(t *testing.T) *testWorld {
	t.Helper()
	w := &testWorld{store: newMemoryStore(), members: map[string]bool{"ana": true, "sam": true}, files: map[string]admit.FileAccess{}}
	gate := admit.Gate{
		VerifyPerson: func(token string) (admit.PersonToken, error) {
			user, ok := strings.CutPrefix(token, "person-")
			if !ok {
				return admit.PersonToken{}, admit.ErrUnauthorized
			}
			return admit.PersonToken{UserID: user, SessionID: "ses_" + user}, nil
		},
		VerifyIntegration: func(token string) (admit.IntegrationToken, error) {
			id, ok := strings.CutPrefix(token, "integration-")
			if !ok {
				return admit.IntegrationToken{}, admit.ErrUnauthorized
			}
			return admit.IntegrationToken{ID: id, SpaceID: "sp_one", Access: []string{admit.AccessRoomsWrite}, KeyPrefix: "bot:"}, nil
		},
		Member: func(ctx context.Context, spaceID, userID string) (admit.Member, error) {
			if w.hold != nil {
				<-w.hold
			}
			if spaceID != "sp_one" {
				return admit.Member{}, admit.ErrSpaceMissing
			}
			if !w.members[userID] {
				return admit.Member{}, admit.ErrNotMember
			}
			return admit.Member{Name: userID}, nil
		},
		SpaceExists: func(ctx context.Context, spaceID string) (bool, error) { return spaceID == "sp_one", nil },
		File: func(ctx context.Context, spaceID, itemID, userID string) (admit.FileAccess, error) {
			access, ok := w.files[itemID]
			if !ok {
				return admit.FileAccess{}, admit.ErrFileMissing
			}
			return access, nil
		},
	}
	w.rooms = New(Config{Store: w.store, Gate: gate, SelfURL: "https://self.example", PublicURL: "https://public.example",
		Logf: func(string, ...any) {}})
	return w
}

func openFrame(t *testing.T, token, key string, options any) wire.Frame {
	t.Helper()
	fields := map[string]any{"type": "open", "v": 1, "space": "sp_one", "key": key, "token": token}
	if options != nil {
		fields["options"] = options
	}
	return wire.TextFrame(fields)
}

func (w *testWorld) join(t *testing.T, id, token, key string, options any) (*fakeConn, *Session) {
	t.Helper()
	conn := newFakeConn(id)
	session := w.rooms.Open(conn, "")
	session.Receive(openFrame(t, token, key, options))
	return conn, session
}
