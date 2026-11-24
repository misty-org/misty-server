package collab

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/misty-org/misty-server/internal/collab/admit"
	"github.com/misty-org/misty-server/internal/collab/wire"
)

const (
	unloadBound   = 30 * time.Second
	recheckBound  = 15 * time.Second
	shutdownBound = 20 * time.Second
)

type Config struct {
	Store     Store
	Gate      admit.Gate
	Bindings  []BindingFactory
	SelfURL   string
	PublicURL string
	Logf      func(format string, args ...any)
}

type Rooms struct {
	store    Store
	gate     admit.Gate
	bindings []BindingFactory
	self     string
	public   string
	logf     func(format string, args ...any)

	mu       sync.Mutex
	rooms    map[string]*Room
	closing  bool
	refusals map[int]int
}

func New(config Config) *Rooms {
	logf := config.Logf
	if logf == nil {
		logf = log.Printf
	}
	public := config.PublicURL
	self := config.SelfURL
	if self == "" {
		self = public
	}
	return &Rooms{store: config.Store, gate: config.Gate, bindings: config.Bindings, self: self, public: public,
		logf: logf, rooms: map[string]*Room{}, refusals: map[int]int{}}
}

func (rs *Rooms) selfURL() string { return rs.self }

func (rs *Rooms) Open(conn Conn, bearer string) *Session {
	session := &Session{rooms: rs, conn: conn, bearer: bearer, opened: make(chan struct{})}
	rs.mu.Lock()
	closing := rs.closing
	rs.mu.Unlock()
	if closing {
		session.state = sessionAdmitting
		go session.refuse(wire.CloseMoved, rs.public)
	}
	return session
}

func (rs *Rooms) Identify(token string) (string, error) {
	who, err := rs.gate.Authenticate(token)
	if err != nil {
		return "", err
	}
	return who.Kind + ":" + who.ID, nil
}

func (rs *Rooms) HandBack() {
	for _, room := range rs.loaded() {
		room.Do(func() { room.dropAll(wire.CloseMoved, rs.public) })
	}
}

func (rs *Rooms) log(event string, details map[string]any) {
	rs.logRoom("", event, details)
}

func (rs *Rooms) logRoom(roomID, event string, details map[string]any) {
	fields := map[string]any{"event": event}
	if roomID != "" {
		fields["room"] = roomID
	}
	for key, value := range details {
		fields[key] = value
	}
	raw, _ := json.Marshal(fields)
	rs.logf("collab %s", raw)
}

func (rs *Rooms) countRefusal(code int) {
	rs.mu.Lock()
	rs.refusals[code]++
	count := rs.refusals[code]
	rs.mu.Unlock()
	if count&(count-1) == 0 {
		rs.log("connection_refused", map[string]any{"code": wire.CloseName(code), "count": count})
	}
}

func (rs *Rooms) authorize(ctx context.Context, token, spaceID, key string, rawOptions json.RawMessage, create bool) (Record, admit.Grant, error) {
	who, err := rs.gate.Authenticate(token)
	if err != nil {
		return Record{}, admit.Grant{}, err
	}
	if !wire.ValidKey(key) || spaceID == "" {
		return Record{}, admit.Grant{}, admit.Refuse(wire.CloseBadOpen, "the room's address is not valid")
	}
	requested, err := ParseRequested(rawOptions)
	if err != nil {
		return Record{}, admit.Grant{}, admit.Refuse(wire.CloseBadOpen, "the options could not be read")
	}
	rec, err := rs.store.FindRoom(ctx, spaceID, key)
	found := err == nil
	if err != nil && !errors.Is(err, ErrNoRoom) {
		return Record{}, admit.Grant{}, err
	}
	if found && !requested.Fits(rec.Options) {
		return Record{}, admit.Grant{}, admit.Refuse(wire.CloseOptionsConflict, "the options differ from the room's")
	}
	fileID := requested.FileID()
	if found {
		fileID = rec.Options.File
	}
	grant, err := rs.gate.Check(ctx, who, spaceID, key, fileID)
	if err != nil {
		return Record{}, admit.Grant{}, err
	}
	if !found {
		if !create {
			return Record{}, admit.Grant{}, ErrNoRoom
		}
		if rec, err = rs.store.CreateRoom(ctx, spaceID, key, requested.New()); err != nil {
			return Record{}, admit.Grant{}, err
		}
		if !requested.Fits(rec.Options) {
			return Record{}, admit.Grant{}, admit.Refuse(wire.CloseOptionsConflict, "the options differ from the room's")
		}
		if rec.Options.File != fileID {
			if grant, err = rs.gate.Check(ctx, who, spaceID, key, rec.Options.File); err != nil {
				return Record{}, admit.Grant{}, err
			}
		}
	}
	if rec.Purged {
		return Record{}, admit.Grant{}, admit.Refuse(wire.CloseGone, "the room was purged")
	}
	return rec, grant, nil
}

