package collab

import (
	"context"
	"sync"
	"time"

	"github.com/misty-org/misty-server/internal/collab/admit"
	"github.com/misty-org/misty-server/internal/collab/presence"
	"github.com/misty-org/misty-server/internal/collab/wire"
	"github.com/misty-org/misty-server/internal/collab/ydoc"
)

type Room struct {
	rooms *Rooms
	rec   Record

	ready   chan struct{}
	loadErr error
	done    chan struct{}

	mu         sync.Mutex
	doc        *ydoc.Doc
	sessions   []*Session
	pending    int
	presence   *presence.Presence
	gate       *sizeGate
	bindings   []Binding
	acl        int64
	purged     bool
	restarting bool
	forgotten  bool
	unloading  bool
	settle     chan struct{}
	released   bool
	stopUpdate func()
	lastEditor string

	save saver
}

func newRoom(rooms *Rooms, rec Record) *Room {
	return &Room{rooms: rooms, rec: rec, acl: rec.ACLVersion, ready: make(chan struct{}), done: make(chan struct{})}
}

func (r *Room) load(ctx context.Context) error {
	gc := r.rec.Options.GC
	doc := ydoc.New(gc)
	stored, err := r.rooms.store.LoadDocument(ctx, r.rec.ID)
	switch {
	case err == nil:
		loaded, loadErr := ydoc.Load(stored.State, gc)
		if loadErr != nil {
			r.log("document_snapshot_load_failed", map[string]any{"starts_empty": true, "error": loadErr.Error()})
		} else {
			doc = loaded
		}
	case err != ErrNoDocument:
		return err
	}
	r.doc = doc
	r.gate = &sizeGate{doc: doc}
	r.gate.measure()
	r.presence = presence.New()
	r.stopUpdate = doc.OnUpdate(r.updated)
	for _, factory := range r.rooms.bindings {
		binding, err := factory(ctx, r)
		if err != nil {
			r.stop()
			return err
		}
		if binding != nil {
			r.bindings = append(r.bindings, binding)
		}
	}
	return nil
}

func (r *Room) stop() {
	if r.stopUpdate != nil {
		r.stopUpdate()
	}
	for _, binding := range r.bindings {
		binding.Stop()
	}
	if r.presence != nil {
		r.presence.Stop()
	}
	r.save.stopTimers()
	if r.doc != nil {
		r.doc.Destroy()
	}
}

func (r *Room) RoomID() string   { return r.rec.ID }
func (r *Room) SpaceID() string  { return r.rec.SpaceID }
func (r *Room) Key() string      { return r.rec.Key }
func (r *Room) Options() Options { return r.rec.Options }
func (r *Room) Doc() *ydoc.Doc   { return r.doc }

func (r *Room) Sessions() []*Session { return append([]*Session(nil), r.sessions...) }

func (r *Room) Busy() bool { return len(r.sessions) > 0 || r.pending > 0 }

func (r *Room) Purged() bool { return r.purged }

func (r *Room) LastEditor() string { return r.lastEditor }

func (r *Room) Send(s *Session, frame wire.Frame) {
	if err := s.conn.Send(frame); err != nil {
		r.log("send_failed", map[string]any{"error": err.Error()})
	}
}

func (r *Room) Broadcast(frame wire.Frame, except *Session) {
	for _, s := range r.sessions {
		if s != except {
			r.Send(s, frame)
		}
	}
}

func (r *Room) Do(fn func()) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.released {
		return false
	}
	fn()
	return true
}

func (r *Room) After(delay time.Duration, fn func()) func() {
	timer := time.AfterFunc(delay, func() { r.Do(fn) })
	return func() { timer.Stop() }
}

func (r *Room) Log(event string, details map[string]any) { r.log(event, details) }

func (r *Room) log(event string, details map[string]any) {
	r.rooms.logRoom(r.rec.ID, event, details)
}

