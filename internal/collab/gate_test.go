package collab

import (
	"strings"
	"testing"

	"github.com/misty-org/misty-server/internal/collab/ydoc"
)

func insertion(t *testing.T, base *ydoc.Doc, text string) []byte {
	t.Helper()
	incoming, err := base.Copy()
	if err != nil {
		t.Fatal(err)
	}
	defer incoming.Destroy()
	vector, err := incoming.StateVector()
	if err != nil {
		t.Fatal(err)
	}
	if err := incoming.Change(nil, func(tx *ydoc.Tx) { tx.Text("content").Insert(tx, 0, text, nil) }); err != nil {
		t.Fatal(err)
	}
	update, err := incoming.UpdateSince(vector)
	if err != nil {
		t.Fatal(err)
	}
	return update
}

func docWith(t *testing.T, text string) *ydoc.Doc {
	t.Helper()
	doc := ydoc.New(true)
	if text != "" {
		if err := doc.Change(nil, func(tx *ydoc.Tx) { tx.Text("content").Insert(tx, 0, text, nil) }); err != nil {
			t.Fatal(err)
		}
	}
	return doc
}

func TestGateLetsSmallUpdatesThroughWithoutMeasuring(t *testing.T) {
	doc := docWith(t, "saved")
	gate := &sizeGate{doc: doc}
	gate.measure()
	if result := gate.check(insertion(t, doc, "more")); result != nil {
		t.Fatalf("result %+v", result)
	}
}

func TestGateMeasuresAgainInsteadOfTrustingAStaleEstimate(t *testing.T) {
	doc := docWith(t, "saved")
	gate := &sizeGate{doc: doc}
	gate.measured(MaxDocumentBytes)
	if result := gate.check(insertion(t, doc, "more")); result != nil {
		t.Fatalf("result %+v", result)
	}
}

func TestGateWarnsNearTheCeilingAndBlocksPastIt(t *testing.T) {
	doc := docWith(t, strings.Repeat("x", DocumentWarningBytes+1024))
	gate := &sizeGate{doc: doc}
	gate.measure()
	if result := gate.check(insertion(t, doc, "y")); result == nil || result.status != "warning" {
		t.Fatalf("near the ceiling %+v", result)
	}
	tooMuch := strings.Repeat("z", MaxDocumentBytes-DocumentWarningBytes)
	if result := gate.check(insertion(t, doc, tooMuch)); result == nil || result.status != "blocked" {
		t.Fatalf("past the ceiling %+v", result)
	}
}

func TestGateCountsUpdatesAppliedSinceTheLastMeasurement(t *testing.T) {
	doc := ydoc.New(true)
	gate := &sizeGate{doc: doc}
	gate.measure()
	big := strings.Repeat("x", DocumentWarningBytes+1024)
	var applied int
	stop := doc.OnUpdate(func(update []byte, _ any) { applied += len(update) })
	if err := doc.Change(nil, func(tx *ydoc.Tx) { tx.Text("content").Insert(tx, 0, big, nil) }); err != nil {
		t.Fatal(err)
	}
	stop()
	gate.appliedSince += applied
	if result := gate.check(insertion(t, doc, "y")); result == nil || result.status != "warning" {
		t.Fatalf("result %+v", result)
	}
}
