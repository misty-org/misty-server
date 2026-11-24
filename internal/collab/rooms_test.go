package collab

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/reearth/ygo/crdt"

	"github.com/misty-org/misty-server/internal/collab/admit"
	"github.com/misty-org/misty-server/internal/collab/wire"
	"github.com/misty-org/misty-server/internal/collab/ydoc"
)

func textUpdate(t *testing.T, text string) []byte {
	t.Helper()
	doc := ydoc.New(true)
	if err := doc.Change(nil, func(tx *ydoc.Tx) { tx.Text("content").Insert(tx, 0, text, nil) }); err != nil {
		t.Fatal(err)
	}
	update, err := doc.Update()
	if err != nil {
		t.Fatal(err)
	}
	return update
}

func syncUpdate(update []byte) wire.Frame { return wire.UpdateFrame(update) }

func TestOpenAnswersOpenedThenSyncStepOne(t *testing.T) {
	w := newWorld(t)
	conn, _ := w.join(t, "c1", "person-ana", "board", nil)
	opened := conn.text(t, "opened")
	if opened["role"] != admit.RoleEdit || opened["seats"] != float64(Seats) || opened["max_bytes"] != float64(MaxDocumentBytes) {
		t.Fatalf("opened %v", opened)
	}
	identity, _ := opened["identity"].(map[string]any)
	if identity["kind"] != admit.KindPerson || identity["id"] != "ana" {
		t.Fatalf("identity %v", identity)
	}
	frame := conn.next(t)
	kind, payload, err := wire.Kind(frame.Data)
	if err != nil || kind != wire.MessageSync {
		t.Fatalf("frame after opened %v %v", kind, err)
	}
	if step, _, err := wire.Sync(payload); err != nil || step != wire.SyncStep1 {
		t.Fatalf("not sync step 1: %v %v", step, err)
	}
}

func TestRefusals(t *testing.T) {
	w := newWorld(t)
	binary := newFakeConn("binary")
	w.rooms.Open(binary, "").Receive(wire.BinaryFrame([]byte{0, 0, 0}))
	if code := binary.waitClosed(t); code != wire.CloseBadOpen {
		t.Fatalf("binary open closed with %d", code)
	}
	cases := []struct {
		token, key string
		options    any
		want       int
	}{
		{"nobody", "board", nil, wire.CloseUnauthorized},
		{"person-eve", "board", nil, wire.CloseForbidden},
		{"person-ana", "", nil, wire.CloseBadOpen},
		{"integration-one", "board", nil, wire.CloseForbidden},
		{"integration-one", "bot:board", map[string]any{"file": "drv_a"}, wire.CloseForbidden},
		{"person-ana", "missing-file", map[string]any{"file": "drv_missing"}, wire.CloseNotFound},
	}
	for i, c := range cases {
		conn, _ := w.join(t, fmt.Sprint("c", i), c.token, c.key, c.options)
		if code := conn.waitClosed(t); code != c.want {
			t.Errorf("%s %q closed with %d, want %d", c.token, c.key, code, c.want)
		}
	}
	first, _ := w.join(t, "history", "person-ana", "doc", map[string]any{"history": true})
	first.text(t, "opened")
	conflict, _ := w.join(t, "conflict", "person-sam", "doc", map[string]any{"history": false})
	if code := conflict.waitClosed(t); code != wire.CloseOptionsConflict {
		t.Fatalf("a conflicting open closed with %d", code)
	}
	same, _ := w.join(t, "same", "person-sam", "doc", nil)
	same.text(t, "opened")
}

func TestAllSeatsTaken(t *testing.T) {
	w := newWorld(t)
	for i := 0; i < Seats; i++ {
		conn, _ := w.join(t, fmt.Sprint("seat", i), "person-ana", "full", nil)
		conn.text(t, "opened")
	}
	extra, _ := w.join(t, "extra", "person-sam", "full", nil)
	if code := extra.waitClosed(t); code != wire.CloseFull {
		t.Fatalf("the 41st connection closed with %d", code)
	}
}

