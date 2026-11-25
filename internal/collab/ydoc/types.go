package ydoc

import (
	"encoding/json"

	"github.com/reearth/ygo/crdt"
)

type Delta = crdt.Delta

type Attributes = crdt.Attributes

const (
	DeltaInsert = crdt.DeltaOpInsert
	DeltaDelete = crdt.DeltaOpDelete
	DeltaRetain = crdt.DeltaOpRetain
)

type Text struct{ t *crdt.YText }

func (t *Text) String() string {
	var out string
	_ = guard(func() error { out = t.t.ToString(); return nil })
	return out
}

func (t *Text) Delta() []Delta {
	var out []Delta
	_ = guard(func() error { out = plainDeltas(t.t.ToDelta()); return nil })
	return out
}

func (t *Text) Len() int {
	var out int
	_ = guard(func() error { out = t.t.Len(); return nil })
	return out
}

func (t *Text) Insert(tx *Tx, index int, text string, attrs Attributes) {
	t.t.Insert(tx.txn, index, text, attrs)
}

func (t *Text) Delete(tx *Tx, index, length int) {
	if length > 0 {
		t.t.Delete(tx.txn, index, length)
	}
}

func (t *Text) Format(tx *Tx, index, length int, attrs Attributes) {
	t.t.Format(tx.txn, index, length, attrs)
}

func (t *Text) Replace(tx *Tx, text string) {
	if n := t.t.Len(); n > 0 {
		t.t.Delete(tx.txn, 0, n)
	}
	if text != "" {
		t.t.Insert(tx.txn, 0, text, nil)
	}
}

func (t *Text) ApplyDelta(tx *Tx, delta []Delta) {
	t.t.ApplyDelta(tx.txn, delta)
}

type Map struct{ m *crdt.YMap }

func (m *Map) Get(key string) (any, bool) {
	var value any
	var ok bool
	_ = guard(func() error { value, ok = m.m.Get(key); value = plainValue(value); return nil })
	return value, ok
}

func (m *Map) Keys() []string {
	var out []string
	_ = guard(func() error { out = m.m.Keys(); return nil })
	return out
}

func (m *Map) Set(tx *Tx, key string, value any) { m.m.Set(tx.txn, key, value) }

func (m *Map) Delete(tx *Tx, key string) { m.m.Delete(tx.txn, key) }

func (m *Map) JSON() (json.RawMessage, error) {
	var out []byte
	err := guard(func() error {
		var err error
		out, err = marshalShared(m.m)
		return err
	})
	return out, err
}

type Array struct{ a *crdt.YArray }

func (a *Array) Get(index int) any {
	var out any
	_ = guard(func() error { out = plainValue(a.a.Get(index)); return nil })
	return out
}

func (a *Array) Len() int {
	var out int
	_ = guard(func() error { out = a.a.Len(); return nil })
	return out
}

func (a *Array) Insert(tx *Tx, index int, values []any) { a.a.Insert(tx.txn, index, values) }

func (a *Array) Delete(tx *Tx, index, length int) {
	if length > 0 {
		a.a.Delete(tx.txn, index, length)
	}
}

func (a *Array) JSON() (json.RawMessage, error) {
	var out []byte
	err := guard(func() error {
		var err error
		out, err = marshalShared(a.a)
		return err
	})
	return out, err
}

type XML struct{ f *crdt.YXmlFragment }

type XMLNode struct {
	Element    string
	Attributes map[string]any
	Children   []XMLNode
	Text       []Delta
	IsText     bool
}

func (x *XML) Len() int {
	var out int
	_ = guard(func() error { out = x.f.Len(); return nil })
	return out
}

func (x *XML) String() string {
	var out string
	_ = guard(func() error { out = xmlFragmentString(x.f); return nil })
	return out
}

func (x *XML) Children() []XMLNode {
	var out []XMLNode
	_ = guard(func() error { out = xmlChildren(x.f); return nil })
	return out
}

func (x *XML) Delete(tx *Tx, index, length int) {
	if length > 0 {
		x.f.Delete(tx.txn, index, length)
	}
}

func (x *XML) Insert(tx *Tx, index int, nodes []XMLNode) {
	for offset, node := range nodes {
		insertXMLNode(tx.txn, x.f, nil, index+offset, node)
	}
}

func xmlChildren(f *crdt.YXmlFragment) []XMLNode { return xmlChildrenAt(f, 0) }

func xmlChildrenAt(f *crdt.YXmlFragment, depth int) []XMLNode {
	if depth > maxNesting {
		return nil
	}
	children := f.Children()
	out := make([]XMLNode, 0, len(children))
	for _, child := range children {
		switch node := any(child).(type) {
		case *crdt.YXmlElement:
			out = append(out, XMLNode{
				Element: node.NodeName, Attributes: plainAttributes(node.GetAttributeValues()), Children: xmlChildrenAt(&node.YXmlFragment, depth+1),
			})
		case *crdt.YXmlText:
			out = append(out, XMLNode{IsText: true, Text: plainDeltas(node.ToDelta())})
		}
	}
	return out
}

func insertXMLNode(txn *crdt.Transaction, fragment *crdt.YXmlFragment, element *crdt.YXmlElement, index int, node XMLNode) {
	if node.IsText {
		text := crdt.NewYXmlText()
		if element != nil {
			element.InsertText(txn, index, text)
		} else {
			fragment.InsertText(txn, index, text)
		}
		writeDelta(txn, &text.YText, node.Text)
		return
	}
	made := crdt.NewYXmlElement(node.Element)
	if element != nil {
		element.InsertElement(txn, index, made)
	} else {
		fragment.InsertElement(txn, index, made)
	}
	for key, value := range node.Attributes {
		made.SetAttributeValue(txn, key, value)
	}
	for i, child := range node.Children {
		insertXMLNode(txn, nil, made, i, child)
	}
}

func writeDelta(txn *crdt.Transaction, text *crdt.YText, delta []Delta) {
	inserts := make([]Delta, 0, len(delta))
	for _, op := range delta {
		if op.Op == crdt.DeltaOpInsert {
			inserts = append(inserts, op)
		}
	}
	if len(inserts) > 0 {
		text.ApplyDelta(txn, inserts)
	}
}