func (r *Room) updated(update []byte, origin any) {
	r.save.changes++
	r.save.retryStep = 0
	r.gate.appliedSince += len(update)
	sender, _ := origin.(*Session)
	r.Broadcast(wire.UpdateFrame(update), sender)
	r.scheduleSave()
	for _, binding := range r.bindings {
		binding.Changed()
	}
}

func (r *Room) ApplyLocal(update []byte, origin any) error {
	return r.doc.Apply(update, origin)
}

func (r *Room) join(s *Session, grant admit.Grant, acl int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending--
	s.mu.Lock()
	if s.state == sessionClosed {
		s.mu.Unlock()
		r.idleIfEmpty()
		return
	}
	s.mu.Unlock()
	if r.purged || r.released {
		s.refuse(wire.CloseGone, "the room was purged")
		r.idleIfEmpty()
		return
	}
	if len(r.sessions) >= Seats {
		s.refuse(wire.CloseFull, "all seats are taken")
		r.idleIfEmpty()
		return
	}
	s.grant, s.acl, s.room = grant, acl, r
	r.sessions = append(r.sessions, s)
	opened := wire.Opened{Type: "opened", Room: r.rec.ID, Role: grant.Role, Options: r.rec.Options.JSON(),
		Seats: Seats, MaxBytes: MaxDocumentBytes, Identity: grant.Who.Identity}
	r.Send(s, wire.TextFrame(opened))
	if vector, err := r.doc.StateVector(); err == nil {
		r.Send(s, wire.SyncStep1Frame(vector))
	}
	if states := r.presence.All(); states != nil {
		r.Send(s, wire.AwarenessFrame(states))
	}
	close(s.opened)
	for _, binding := range r.bindings {
		binding.Joined(s)
	}
	for {
		s.mu.Lock()
		if s.state == sessionClosed {
			s.mu.Unlock()
			return
		}
		if len(s.early) == 0 {
			s.state = sessionAdmitted
			s.mu.Unlock()
			break
		}
		next := s.early[0]
		s.early = s.early[1:]
		s.mu.Unlock()
		if !r.member(s) {
			return
		}
		r.receive(s, next)
	}
	if acl < r.acl {
		go r.rooms.recheck(r, s)
	}
}

func (r *Room) member(s *Session) bool {
	for _, held := range r.sessions {
		if held == s {
			return true
		}
	}
	return false
}

func (r *Room) left(s *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.leaveLocked(s)
}

func (r *Room) leaveLocked(s *Session) {
	index := -1
	for i, held := range r.sessions {
		if held == s {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	r.sessions = append(r.sessions[:index], r.sessions[index+1:]...)
	if removal := r.presence.Leave(s.ID()); removal != nil {
		r.Broadcast(wire.AwarenessFrame(removal), nil)
	}
	for _, binding := range r.bindings {
		binding.Left(s)
	}
	r.idleIfEmpty()
}

func (r *Room) drop(s *Session, code int, reason string) {
	r.leaveLocked(s)
	s.refuse(code, reason)
}

func (r *Room) dropAll(code int, reason string) {
	for _, s := range r.Sessions() {
		r.drop(s, code, reason)
	}
}

func (r *Room) idleIfEmpty() {
	if len(r.sessions) == 0 && r.pending == 0 && !r.unloading {
		go r.rooms.unload(r)
	}
}

func (r *Room) Restart(reason string) {
	r.restarting = true
	r.save.stopped = true
	r.save.stopTimers()
	r.dropAll(wire.CloseMoved, r.rooms.selfURL())
	r.log("file_room_reloaded", map[string]any{"reason": reason})
}

func (r *Room) Forget(ctx context.Context) error {
	if err := r.rooms.store.DeleteDocument(ctx, r.rec.ID); err != nil {
		return err
	}
	r.Do(func() { r.forgotten = true })
	return nil
}

func (r *Room) fail(err error) {
	r.log("room_failed", map[string]any{"error": err.Error()})
	r.purged = true
	r.save.stopped = true
	r.dropAll(wire.CloseInternal, "the room failed")
}