func TestMovedToTheOwner(t *testing.T) {
	w := newWorld(t)
	rec, _ := w.store.CreateRoom(context.Background(), "sp_one", "owned", Options{GC: true})
	_ = w.store.SetRoomOwner(context.Background(), rec.ID, "https://owner.example")
	w.store.refuse = true
	conn, _ := w.join(t, "moved", "person-ana", "owned", nil)
	if code := conn.waitClosed(t); code != wire.CloseMoved {
		t.Fatalf("closed with %d", code)
	}
	if moved := conn.text(t, "moved"); moved["url"] != "https://owner.example" {
		t.Fatalf("moved %v", moved)
	}
}

func TestTooMuchBeforeAdmission(t *testing.T) {
	w := newWorld(t)
	w.hold = make(chan struct{})
	conn := newFakeConn("early")
	session := w.rooms.Open(conn, "")
	session.Receive(openFrame(t, "person-ana", "early", nil))
	for i := 0; i <= EarlyFrameLimit; i++ {
		session.Receive(wire.BinaryFrame([]byte{3}))
	}
	close(w.hold)
	if code := conn.waitClosed(t); code != wire.CloseTooLarge {
		t.Fatalf("closed with %d", code)
	}
}

func TestEarlyFramesApplyAfterAdmission(t *testing.T) {
	w := newWorld(t)
	w.hold = make(chan struct{})
	writer := newFakeConn("writer")
	session := w.rooms.Open(writer, "")
	session.Receive(openFrame(t, "person-ana", "early-apply", nil))
	session.Receive(syncUpdate(textUpdate(t, "hello")))
	close(w.hold)
	writer.text(t, "opened")
	w.hold = nil
	for start := time.Now(); time.Since(start) < 5*time.Second; time.Sleep(20 * time.Millisecond) {
		content, err := w.rooms.Read(context.Background(), "person-sam", "sp_one", "early-apply", true)
		if err == nil && content.JSON["content"] == "hello" {
			session.Closed()
			return
		}
	}
	t.Fatal("the frame sent before admission was not applied")
}

func TestViewersAreToldOnceAndClientZeroIsRefused(t *testing.T) {
	w := newWorld(t)
	w.files["drv_view"] = admit.FileAccess{Read: true}
	viewer, session := w.join(t, "viewer", "person-ana", "drv_view", map[string]any{"file": "drv_view"})
	if opened := viewer.text(t, "opened"); opened["role"] != admit.RoleView {
		t.Fatalf("role %v", opened["role"])
	}
	session.Receive(syncUpdate(textUpdate(t, "a")))
	session.Receive(syncUpdate(textUpdate(t, "b")))
	if failure := viewer.text(t, "error"); failure["code"] != "read_only" {
		t.Fatalf("error %v", failure)
	}
	zeroDoc := crdt.New(crdt.WithClientID(0))
	text := zeroDoc.GetText("content")
	zeroDoc.Transact(func(txn *crdt.Transaction) { text.Insert(txn, 0, "zero", nil) })
	editor, editing := w.join(t, "editor", "person-sam", "plain", nil)
	editor.text(t, "opened")
	editing.Receive(syncUpdate(zeroDoc.EncodeStateAsUpdate()))
	if code := editor.waitClosed(t); code != wire.CloseBadFrame {
		t.Fatalf("client 0 closed with %d", code)
	}
}

func TestEditsReachOthersAndAreSavedWhenTheLastLeaves(t *testing.T) {
	w := newWorld(t)
	first, firstSession := w.join(t, "first", "person-ana", "shared", nil)
	first.text(t, "opened")
	second, secondSession := w.join(t, "second", "person-sam", "shared", nil)
	second.text(t, "opened")
	firstSession.Receive(syncUpdate(textUpdate(t, "hello")))
	deadline := time.After(5 * time.Second)
	for got := false; !got; {
		select {
		case frame := <-second.frames:
			if kind, payload, err := wire.Kind(frame.Data); err == nil && kind == wire.MessageSync && !frame.Text {
				if step, _, _ := wire.Sync(payload); step == wire.SyncUpdate {
					got = true
				}
			}
		case <-deadline:
			t.Fatal("the edit did not reach the other connection")
		}
	}
	firstSession.Closed()
	secondSession.Closed()
	rec, _ := w.store.FindRoom(context.Background(), "sp_one", "shared")
	for start := time.Now(); time.Since(start) < 5*time.Second; time.Sleep(20 * time.Millisecond) {
		if saved, ok := w.store.document(rec.ID); ok {
			doc, err := ydoc.Load(saved.State, true)
			if err != nil {
				t.Fatal(err)
			}
			text, _ := doc.Text("content")
			if text.String() != "hello" {
				t.Fatalf("saved %q", text.String())
			}
			return
		}
	}
	t.Fatal("the room was not saved when the last connection left")
}

