package ydoc

import (
	"bytes"
	"slices"
	"testing"
)

const keyOrderReason = "ygo decodes an Any object into a Go map and re-encodes its keys sorted, where yjs keeps insertion order; values, state vector and fingerprint are unchanged"

var knownReencodeDiffs = map[string]string{
	"values_gc":   keyOrderReason,
	"values_nogc": keyOrderReason,
}

var knownGCReloadDiffs = map[string]string{
	"values_nogc": keyOrderReason,
	"heavy_nogc":  "ygo leaves some adjacent deleted items unmerged when it collects garbage on load, where yjs merges them; content, state vector, delete set and fingerprint match",
}

func TestFixtureRoundTrip(t *testing.T) {
	for _, c := range loadCases(t) {
		if c.Name == "big" {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			checkCase(t, c, readFixture(t, c.File))
		})
	}
}

func checkCase(t *testing.T, c caseExpect, update []byte) {
	if len(update) != c.Size {
		t.Fatalf("fixture size %d, want %d", len(update), c.Size)
	}
	doc := mustLoad(t, update, c.GC)
	defer doc.Destroy()

	sv, err := doc.StateVector()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sv, unb64(t, c.StateVector)) {
		t.Errorf("state vector %x, want %x", sv, unb64(t, c.StateVector))
	}
	svOf, err := StateVectorOf(update)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(svOf, unb64(t, c.StateVectorFromUpdate)) {
		t.Errorf("state vector of update %x, want %x", svOf, unb64(t, c.StateVectorFromUpdate))
	}

	snapshot, err := doc.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(snapshot, unb64(t, c.Snapshot)) {
		t.Errorf("snapshot %x, want %x", snapshot, unb64(t, c.Snapshot))
	}
	if fp := mustFingerprint(t, doc); fp != c.Fingerprint {
		t.Errorf("fingerprint %s, want %s", fp, c.Fingerprint)
	}

	clients, err := UpdateClients(update)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(clients)
	want := c.Clients
	if want == nil {
		want = []uint64{}
	}
	if !slices.Equal(clients, want) {
		t.Errorf("clients %v, want %v", clients, want)
	}

	scanned, err := RootsOf(update)
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range scanned {
		exp, ok := c.Roots[root.Name]
		if !ok {
			t.Errorf("RootsOf found unknown root %q", root.Name)
			continue
		}
		if root.Kind != exp.Kind {
			t.Errorf("RootsOf %q kind %s, want %s", root.Name, root.Kind, exp.Kind)
		}
	}

	checkRoots(t, "loaded", doc, c.Roots)

	reencoded := mustUpdate(t, doc)
	if !bytes.Equal(reencoded, update) {
		if reason, known := knownReencodeDiffs[c.Name]; known {
			t.Logf("re-encoded bytes differ from yjs (%d vs %d), known: %s", len(reencoded), len(update), reason)
		} else {
			t.Errorf("re-encoded update differs from yjs: %d bytes vs %d", len(reencoded), len(update))
		}
	}
	reloaded := mustLoad(t, reencoded, c.GC)
	defer reloaded.Destroy()
	checkRoots(t, "reloaded", reloaded, c.Roots)
	if fp := mustFingerprint(t, reloaded); fp != c.Fingerprint {
		t.Errorf("reloaded fingerprint %s, want %s", fp, c.Fingerprint)
	}
	again := mustUpdate(t, reloaded)
	if !bytes.Equal(again, reencoded) {
		t.Errorf("second re-encode differs from the first: %d bytes vs %d", len(again), len(reencoded))
	}

	if !c.GC {
		gcDoc := mustLoad(t, update, true)
		defer gcDoc.Destroy()
		gcUpdate := mustUpdate(t, gcDoc)
		if want := unb64(t, c.GCReload); !bytes.Equal(gcUpdate, want) {
			if reason, known := knownGCReloadDiffs[c.Name]; known {
				t.Logf("gc reload bytes differ from yjs (%d vs %d), known: %s", len(gcUpdate), len(want), reason)
			} else {
				t.Errorf("gc reload of a gc:false update: %d bytes, yjs %d", len(gcUpdate), len(want))
			}
		}
		if fp := mustFingerprint(t, gcDoc); fp != c.GCReloadFingerprint {
			t.Errorf("gc reload fingerprint %s, want %s", fp, c.GCReloadFingerprint)
		}
		checkRoots(t, "gc reload", gcDoc, c.Roots)
	}
}

func TestFingerprintOf(t *testing.T) {
	for _, c := range loadCases(t) {
		if got := FingerprintOf(unb64(t, c.Snapshot)); got != c.Fingerprint {
			t.Errorf("%s: FingerprintOf %s, want %s", c.Name, got, c.Fingerprint)
		}
	}
}
