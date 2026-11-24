package ydoc

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"

	"github.com/reearth/ygo/crdt"
)

var ErrPanic = errors.New("ydoc: the document failed")

type Doc struct {
	doc    *crdt.Doc
	gc     bool
	mu     sync.Mutex
	broken error
}

func guard(fn func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if cause, ok := recovered.(error); ok {
				err = fmt.Errorf("%w: %w", ErrPanic, cause)
				return
			}
			err = fmt.Errorf("%w: %v", ErrPanic, recovered)
		}
	}()
	return fn()
}

func New(gc bool) *Doc {
	return &Doc{doc: crdt.New(crdt.WithGC(gc)), gc: gc}
}

func Load(update []byte, gc bool) (*Doc, error) {
	doc := New(gc)
	if len(update) == 0 {
		return doc, nil
	}
	if err := doc.Apply(update, nil); err != nil {
		return nil, err
	}
	return doc, nil
}

func (d *Doc) GC() bool { return d.gc }

func (d *Doc) Err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.broken
}

func (d *Doc) run(fn func() error) error {
	if err := d.Err(); err != nil {
		return err
	}
	err := guard(fn)
	if errors.Is(err, ErrPanic) && !errors.Is(err, crdt.ErrRootTypeMismatch) {
		d.mu.Lock()
		d.broken = err
		d.mu.Unlock()
	}
	return err
}

func (d *Doc) Apply(update []byte, origin any) error {
	return d.run(func() error {
		if err := crdt.ApplyUpdateV1(d.doc, update, origin); err != nil {
			return fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		return nil
	})
}

func (d *Doc) Update() ([]byte, error) {
	var out []byte
	err := d.run(func() error {
		out = crdt.EncodeStateAsUpdateV1(d.doc, nil)
		return nil
	})
	return out, err
}

func (d *Doc) UpdateSince(stateVector []byte) ([]byte, error) {
	var out []byte
	err := d.run(func() error {
		sv, err := crdt.DecodeStateVectorV1(stateVector)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		out = crdt.EncodeStateAsUpdateV1(d.doc, sv)
		return nil
	})
	return out, err
}

func (d *Doc) StateVector() ([]byte, error) {
	var out []byte
	err := d.run(func() error {
		out = crdt.EncodeStateVectorV1(d.doc)
		return nil
	})
	return out, err
}

func (d *Doc) Snapshot() ([]byte, error) {
	var out []byte
	err := d.run(func() error {
		out = crdt.EncodeSnapshot(crdt.CaptureSnapshot(d.doc))
		return nil
	})
	return out, err
}

func (d *Doc) Fingerprint() (string, error) {
	snapshot, err := d.Snapshot()
	if err != nil {
		return "", err
	}
	return FingerprintOf(snapshot), nil
}

func FingerprintOf(snapshot []byte) string {
	sum := sha256.Sum256(snapshot)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func (d *Doc) Size() (int, error) {
	update, err := d.Update()
	return len(update), err
}

func (d *Doc) OnUpdate(fn func(update []byte, origin any)) func() {
	var stop func()
	_ = d.run(func() error {
		stop = d.doc.OnUpdate(fn)
		return nil
	})
	if stop == nil {
		return func() {}
	}
	return func() { _ = guard(func() error { stop(); return nil }) }
}

func (d *Doc) Change(origin any, fn func(tx *Tx)) error {
	return d.run(func() error {
		d.doc.Transact(func(txn *crdt.Transaction) { fn(&Tx{txn: txn}) }, origin)
		return nil
	})
}

func (d *Doc) Copy() (*Doc, error) {
	update, err := d.Update()
	if err != nil {
		return nil, err
	}
	return Load(update, d.gc)
}

func (d *Doc) Roots() ([]Root, error) {
	update, err := d.Update()
	if err != nil {
		return nil, err
	}
	var roots []Root
	err = guard(func() error {
		var scanErr error
		roots, scanErr = classifyRoots(update)
		return scanErr
	})
	return roots, err
}

func (d *Doc) Destroy() {
	_ = guard(func() error { d.doc.Destroy(); return nil })
}

func (d *Doc) Text(name string) (*Text, error) {
	var t *crdt.YText
	err := d.run(func() error { t = d.doc.GetText(name); return nil })
	return &Text{t: t}, err
}

func (d *Doc) Map(name string) (*Map, error) {
	var m *crdt.YMap
	err := d.run(func() error { m = d.doc.GetMap(name); return nil })
	return &Map{m: m}, err
}

func (d *Doc) Array(name string) (*Array, error) {
	var a *crdt.YArray
	err := d.run(func() error { a = d.doc.GetArray(name); return nil })
	return &Array{a: a}, err
}

func (d *Doc) XML(name string) (*XML, error) {
	var f *crdt.YXmlFragment
	err := d.run(func() error { f = d.doc.GetXmlFragment(name); return nil })
	return &XML{f: f}, err
}

type Tx struct {
	txn *crdt.Transaction
}

func (tx *Tx) Text(name string) *Text   { return &Text{t: tx.txn.GetText(name)} }
func (tx *Tx) Map(name string) *Map     { return &Map{m: tx.txn.GetMap(name)} }
func (tx *Tx) Array(name string) *Array { return &Array{a: tx.txn.GetArray(name)} }
func (tx *Tx) XML(name string) *XML     { return &XML{f: tx.txn.GetXmlFragment(name)} }

func Merge(updates ...[]byte) ([]byte, error) {
	var out []byte
	err := guard(func() error {
		merged, err := crdt.MergeUpdatesV1(updates...)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		out = merged
		return nil
	})
	return out, err
}

func Diff(update, stateVector []byte) ([]byte, error) {
	var out []byte
	err := guard(func() error {
		sv, err := crdt.DecodeStateVectorV1(stateVector)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		diff, err := crdt.DiffUpdateV1(update, sv)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		out = diff
		return nil
	})
	return out, err
}

func StateVectorOf(update []byte) ([]byte, error) {
	var out []byte
	err := guard(func() error {
		sv, err := crdt.EncodeStateVectorFromUpdate(update)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		out = sv
		return nil
	})
	return out, err
}

func UpdateClients(update []byte) ([]uint64, error) {
	var out []uint64
	err := guard(func() error {
		clients, err := updateClients(update)
		out = clients
		return err
	})
	return out, err
}

func RootsOf(update []byte) ([]Root, error) {
	var roots []Root
	err := guard(func() error {
		var scanErr error
		roots, scanErr = classifyRoots(update)
		return scanErr
	})
	return roots, err
}