func (rs *Rooms) enter(ctx context.Context, token, spaceID, key string, rawOptions json.RawMessage) (*Room, admit.Grant, int64, error) {
	rec, grant, err := rs.authorize(ctx, token, spaceID, key, rawOptions, true)
	if err != nil {
		return nil, admit.Grant{}, 0, err
	}
	room, err := rs.acquire(ctx, rec)
	if err != nil {
		return nil, admit.Grant{}, 0, err
	}
	return room, grant, rec.ACLVersion, nil
}

func (rs *Rooms) acquire(ctx context.Context, rec Record) (*Room, error) {
	for {
		rs.mu.Lock()
		if rs.closing {
			rs.mu.Unlock()
			return nil, &Moved{URL: rs.public}
		}
		room := rs.rooms[rec.ID]
		if room == nil {
			room = newRoom(rs, rec)
			room.pending = 1
			rs.rooms[rec.ID] = room
			rs.mu.Unlock()
			if err := rs.bring(ctx, room); err != nil {
				rs.mu.Lock()
				delete(rs.rooms, rec.ID)
				rs.mu.Unlock()
				room.loadErr = err
				close(room.ready)
				close(room.done)
				return nil, err
			}
			close(room.ready)
			return room, nil
		}
		select {
		case <-room.ready:
		default:
			rs.mu.Unlock()
			select {
			case <-room.ready:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		room.mu.Lock()
		if room.loadErr == nil && !room.unloading && !room.released {
			room.pending++
			room.mu.Unlock()
			rs.mu.Unlock()
			return room, nil
		}
		wait := room.done
		if room.unloading && room.settle != nil {
			wait = room.settle
		}
		room.mu.Unlock()
		rs.mu.Unlock()
		select {
		case <-wait:
		case <-room.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (rs *Rooms) bring(ctx context.Context, room *Room) error {
	claimed, err := rs.store.ClaimRoom(ctx, room.rec.ID)
	if err != nil {
		return err
	}
	if !claimed {
		owner := rs.public
		if current, err := rs.store.RoomByID(ctx, room.rec.ID); err == nil && current.Owner != "" {
			owner = current.Owner
		}
		return &Moved{URL: owner}
	}
	current, err := rs.store.RoomByID(ctx, room.rec.ID)
	if err == nil {
		room.rec = current
		room.acl = current.ACLVersion
	}
	if err == nil && current.Purged {
		err = admit.Refuse(wire.CloseGone, "the room was purged")
	}
	if err == nil {
		err = room.load(ctx)
	}
	if err == nil {
		err = rs.store.SetRoomOwner(ctx, room.rec.ID, rs.self)
		if err != nil {
			room.stop()
		}
	}
	if err != nil {
		_ = rs.store.ReleaseRoom(context.Background(), room.rec.ID)
		return err
	}
	return nil
}

func (rs *Rooms) release(room *Room) {
	room.mu.Lock()
	if room.released {
		room.mu.Unlock()
		return
	}
	room.released = true
	room.stop()
	room.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := rs.store.ReleaseRoom(ctx, room.rec.ID); err != nil {
		rs.logRoom(room.rec.ID, "room_release_failed", map[string]any{"error": err.Error()})
	}
	rs.mu.Lock()
	if rs.rooms[room.rec.ID] == room {
		delete(rs.rooms, room.rec.ID)
	}
	rs.mu.Unlock()
	close(room.done)
}

func (rs *Rooms) unload(room *Room) {
	room.mu.Lock()
	if room.Busy() || room.unloading || room.released {
		room.mu.Unlock()
		return
	}
	room.unloading = true
	room.settle = make(chan struct{})
	unsaved := !room.purged && !room.restarting && !room.forgotten && room.unsavedLocked()
	bindings := append([]Binding(nil), room.bindings...)
	room.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), unloadBound)
	defer cancel()
	if unsaved {
		if err := room.Save(ctx); err != nil && !errors.Is(err, ErrStopped) {
			room.log("save_on_close_failed", map[string]any{"error": err.Error()})
		}
	}
	keep := false
	for _, binding := range bindings {
		if idler, ok := binding.(Idler); ok && idler.Idle(ctx) {
			keep = true
		}
	}
	room.mu.Lock()
	settle := room.settle
	room.settle = nil
	if keep && room.save.retry != nil && !room.purged && !room.restarting && !room.forgotten {
		room.unloading = false
		room.mu.Unlock()
		close(settle)
		return
	}
	room.mu.Unlock()
	rs.release(room)
	close(settle)
}

func (rs *Rooms) loaded() []*Room {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	out := make([]*Room, 0, len(rs.rooms))
	for _, room := range rs.rooms {
		select {
		case <-room.ready:
			if room.loadErr == nil {
				out = append(out, room)
			}
		default:
		}
	}
	return out
}

func (rs *Rooms) Shutdown(ctx context.Context) {
	rs.mu.Lock()
	rs.closing = true
	rs.mu.Unlock()
	for _, room := range rs.loaded() {
		room.mu.Lock()
		room.dropAll(wire.CloseMoved, rs.public)
		room.mu.Unlock()
	}
	for _, room := range rs.loaded() {
		go rs.unload(room)
	}
	for _, room := range rs.loaded() {
		select {
		case <-room.done:
		case <-ctx.Done():
			return
		}
	}
}
