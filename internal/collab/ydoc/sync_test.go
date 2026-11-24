package ydoc

import (
	"bytes"
	"testing"
)

type syncExpect struct {
	Name              string                `json:"name"`
	GC                bool                  `json:"gc"`
	Updates           []string              `json:"updates"`
	Merged            string                `json:"merged"`
	MergedStateVector string                `json:"mergedStateVector"`
	Final             string                `json:"final"`
	FinalStateVector  string                `json:"finalStateVector"`
	Mid               string                `json:"mid"`
	MidStateVector    string                `json:"midStateVector"`
	Diff              string                `json:"diff"`
	Roots             map[string]rootExpect `json:"roots"`
	MergedRoots       map[string]rootExpect `json:"mergedRoots"`
	Fingerprint       string                `json:"fingerprint"`
}

func loadSyncCases(t *testing.T) []syncExpect {
	var cases []syncExpect
	readJSON(t, "sync.json", &cases)
	if len(cases) == 0 {
		t.Fatal("no sync cases")
	}
	return cases
}

func checkState(t *testing.T, label string, doc *Doc, c syncExpect) {
	t.Helper()
	checkRoots(t, label, doc, c.Roots)
	if fp := mustFingerprint(t, doc); fp != c.Fingerprint {
		t.Errorf("%s: fingerprint %s, want %s", label, fp, c.Fingerprint)
	}
	sv, err := doc.StateVector()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sv, unb64(t, c.FinalStateVector)) {
		t.Errorf("%s: state vector %x, want %x", label, sv, unb64(t, c.FinalStateVector))
	}
}

func TestMerge(t *testing.T) {
	for _, c := range loadSyncCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			updates := make([][]byte, len(c.Updates))
			for i, u := range c.Updates {
				updates[i] = unb64(t, u)
			}
			yjsMerged := unb64(t, c.Merged)
			merged, err := Merge(updates...)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(merged, yjsMerged) {
				t.Logf("Merge bytes differ from Y.mergeUpdates (%d vs %d): ygo joins adjacent structs differently; the merged state is checked below", len(merged), len(yjsMerged))
			}
			sv, err := StateVectorOf(merged)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(sv, unb64(t, c.MergedStateVector)) {
				t.Errorf("merged state vector %x, want %x", sv, unb64(t, c.MergedStateVector))
			}
			checkState(t, "go merge", mustLoad(t, merged, c.GC), c)
			checkState(t, "yjs merge", mustLoad(t, yjsMerged, c.GC), c)
			checkRoots(t, "yjs merge in yjs", mustLoad(t, yjsMerged, c.GC), c.MergedRoots)

			inOrder := New(c.GC)
			for _, u := range updates {
				if err := inOrder.Apply(u, nil); err != nil {
					t.Fatal(err)
				}
			}
			checkState(t, "applied in order", inOrder, c)

			reversed := New(c.GC)
			for i := len(updates) - 1; i >= 0; i-- {
				if err := reversed.Apply(updates[i], nil); err != nil {
					t.Fatal(err)
				}
			}
			checkState(t, "applied in reverse", reversed, c)

			halves, err := Merge(mustMerge(t, updates[:len(updates)/2]), mustMerge(t, updates[len(updates)/2:]))
			if err != nil {
				t.Fatal(err)
			}
			checkState(t, "merge of merges", mustLoad(t, halves, c.GC), c)
		})
	}
}

func mustMerge(t *testing.T, updates [][]byte) []byte {
	t.Helper()
	merged, err := Merge(updates...)
	if err != nil {
		t.Fatal(err)
	}
	return merged
}

func TestDiff(t *testing.T) {
	for _, c := range loadSyncCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			final := unb64(t, c.Final)
			mid := unb64(t, c.Mid)
			midSV := unb64(t, c.MidStateVector)
			yjsDiff := unb64(t, c.Diff)

			midDoc := mustLoad(t, mid, c.GC)
			got, err := midDoc.StateVector()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, midSV) {
				t.Errorf("mid state vector %x, want %x", got, midSV)
			}

			diff, err := Diff(final, midSV)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(diff, yjsDiff) {
				t.Logf("Diff bytes differ from Y.diffUpdate (%d vs %d): ygo keeps the parentSub bit on items that have an origin, which readers ignore; the applied state is checked below", len(diff), len(yjsDiff))
			}
			withGo := mustLoad(t, mid, c.GC)
			if err := withGo.Apply(diff, nil); err != nil {
				t.Fatal(err)
			}
			checkState(t, "mid + go diff", withGo, c)

			withYjs := mustLoad(t, mid, c.GC)
			if err := withYjs.Apply(yjsDiff, nil); err != nil {
				t.Fatal(err)
			}
			checkState(t, "mid + yjs diff", withYjs, c)

			finalDoc := mustLoad(t, final, c.GC)
			since, err := finalDoc.UpdateSince(midSV)
			if err != nil {
				t.Fatal(err)
			}
			withSince := mustLoad(t, mid, c.GC)
			if err := withSince.Apply(since, nil); err != nil {
				t.Fatal(err)
			}
			checkState(t, "mid + update since", withSince, c)

			everything, err := finalDoc.UpdateSince([]byte{0})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(everything, mustUpdate(t, finalDoc)) {
				t.Error("UpdateSince(empty state vector) is not the whole update")
			}
			nothing, err := Diff(final, unb64(t, c.FinalStateVector))
			if err != nil {
				t.Fatal(err)
			}
			unchanged := mustLoad(t, final, c.GC)
			before := mustFingerprint(t, unchanged)
			if err := unchanged.Apply(nothing, nil); err != nil {
				t.Fatal(err)
			}
			if mustFingerprint(t, unchanged) != before {
				t.Error("a diff against the full state vector changed the document")
			}
		})
	}
}
