package ydoc

import "testing"

type restoreExpect struct {
	Name   string                `json:"name"`
	GC     bool                  `json:"gc"`
	A      string                `json:"a"`
	B      string                `json:"b"`
	RootsA map[string]rootExpect `json:"rootsA"`
	RootsB map[string]rootExpect `json:"rootsB"`
}

func loadRestoreCases(t *testing.T) []restoreExpect {
	var cases []restoreExpect
	readJSON(t, "restore.json", &cases)
	if len(cases) == 0 {
		t.Fatal("no restore cases")
	}
	return cases
}

func checkKinds(t *testing.T, label string, update []byte, want map[string]rootExpect) {
	t.Helper()
	roots, err := RootsOf(update)
	if err != nil {
		t.Fatalf("%s: RootsOf: %v", label, err)
	}
	found := map[string]Kind{}
	for _, root := range roots {
		found[root.Name] = root.Kind
	}
	for name, kind := range found {
		exp, ok := want[name]
		if !ok {
			t.Errorf("%s: unexpected root %q (%s)", label, name, kind)
			continue
		}
		if kind != exp.Kind {
			t.Errorf("%s: root %q is %s, want %s", label, name, kind, exp.Kind)
		}
	}
	for name, exp := range want {
		if _, ok := found[name]; !ok && !exp.Empty {
			t.Errorf("%s: root %q (%s) not found", label, name, exp.Kind)
		}
	}
}

func TestRestoreUndeclaredRoots(t *testing.T) {
	for _, c := range loadRestoreCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			a := readFixture(t, c.A)
			b := readFixture(t, c.B)
			checkKinds(t, "version a", a, c.RootsA)
			checkKinds(t, "version b", b, c.RootsB)

			doc := mustLoad(t, b, c.GC)
			defer doc.Destroy()
			checkRoots(t, "b before restore", doc, c.RootsB)

			mirror := mustLoad(t, b, c.GC)
			defer mirror.Destroy()
			var sent [][]byte
			stop := doc.OnUpdate(func(update []byte, origin any) {
				if origin != "restore" {
					t.Errorf("update origin %v, want restore", origin)
				}
				sent = append(sent, append([]byte(nil), update...))
			})
			if err := doc.Restore(a, "restore"); err != nil {
				t.Fatalf("restore: %v", err)
			}
			stop()
			checkRoots(t, "restored", doc, c.RootsA)
			if len(sent) == 0 {
				t.Fatal("restore emitted no update")
			}
			for _, update := range sent {
				if err := mirror.Apply(update, nil); err != nil {
					t.Fatal(err)
				}
			}
			checkRoots(t, "peer after restore update", mirror, c.RootsA)

			reloaded := mustLoad(t, mustUpdate(t, doc), c.GC)
			defer reloaded.Destroy()
			checkRoots(t, "restored and reloaded", reloaded, c.RootsA)

			if err := doc.Restore(b, nil); err != nil {
				t.Fatalf("restore back: %v", err)
			}
			checkRoots(t, "restored back to b", doc, c.RootsB)

			fresh := New(c.GC)
			defer fresh.Destroy()
			if err := fresh.Restore(a, nil); err != nil {
				t.Fatalf("restore into empty: %v", err)
			}
			checkRoots(t, "restore into empty doc", fresh, c.RootsA)
		})
	}
}
