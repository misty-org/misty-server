package collab

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/misty-org/misty-server/internal/collab/admit"
	"github.com/misty-org/misty-server/internal/collab/wire"
)

const (
	SignalMemberRemoved      = "member.removed"
	SignalMemberLeft         = "member.left"
	SignalSpaceDeleted       = "space.deleted"
	SignalAccessChanged      = "collab.access_changed"
	SignalPurged             = "collab.purged"
	SignalSessionEnded       = "collab.session_ended"
	SignalIntegrationEnded   = "collab.integration_ended"
	SignalIntegrationChanged = "collab.integration_changed"
	SignalAccessRecheck      = "collab.access_recheck"
)

type signal struct {
	Type          string   `json:"type"`
	Kind          string   `json:"kind"`
	SpaceID       string   `json:"space_id"`
	UserIDs       []string `json:"user_ids"`
	SessionID     string   `json:"session_id"`
	IntegrationID string   `json:"integration_id"`
	RoomID        string   `json:"room_id"`
}

func (rs *Rooms) Control(payload []byte) {
	var event signal
	if json.Unmarshal(payload, &event) != nil {
		return
	}
	kind := event.Type
	if kind == "" {
		kind = event.Kind
	}
	switch kind {
	case SignalMemberRemoved, SignalMemberLeft:
		users := map[string]bool{}
		for _, userID := range event.UserIDs {
			users[userID] = true
		}
		rs.closeWhere(event.SpaceID, wire.CloseAccessChanged, "access_changed", func(s *Session) bool {
			return s.Who().Kind == admit.KindPerson && users[s.Who().ID]
		})
	case SignalSpaceDeleted:
		rs.purge(func(room *Room) bool { return room.rec.SpaceID == event.SpaceID && event.SpaceID != "" })
	case SignalPurged:
		rs.purge(func(room *Room) bool { return room.rec.ID == event.RoomID && event.RoomID != "" })
	case SignalAccessChanged, SignalAccessRecheck:
		for _, room := range rs.loaded() {
			if kind == SignalAccessChanged && (room.rec.SpaceID != event.SpaceID || event.SpaceID == "") {
				continue
			}
			var sessions []*Session
			room.Do(func() {
				room.acl++
				sessions = room.Sessions()
			})
			for _, s := range sessions {
				go rs.recheck(room, s)
			}
		}
	case SignalSessionEnded:
		rs.closeWhere("", wire.CloseSignedOut, "signed_out", func(s *Session) bool {
			return event.SessionID != "" && s.Who().Kind == admit.KindPerson && s.Who().SessionID == event.SessionID
		})
	case SignalIntegrationEnded, SignalIntegrationChanged:
		code, reason := wire.CloseSignedOut, "signed_out"
		if kind == SignalIntegrationChanged {
			code, reason = wire.CloseReauth, "reauth"
		}
		rs.closeWhere("", code, reason, func(s *Session) bool {
			return event.IntegrationID != "" && s.Who().Kind == admit.KindIntegration && s.Who().ID == event.IntegrationID
		})
	}
}

func (rs *Rooms) closeWhere(spaceID string, code int, reason string, match func(s *Session) bool) {
	for _, room := range rs.loaded() {
		if spaceID != "" && room.rec.SpaceID != spaceID {
			continue
		}
		room.Do(func() {
			for _, s := range room.Sessions() {
				if match(s) {
					room.drop(s, code, reason)
				}
			}
		})
	}
}

func (rs *Rooms) purge(match func(room *Room) bool) {
	for _, room := range rs.loaded() {
		if !match(room) {
			continue
		}
		room.Do(func() {
			room.purged = true
			room.save.stopped = true
			room.save.stopTimers()
			room.dropAll(wire.CloseGone, "gone")
		})
	}
}

func (rs *Rooms) recheck(room *Room, s *Session) {
	ctx, cancel := context.WithTimeout(context.Background(), recheckBound)
	defer cancel()
	var acl int64
	room.Do(func() { acl = room.acl })
	grant, err := rs.gate.Check(ctx, s.Who(), room.rec.SpaceID, room.rec.Key, room.rec.Options.File)
	room.Do(func() {
		if !room.member(s) {
			return
		}
		var refusal *admit.Refusal
		if errors.As(err, &refusal) || (err == nil && admit.Lower(s.Role(), grant.Role)) {
			room.drop(s, wire.CloseAccessChanged, "access_changed")
			return
		}
		if err != nil {
			room.log("access_recheck_failed", map[string]any{"error": err.Error()})
			return
		}
		if s.acl < acl {
			s.acl = acl
		}
		if s.acl < room.acl {
			go rs.recheck(room, s)
		}
	})
}
