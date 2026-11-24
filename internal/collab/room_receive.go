package collab

import (
	"encoding/json"
	"errors"

	"github.com/misty-org/misty-server/internal/collab/presence"
	"github.com/misty-org/misty-server/internal/collab/wire"
	"github.com/misty-org/misty-server/internal/collab/ydoc"
)

func (r *Room) receiveFrom(s *Session, frame wire.Frame) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.member(s) {
		return
	}
	r.receive(s, frame)
}

func (r *Room) receive(s *Session, frame wire.Frame) {
	if r.purged {
		r.drop(s, wire.CloseGone, "the room was purged")
		return
	}
	if len(frame.Data) > wire.MaxFrameBytes {
		r.drop(s, wire.CloseTooLarge, "the frame is too large")
		return
	}
	if frame.Text {
		r.receiveText(s, frame.Data)
		return
	}
	kind, payload, err := wire.Kind(frame.Data)
	if err != nil {
		r.drop(s, wire.CloseBadFrame, "the frame could not be read")
		return
	}
	switch kind {
	case wire.MessageSync:
		r.receiveSync(s, payload)
	case wire.MessageAwareness:
		update, err := wire.AwarenessPayload(payload)
		if err != nil {
			r.drop(s, wire.CloseBadFrame, "the awareness update could not be read")
			return
		}
		relay, err := r.presence.Apply(presence.Owner{Conn: s.ID(), Person: s.Actor()}, s.grant.Who.Identity, update)
		if err != nil {
			r.drop(s, wire.CloseBadFrame, "the awareness update could not be read")
			return
		}
		if relay != nil {
			r.Broadcast(wire.AwarenessFrame(relay), s)
		}
	case wire.MessageAwarenessQuery:
		if states := r.presence.All(); states != nil {
			r.Send(s, wire.AwarenessFrame(states))
		}
	default:
		r.drop(s, wire.CloseBadFrame, "unknown message type")
	}
}

func (r *Room) receiveSync(s *Session, payload []byte) {
	step, data, err := wire.Sync(payload)
	if err != nil {
		r.drop(s, wire.CloseBadFrame, "the sync message could not be read")
		return
	}
	if step == wire.SyncStep1 {
		update, err := r.doc.UpdateSince(data)
		if err != nil {
			r.failOrRefuse(s, err)
			return
		}
		r.Send(s, wire.SyncStep2Frame(update))
		return
	}
	if s.ReadOnly() {
		if !s.toldReadOnly {
			s.toldReadOnly = true
			r.Send(s, wire.Error("read_only", "this connection can only view the room"))
		}
		return
	}
	if code, reason := r.edit(s, data, s); code != 0 {
		r.drop(s, code, reason)
	}
}

func (r *Room) edit(s *Session, update []byte, origin any) (int, string) {
	clients, err := ydoc.UpdateClients(update)
	if err != nil {
		return wire.CloseBadFrame, "the update could not be read"
	}
	for _, client := range clients {
		if client == 0 {
			return wire.CloseBadFrame, "client id 0 is not allowed"
		}
	}
	for _, binding := range r.bindings {
		if binding.RefusesUpdates() {
			return 0, ""
		}
	}
	if gate := r.gate.check(update); gate != nil {
		if s != nil {
			r.Send(s, statusFrame(gate.status, gate.projectedBytes, ""))
		}
		if gate.status == "blocked" {
			r.log("document_limit_blocked", map[string]any{"projected_bytes": gate.projectedBytes})
			if s == nil {
				return wire.CloseTooLarge, "the document would pass max_bytes"
			}
			return 0, ""
		}
	}
	if s != nil {
		r.lastEditor = s.Actor()
	}
	if err := r.doc.Apply(update, origin); err != nil {
		if errors.Is(err, ydoc.ErrPanic) {
			r.fail(err)
			return 0, ""
		}
		return wire.CloseBadFrame, "the update could not be applied"
	}
	return 0, ""
}

func (r *Room) failOrRefuse(s *Session, err error) {
	if errors.Is(err, ydoc.ErrPanic) {
		r.fail(err)
		return
	}
	r.drop(s, wire.CloseBadFrame, "the message could not be read")
}

func (r *Room) receiveText(s *Session, data []byte) {
	if !json.Valid(data) {
		r.drop(s, wire.CloseBadFrame, "a text frame must be JSON")
		return
	}
	message, fields, ok := wire.ParseOption(data)
	if !ok {
		return
	}
	for _, binding := range r.bindings {
		if binding.Option() == message.Option {
			binding.Message(s, message, fields)
			return
		}
	}
	reply := map[string]any{"option": message.Option, "type": "error", "code": "unknown_option"}
	if message.Request != nil {
		reply["request"] = *message.Request
	}
	r.Send(s, wire.TextFrame(reply))
}
