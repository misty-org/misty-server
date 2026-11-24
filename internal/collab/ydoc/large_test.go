package ydoc

import (
	"bytes"
	"testing"
)

func TestLargeDocumentAtCeiling(t *testing.T) {
	if testing.Short() {
		t.Skip("large document")
	}
	c := findCase(t, "big")
	update := readFixture(t, c.File)
	if sha(update) != c.SHA256 {
		t.Fatal("big fixture does not match its hash")
	}
	if len(update) < 8*1024*1024-128*1024 || len(update) > 8*1024*1024 {
		t.Fatalf("big fixture is %d bytes, want just under 8 MiB", len(update))
	}
	t.Logf("%d bytes from %d yjs edits", len(update), c.Ops)
	checkCase(t, c, update)

	current := update
	for round := 0; round < 3; round++ {
		doc := mustLoad(t, current, true)
		next := mustUpdate(t, doc)
		if !bytes.Equal(next, current) {
			t.Fatalf("round %d: reload changed the bytes", round)
		}
		if fp := mustFingerprint(t, doc); fp != c.Fingerprint {
			t.Fatalf("round %d: fingerprint %s, want %s", round, fp, c.Fingerprint)
		}
		doc.Destroy()
		current = next
	}
}

func TestLongEditingSessionReloads(t *testing.T) {
	c := findCase(t, "heavy_gc")
	doc := mustLoad(t, readFixture(t, c.File), true)
	peer := mustLoad(t, readFixture(t, c.File), true)
	var sent [][]byte
	stop := doc.OnUpdate(func(update []byte, _ any) { sent = append(sent, append([]byte(nil), update...)) })
	seed := uint32(17)
	next := func(n int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>8) % n
	}
	words := []string{"one ", "two ", "three ", "😀", "日本", "\n"}
	for round := 0; round < 40; round++ {
		for i := 0; i < 50; i++ {
			err := doc.Change(nil, func(tx *Tx) {
				text := tx.Text("text")
				n := text.Len()
				switch op := next(10); {
				case op < 5:
					text.Insert(tx, next(n+1), words[next(len(words))], nil)
				case op < 7 && n > 10:
					at := next(n - 5)
					text.Delete(tx, at, 1+next(4))
				case op < 9:
					tx.Map("map").Set(tx, "key"+string(rune('a'+next(26))), float64(next(1000))/7)
				default:
					arr := tx.Array("array")
					if arr.Len() > 0 && next(2) == 0 {
						arr.Delete(tx, next(arr.Len()), 1)
					} else {
						arr.Insert(tx, next(arr.Len()+1), []any{round, "x"})
					}
				}
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		update := mustUpdate(t, doc)
		reloaded := mustLoad(t, update, true)
		if !bytes.Equal(mustUpdate(t, reloaded), update) {
			t.Fatalf("round %d: reload changed the bytes", round)
		}
		if mustFingerprint(t, reloaded) != mustFingerprint(t, doc) {
			t.Fatalf("round %d: reload changed the fingerprint", round)
		}
		reloaded.Destroy()
	}
	stop()
	for _, update := range sent {
		if err := peer.Apply(update, nil); err != nil {
			t.Fatal(err)
		}
	}
	if mustFingerprint(t, peer) != mustFingerprint(t, doc) {
		t.Error("a peer fed every incremental update ended in another state")
	}
	a, _ := doc.Text("text")
	b, _ := peer.Text("text")
	if a.String() != b.String() {
		t.Error("peer text differs")
	}
}
