package ydoc

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

var invalidGUIDDoc = []byte{1, 1, 1, 0, 9, 1, 1, 'm', 1, 0xff, 0x76, 0, 0}

func TestYgoPanicsBecomeErrors(t *testing.T) {
	if _, err := Merge(invalidGUIDDoc); !errors.Is(err, ErrPanic) {
		t.Errorf("Merge: %v, want ErrPanic", err)
	}
	if _, err := Merge(readFixture(t, "text_basic_gc.bin"), invalidGUIDDoc); !errors.Is(err, ErrPanic) {
		t.Errorf("Merge with a good update: %v, want ErrPanic", err)
	}
	if _, err := Diff(invalidGUIDDoc, []byte{0}); !errors.Is(err, ErrPanic) {
		t.Errorf("Diff: %v, want ErrPanic", err)
	}
	if _, err := StateVectorOf(invalidGUIDDoc); !errors.Is(err, ErrPanic) {
		t.Errorf("StateVectorOf: %v, want ErrPanic", err)
	}
	if _, err := RootsOf(invalidGUIDDoc); !errors.Is(err, ErrMalformed) {
		t.Errorf("RootsOf: %v, want ErrMalformed", err)
	}
	if _, err := UpdateClients(invalidGUIDDoc); !errors.Is(err, ErrMalformed) {
		t.Errorf("UpdateClients: %v, want ErrMalformed", err)
	}

	doc := mustLoad(t, readFixture(t, "text_basic_gc.bin"), true)
	defer doc.Destroy()
	if err := doc.Apply(invalidGUIDDoc, nil); !errors.Is(err, ErrMalformed) {
		t.Errorf("Apply: %v, want ErrMalformed", err)
	}
	if err := doc.Err(); err != nil {
		t.Errorf("a rejected update broke the document: %v", err)
	}
	if err := doc.Apply(readFixture(t, "map_basic_gc.bin"), nil); err != nil {
		t.Errorf("Apply after a rejected update: %v", err)
	}
	if _, err := Load(invalidGUIDDoc, true); err == nil {
		t.Error("Load accepted an invalid subdocument guid")
	}
}

func TestGuardMarksTheDocumentBroken(t *testing.T) {
	doc := New(true)
	defer doc.Destroy()
	err := doc.Change(nil, func(tx *Tx) {
		tx.Text("t").Insert(tx, 0, "before", nil)
		panic("boom")
	})
	if !errors.Is(err, ErrPanic) {
		t.Fatalf("Change: %v, want ErrPanic", err)
	}
	if !errors.Is(doc.Err(), ErrPanic) {
		t.Fatalf("Err: %v, want ErrPanic", doc.Err())
	}
	if _, err := doc.Update(); !errors.Is(err, ErrPanic) {
		t.Errorf("Update after a panic: %v", err)
	}
	if err := doc.Apply(readFixture(t, "text_basic_gc.bin"), nil); !errors.Is(err, ErrPanic) {
		t.Errorf("Apply after a panic: %v", err)
	}
	if _, err := doc.Fingerprint(); !errors.Is(err, ErrPanic) {
		t.Errorf("Fingerprint after a panic: %v", err)
	}

	errDoc := New(true)
	defer errDoc.Destroy()
	cause := errors.New("cause")
	if err := errDoc.run(func() error { panic(cause) }); !errors.Is(err, ErrPanic) || !errors.Is(err, cause) {
		t.Errorf("run with an error panic: %v", err)
	}
	if err := guard(func() error { panic(fmt.Sprint("text")) }); !errors.Is(err, ErrPanic) {
		t.Errorf("guard: %v", err)
	}
	if err := guard(func() error { return nil }); err != nil {
		t.Errorf("guard without a panic: %v", err)
	}
}

func TestRootTypeMismatchKeepsTheDocument(t *testing.T) {
	doc := mustLoad(t, readFixture(t, "map_basic_gc.bin"), true)
	defer doc.Destroy()
	if _, err := doc.Map("settings"); err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Text("settings"); !errors.Is(err, ErrPanic) {
		t.Fatalf("reading a map root as text: %v", err)
	}
	if err := doc.Err(); err != nil {
		t.Fatalf("a root type mismatch broke the document: %v", err)
	}
	if _, err := doc.Update(); err != nil {
		t.Fatal(err)
	}
}

func TestMalformedUpdatesNeverPanic(t *testing.T) {
	seeds := []string{"text_basic_gc.bin", "values_gc.bin", "nested_nogc.bin", "xml_gc.bin", "many_clients_gc.bin", "restore_map_gc_b.bin"}
	inputs := [][]byte{nil, {}, {0}, {0xff}, {0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}, {1, 0xff, 0xff, 0xff, 0xff, 0x0f}, invalidGUIDDoc}
	r := rand.New(rand.NewSource(42))
	for i := 0; i < 64; i++ {
		garbage := make([]byte, r.Intn(200))
		r.Read(garbage)
		inputs = append(inputs, garbage)
	}
	for _, name := range seeds {
		base := readFixture(t, name)
		for cut := 0; cut < len(base); cut += 1 + len(base)/97 {
			inputs = append(inputs, append([]byte(nil), base[:cut]...))
		}
		for i := 0; i < 150; i++ {
			mutated := append([]byte(nil), base...)
			for k := 0; k <= r.Intn(3); k++ {
				mutated[r.Intn(len(mutated))] = byte(r.Intn(256))
			}
			inputs = append(inputs, mutated)
		}
	}
	good := readFixture(t, "text_basic_gc.bin")
	for i, input := range inputs {
		label := fmt.Sprintf("input %d (%x)", i, input[:min(len(input), 24)])
		noPanic(t, label+" Apply", func() {
			doc := New(true)
			_ = doc.Apply(input, nil)
			_, _ = doc.Update()
			_, _ = doc.JSON()
			doc.Destroy()
		})
		noPanic(t, label+" Load nogc", func() {
			if doc, err := Load(input, false); err == nil {
				_, _ = doc.Fingerprint()
				_, _ = doc.Roots()
				doc.Destroy()
			}
		})
		noPanic(t, label+" RootsOf", func() { _, _ = RootsOf(input) })
		noPanic(t, label+" UpdateClients", func() { _, _ = UpdateClients(input) })
		noPanic(t, label+" Merge", func() { _, _ = Merge(input, good) })
		noPanic(t, label+" Diff", func() { _, _ = Diff(input, []byte{0}) })
		noPanic(t, label+" Diff sv", func() { _, _ = Diff(good, input) })
		noPanic(t, label+" StateVectorOf", func() { _, _ = StateVectorOf(input) })
		noPanic(t, label+" UpdateSince", func() { doc := mustLoad(t, good, true); _, _ = doc.UpdateSince(input); doc.Destroy() })
		noPanic(t, label+" Restore", func() { doc := mustLoad(t, good, true); _ = doc.Restore(input, nil); doc.Destroy() })
	}
	if _, err := Load(good[:len(good)-1], true); err == nil {
		t.Error("a truncated update loaded")
	}
	if _, err := RootsOf(good[:len(good)/2]); err == nil {
		t.Error("RootsOf accepted a truncated update")
	}
	if _, err := UpdateClients(good[:len(good)/2]); err == nil {
		t.Error("UpdateClients accepted a truncated update")
	}
	if _, err := Diff(good, []byte{5, 1}); err == nil {
		t.Error("Diff accepted a truncated state vector")
	}
}

func noPanic(t *testing.T, label string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", label, r)
		}
	}()
	fn()
}
