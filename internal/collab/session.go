package collab

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/misty-org/misty-server/internal/collab/admit"
	"github.com/misty-org/misty-server/internal/collab/wire"
)

const admissionBound = 20 * time.Second

const (
	sessionWaiting = iota
	sessionAdmitting
	sessionAdmitted
	sessionClosed
)

type Session struct {
	rooms  *Rooms
	conn   Conn
	bearer string

	mu     sync.Mutex
	state  int
	early  []wire.Frame
	bytes  int
	opened chan struct{}

	room         *Room
	grant        admit.Grant
	acl          int64
	toldReadOnly bool
}

func (s *Session) ID() string { return s.conn.ID() }

func (s *Session) Role() string { return s.grant.Role }

func (s *Session) ReadOnly() bool { return s.grant.Role != admit.RoleEdit }

func (s *Session) Who() admit.Who { return s.grant.Who }

func (s *Session) UserID() string {
	if s.grant.Who.Kind == admit.KindPerson {
		return s.grant.Who.ID
	}
	return ""
}

func (s *Session) Actor() string { return s.grant.Who.Kind + ":" + s.grant.Who.ID }

func (s *Session) Opened() <-chan struct{} { return s.opened }

func (s *Session) Receive(frame wire.Frame) {
	s.mu.Lock()
	switch s.state {
	case sessionClosed:
		s.mu.Unlock()
		return
	case sessionWaiting:
		s.state = sessionAdmitting
		s.mu.Unlock()
		if !frame.Text {
			s.refuse(wire.CloseBadOpen, "the first message must be open")
			return
		}
		open, err := wire.ParseOpen(frame.Data)
		if err != nil {
			s.refuse(wire.CloseBadOpen, err.Error())
			return
		}
		go s.admit(open)
		return
	case sessionAdmitting:
		s.early = append(s.early, frame)
		s.bytes += len(frame.Data)
		over := len(s.early) > EarlyFrameLimit || s.bytes > EarlyByteLimit
		s.mu.Unlock()
		if over {
			s.refuse(wire.CloseTooLarge, "too much was sent before the room opened")
		}
		return
	}
	room := s.room
	s.mu.Unlock()
	room.receiveFrom(s, frame)
}

func (s *Session) Closed() {
	s.mu.Lock()
	previous := s.state
	s.state = sessionClosed
	room := s.room
	s.mu.Unlock()
	if previous == sessionAdmitted && room != nil {
		room.left(s)
	}
}

func (s *Session) closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == sessionClosed
}

func (s *Session) refuse(code int, reason string) {
	s.mu.Lock()
	if s.state == sessionClosed {
		s.mu.Unlock()
		return
	}
	s.state = sessionClosed
	s.early = nil
	s.mu.Unlock()
	if code == wire.CloseMoved {
		_ = s.conn.Send(wire.Moved(reason))
		reason = "moved"
	}
	s.rooms.countRefusal(code)
	s.conn.Close(code, reason)
}

func (s *Session) admit(open wire.Open) {
	ctx, cancel := context.WithTimeout(context.Background(), admissionBound)
	defer cancel()
	token := open.Token
	if token == "" {
		token = s.bearer
	}
	room, grant, acl, err := s.rooms.enter(ctx, token, open.Space, open.Key, open.Options)
	if err != nil {
		var moved *Moved
		if errors.As(err, &moved) {
			s.refuse(wire.CloseMoved, moved.URL)
			return
		}
		code := admit.CodeOf(err)
		if code == wire.CloseInternal {
			s.rooms.log("admission_failed", map[string]any{"error": err.Error()})
		}
		s.refuse(code, wire.CloseName(code))
		return
	}
	room.join(s, grant, acl)
}
