package history_test

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/misty-org/misty-server/internal/collab"
	"github.com/misty-org/misty-server/internal/collab/admit"
	"github.com/misty-org/misty-server/internal/collab/history"
	"github.com/misty-org/misty-server/internal/collab/wire"
	"github.com/misty-org/misty-server/internal/collab/ydoc"
)

type store struct {
	mu       sync.Mutex
	rec      *collab.Record
	doc      *collab.Document
	versions map[string]history.Version
	states   map[string][]byte
}

func (s *store) FindRoom(context.Context, string, string) (collab.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rec == nil {
		return collab.Record{}, collab.ErrNoRoom
	}
	return *s.rec, nil
}
func (s *store) CreateRoom(_ context.Context, spaceID, key string, options collab.Options) (collab.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rec == nil {
		s.rec = &collab.Record{ID: "rm_" + strings.Repeat("a", 26), SpaceID: spaceID, Key: key, Options: options}
	}
	return *s.rec, nil
}
func (s *store) RoomByID(ctx context.Context, _ string) (collab.Record, error) {
	return s.FindRoom(ctx, "", "")
}
func (s *store) ClaimRoom(context.Context, string) (bool, error)    { return true, nil }
func (s *store) ReleaseRoom(context.Context, string) error          { return nil }
func (s *store) SetRoomOwner(context.Context, string, string) error { return nil }
func (s *store) LoadDocument(context.Context, string) (collab.Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.doc == nil {
		return collab.Document{}, collab.ErrNoDocument
	}
	return *s.doc, nil
}
func (s *store) SaveDocument(_ context.Context, _ string, state []byte, checksum string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.doc = &collab.Document{State: append([]byte(nil), state...), Checksum: checksum, SavedAt: time.Now()}
	return nil
}
func (s *store) DeleteDocument(context.Context, string) error { return nil }
func (s *store) Versions(context.Context, string) ([]history.Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []history.Version{}
	for _, version := range s.versions {
		out = append(out, version)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}
func (s *store) AddVersion(_ context.Context, _ string, version history.Version, state []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions[version.ID], s.states[version.ID] = version, append([]byte(nil), state...)
	return nil
}
func (s *store) ReadVersion(_ context.Context, _, id string) (history.Version, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	version, ok := s.versions[id]
	if !ok {
		return history.Version{}, nil, history.ErrNoVersion
	}
	return version, s.states[id], nil
}
func (s *store) DeleteVersions(_ context.Context, _ string, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		delete(s.versions, id)
		delete(s.states, id)
	}
	return nil
}

type conn struct{ frames chan wire.Frame }

func (c *conn) ID() string                  { return "c" }
func (c *conn) Send(frame wire.Frame) error { c.frames <- frame; return nil }
func (c *conn) Close(int, string)           {}

func (c *conn) option(t *testing.T, kind string) map[string]any {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case frame := <-c.frames:
			fields := map[string]any{}
			if frame.Text && json.Unmarshal(frame.Data, &fields) == nil && fields["option"] == history.Option && fields["type"] == kind {
				return fields
			}
		case <-deadline:
			t.Fatalf("no %s answer", kind)
		}
	}
}

func update(t *testing.T, base *ydoc.Doc, text string) []byte {
	t.Helper()
	vector, _ := base.StateVector()
	if err := base.Change(nil, func(tx *ydoc.Tx) { tx.Text("title").Replace(tx, text) }); err != nil {
		t.Fatal(err)
	}
	out, err := base.UpdateSince(vector)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestHistoryKeepsListsReadsAndRestoresVersions(t *testing.T) {
	memory := &store{versions: map[string]history.Version{}, states: map[string][]byte{}}
	gate := admit.Gate{
		VerifyPerson: func(token string) (admit.PersonToken, error) {
			return admit.PersonToken{UserID: "ana", SessionID: "s"}, nil
		},
		Member: func(context.Context, string, string) (admit.Member, error) { return admit.Member{Name: "Ana"}, nil },
	}
	rooms := collab.New(collab.Config{Store: memory, Gate: gate, PublicURL: "https://p", Logf: func(string, ...any) {},
		Bindings: []collab.BindingFactory{history.Factory(memory, nil)}})
	client := &conn{frames: make(chan wire.Frame, 1024)}
	session := rooms.Open(client, "token")
	session.Receive(wire.TextFrame(map[string]any{"type": "open", "v": 1, "space": "sp", "key": "notes", "options": map[string]any{"history": true}}))
	<-session.Opened()
	local := ydoc.New(true)
	ctx := context.Background()
	if err := rooms.Change(ctx, "token", "sp", "notes", update(t, local, "First"), nil); err != nil {
		t.Fatal(err)
	}
	if err := rooms.Change(ctx, "token", "sp", "notes", update(t, local, "Second"), nil); err != nil {
		t.Fatal(err)
	}
	session.Receive(wire.TextFrame(map[string]any{"option": "history", "type": "list", "request": 1}))
	listed := client.option(t, "list")
	versions, _ := listed["versions"].([]any)
	if len(versions) != 1 || listed["request"] != float64(1) {
		t.Fatalf("listed %v", listed)
	}
	id := versions[0].(map[string]any)["id"].(string)
	session.Receive(wire.TextFrame(map[string]any{"option": "history", "type": "read", "request": 2, "version": id}))
	if read := client.option(t, "read"); read["version"] != id || read["update"] == "" {
		t.Fatalf("read %v", read)
	}
	session.Receive(wire.TextFrame(map[string]any{"option": "history", "type": "read", "request": 3, "version": "nope"}))
	if failure := client.option(t, "error"); failure["code"] != "not_found" {
		t.Fatalf("missing version %v", failure)
	}
	session.Receive(wire.TextFrame(map[string]any{"option": "history", "type": "restore", "request": 4, "version": id}))
	if restored := client.option(t, "restored"); restored["version"] != id {
		t.Fatalf("restored %v", restored)
	}
	content, err := rooms.Read(ctx, "token", "sp", "notes", true)
	if err != nil || content.JSON["title"] != "First" {
		t.Fatalf("after restore %v %v", content.JSON, err)
	}
	session.Closed()
}