func TestControlClosesConnections(t *testing.T) {
	w := newWorld(t)
	removed, _ := w.join(t, "removed", "person-ana", "control", nil)
	removed.text(t, "opened")
	signedOut, _ := w.join(t, "signed-out", "person-sam", "control", nil)
	signedOut.text(t, "opened")
	bot, _ := w.join(t, "bot", "integration-one", "bot:control", nil)
	bot.text(t, "opened")
	w.rooms.Control([]byte(`{"type":"member.removed","space_id":"sp_one","user_ids":["ana"]}`))
	if code := removed.waitClosed(t); code != wire.CloseAccessChanged {
		t.Fatalf("member removed closed with %d", code)
	}
	w.rooms.Control([]byte(`{"kind":"collab.session_ended","session_id":"ses_sam"}`))
	if code := signedOut.waitClosed(t); code != wire.CloseSignedOut {
		t.Fatalf("session ended closed with %d", code)
	}
	w.rooms.Control([]byte(`{"kind":"collab.integration_changed","integration_id":"one"}`))
	if code := bot.waitClosed(t); code != wire.CloseReauth {
		t.Fatalf("integration changed closed with %d", code)
	}
	again, _ := w.join(t, "again", "person-sam", "control", nil)
	again.text(t, "opened")
	w.members["sam"] = false
	w.rooms.Control([]byte(`{"type":"collab.access_changed","space_id":"sp_one"}`))
	if code := again.waitClosed(t); code != wire.CloseAccessChanged {
		t.Fatalf("access changed closed with %d", code)
	}
}

func TestShutdownHandsRoomsBack(t *testing.T) {
	w := newWorld(t)
	conn, _ := w.join(t, "held", "person-ana", "handback", nil)
	conn.text(t, "opened")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.rooms.Shutdown(ctx)
	if code := conn.waitClosed(t); code != wire.CloseMoved {
		t.Fatalf("closed with %d", code)
	}
	if moved := conn.text(t, "moved"); moved["url"] != "https://public.example" {
		t.Fatalf("moved %v", moved)
	}
	w.store.mu.Lock()
	held := len(w.store.held)
	w.store.mu.Unlock()
	if held != 0 {
		t.Fatalf("%d rooms are still held", held)
	}
	late, _ := w.join(t, "late", "person-ana", "handback", nil)
	if code := late.waitClosed(t); code != wire.CloseMoved {
		t.Fatalf("a connection after shutdown closed with %d", code)
	}
}

func TestRequestReadAndChange(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	if _, err := w.rooms.Read(ctx, "person-ana", "sp_one", "never", false); err != ErrNoRoom {
		t.Fatalf("reading a room that was never opened: %v", err)
	}
	if err := w.rooms.Change(ctx, "person-ana", "sp_one", "scripted", textUpdate(t, "from a script"), nil); err != nil {
		t.Fatal(err)
	}
	content, err := w.rooms.Read(ctx, "person-sam", "sp_one", "scripted", true)
	if err != nil || content.JSON["content"] != "from a script" {
		t.Fatalf("read %v %v", content.JSON, err)
	}
	if err := w.rooms.Change(ctx, "nobody", "sp_one", "scripted", textUpdate(t, "x"), nil); admit.CodeOf(err) != wire.CloseUnauthorized {
		t.Fatalf("an unknown token changed the room: %v", err)
	}
}
