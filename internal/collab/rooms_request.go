package collab

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/misty-org/misty-server/internal/collab/admit"
	"github.com/misty-org/misty-server/internal/collab/wire"
	"github.com/misty-org/misty-server/internal/collab/ydoc"
)

type Content struct {
	Update []byte
	JSON   map[string]any
}

func (rs *Rooms) Read(ctx context.Context, token, spaceID, key string, asJSON bool) (Content, error) {
	rec, _, err := rs.authorize(ctx, token, spaceID, key, nil, false)
	if err != nil {
		return Content{}, err
	}
	rs.mu.Lock()
	room := rs.rooms[rec.ID]
	rs.mu.Unlock()
	if room != nil {
		var content Content
		var readErr error
		served := false
		select {
		case <-room.ready:
			served = room.loadErr == nil && room.Do(func() {
				content, readErr = contentOf(room.doc, asJSON)
			})
		default:
		}
		if served {
			return content, readErr
		}
	}
	claimed, err := rs.store.ClaimRoom(ctx, rec.ID)
	if err != nil {
		return Content{}, err
	}
	if !claimed {
		owner := rs.public
		if current, err := rs.store.RoomByID(ctx, rec.ID); err == nil && current.Owner != "" {
			owner = current.Owner
		}
		return Content{}, &Moved{URL: owner}
	}
	defer func() { _ = rs.store.ReleaseRoom(context.Background(), rec.ID) }()
	doc := ydoc.New(rec.Options.GC)
	stored, err := rs.store.LoadDocument(ctx, rec.ID)
	if err == nil {
		if doc, err = ydoc.Load(stored.State, rec.Options.GC); err != nil {
			return Content{}, err
		}
	} else if !errors.Is(err, ErrNoDocument) {
		return Content{}, err
	}
	defer doc.Destroy()
	return contentOf(doc, asJSON)
}

func contentOf(doc *ydoc.Doc, asJSON bool) (Content, error) {
	if asJSON {
		values, err := doc.JSON()
		return Content{JSON: values}, err
	}
	update, err := doc.Update()
	if err == nil && len(update) > MaxDocumentBytes {
		return Content{}, admit.Refuse(wire.CloseTooLarge, "document_too_large")
	}
	return Content{Update: update}, err
}

func (rs *Rooms) Change(ctx context.Context, token, spaceID, key string, update []byte, options json.RawMessage) error {
	if len(update) > wire.MaxFrameBytes {
		return admit.Refuse(wire.CloseTooLarge, "the update is too large")
	}
	rec, grant, err := rs.authorize(ctx, token, spaceID, key, options, true)
	if err != nil {
		return err
	}
	if grant.Role != admit.RoleEdit {
		return admit.Refuse(wire.CloseForbidden, "this token can only view the room")
	}
	room, err := rs.acquire(ctx, rec)
	if err != nil {
		return err
	}
	var code int
	var reason string
	room.Do(func() {
		room.pending--
		if room.purged {
			code, reason = wire.CloseGone, "the room was purged"
			return
		}
		room.lastEditor = grant.Who.Kind + ":" + grant.Who.ID
		code, reason = room.edit(nil, update, grant.Who.Kind+":"+grant.Who.ID)
	})
	saveCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if code == 0 {
		if err := room.Save(saveCtx); err != nil && !errors.Is(err, ErrStopped) {
			room.log("save_after_change_failed", map[string]any{"error": err.Error()})
		}
	}
	room.Do(func() { room.idleIfEmpty() })
	if code != 0 {
		return admit.Refuse(code, reason)
	}
	return nil
}
