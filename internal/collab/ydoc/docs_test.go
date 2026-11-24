package ydoc

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"
)

func TestClearEmptiesEveryRoot(t *testing.T) {
	for _, c := range loadCases(t) {
		if c.Name == "big" {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			doc := mustLoad(t, readFixture(t, c.File), c.GC)
			defer doc.Destroy()
			before := mustFingerprint(t, doc)
			if err := doc.Clear("clear"); err != nil {
				t.Fatal(err)
			}
			for _, d := range []*Doc{doc, mustLoad(t, mustUpdate(t, doc), c.GC)} {
				roots, err := d.Roots()
				if err != nil {
					t.Fatal(err)
				}
				if len(roots) != 0 {
					t.Errorf("roots left after Clear: %v", roots)
				}
				got, err := d.JSON()
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != 0 {
					t.Errorf("JSON after Clear: %v", got)
				}
				for name, root := range c.Roots {
					switch root.Kind {
					case KindText:
						text, _ := d.Text(name)
						if text.Len() != 0 {
							t.Errorf("text %q still has %q", name, text.String())
						}
					case KindMap:
						m, _ := d.Map(name)
						if keys := m.Keys(); len(keys) != 0 {
							t.Errorf("map %q still has %v", name, keys)
						}
					case KindArray:
						a, _ := d.Array(name)
						if a.Len() != 0 {
							t.Errorf("array %q still has %d items", name, a.Len())
						}
					case KindXML:
						x, _ := d.XML(name)
						if x.Len() != 0 {
							t.Errorf("xml %q still has %q", name, x.String())
						}
					}
				}
			}
			hasContent := false
			for _, root := range c.Roots {
				hasContent = hasContent || !root.Empty
			}
			if hasContent && mustFingerprint(t, doc) == before {
				t.Error("Clear left the fingerprint as it was")
			}
		})
	}
}

func TestJSONShape(t *testing.T) {
	doc := New(true)
	defer doc.Destroy()
	if err := doc.Apply(readFixture(t, "restore_mixed_gc_a.bin"), nil); err != nil {
		t.Fatal(err)
	}
	got, err := doc.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("JSON roots: %v", got)
	}
	if text, ok := got["text"].(string); !ok || text != "text A" {
		t.Errorf("text root: %#v", got["text"])
	}
	if xml, ok := got["xml"].(string); !ok || xml != "<p>xml A</p>" {
		t.Errorf("xml root: %#v", got["xml"])
	}
	if raw, ok := got["map"].(json.RawMessage); !ok || string(raw) != `{"a":1}` {
		t.Errorf("map root: %#v", got["map"])
	}
	if raw, ok := got["list"].(json.RawMessage); !ok || string(raw) != `["a"]` {
		t.Errorf("array root: %#v", got["list"])
	}
	empty, err := New(true).JSON()
	if err != nil || len(empty) != 0 {
		t.Errorf("empty doc JSON: %v %v", empty, err)
	}
	x, _ := doc.XML("xml")
	if x.String() != "<p>xml A</p>" {
		t.Errorf("XML.String: %q", x.String())
	}
	m, _ := doc.Map("map")
	if raw, err := m.JSON(); err != nil || string(raw) != `{"a":1}` {
		t.Errorf("Map.JSON: %s %v", raw, err)
	}
	a, _ := doc.Array("list")
	if raw, err := a.JSON(); err != nil || string(raw) != `["a"]` {
		t.Errorf("Array.JSON: %s %v", raw, err)
	}
}

func TestClientZero(t *testing.T) {
	zero := readFixture(t, "client_zero.bin")
	clients, err := UpdateClients(zero)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(clients, []uint64{0}) {
		t.Errorf("UpdateClients = %v, want [0]", clients)
	}
	mixed := readFixture(t, "client_zero_mixed.bin")
	clients, err = UpdateClients(mixed)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(clients)
	if !slices.Equal(clients, []uint64{0, 77}) {
		t.Errorf("UpdateClients = %v, want [0 77]", clients)
	}
	merged, err := Merge(zero, readFixture(t, "text_basic_gc.bin"))
	if err != nil {
		t.Fatal(err)
	}
	clients, err = UpdateClients(merged)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(clients)
	if !slices.Equal(clients, []uint64{0, 1}) {
		t.Errorf("merged UpdateClients = %v, want [0 1]", clients)
	}
	for _, c := range loadCases(t) {
		if c.Name == "big" || c.Name == "empty" {
			continue
		}
		clients, err := UpdateClients(readFixture(t, c.File))
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(clients, 0) != slices.Contains(c.Clients, 0) {
			t.Errorf("%s: UpdateClients %v, yjs %v", c.Name, clients, c.Clients)
		}
	}
	if clients, err := UpdateClients(readFixture(t, "empty.bin")); err != nil || len(clients) != 0 {
		t.Errorf("empty update clients: %v %v", clients, err)
	}
}

func TestGCOffKeepsDeletedContent(t *testing.T) {
	edit := func(doc *Doc) {
		err := doc.Change(nil, func(tx *Tx) {
			tx.Text("t").Insert(tx, 0, "kept SECRETWORD kept", nil)
			tx.Map("m").Set(tx, "k", "OLDVALUE")
		})
		if err != nil {
			t.Fatal(err)
		}
		err = doc.Change(nil, func(tx *Tx) {
			tx.Text("t").Delete(tx, 5, 11)
			tx.Map("m").Set(tx, "k", "new")
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	keep := New(false)
	defer keep.Destroy()
	edit(keep)
	drop := New(true)
	defer drop.Destroy()
	edit(drop)
	kept := mustUpdate(t, keep)
	dropped := mustUpdate(t, drop)
	for _, word := range []string{"SECRETWORD", "OLDVALUE"} {
		if !bytes.Contains(kept, []byte(word)) {
			t.Errorf("gc:false update lost %q", word)
		}
		if bytes.Contains(dropped, []byte(word)) {
			t.Errorf("gc:true update kept %q", word)
		}
	}
	reloaded := mustLoad(t, kept, false)
	defer reloaded.Destroy()
	if !bytes.Contains(mustUpdate(t, reloaded), []byte("SECRETWORD")) {
		t.Error("reloading a gc:false update lost the deleted text")
	}
	text, _ := reloaded.Text("t")
	if text.String() != "kept kept" {
		t.Errorf("text %q", text.String())
	}
	if keep.GC() || !drop.GC() {
		t.Error("GC() reports the wrong setting")
	}

	cases := map[string]caseExpect{}
	for _, c := range loadCases(t) {
		cases[c.Name] = c
	}
	for _, name := range []string{"text_basic", "text_two_clients", "map_basic", "array_basic", "xml", "nested", "many_clients"} {
		on, off := cases[name+"_gc"], cases[name+"_nogc"]
		if on.Fingerprint != off.Fingerprint {
			t.Errorf("%s: yjs fingerprints differ between gc on and off", name)
		}
		if off.Size < on.Size {
			t.Errorf("%s: gc:false update is smaller than gc:true", name)
		}
		goOn := mustFingerprint(t, mustLoad(t, readFixture(t, on.File), true))
		goOff := mustFingerprint(t, mustLoad(t, readFixture(t, off.File), false))
		if goOn != on.Fingerprint || goOff != off.Fingerprint {
			t.Errorf("%s: Go fingerprints %s/%s, yjs %s/%s", name, goOn, goOff, on.Fingerprint, off.Fingerprint)
		}
	}
}
