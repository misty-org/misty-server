package ydoc

import (
	"encoding/json"
	"errors"

	"github.com/reearth/ygo/crdt"
)

type mapSpec struct {
	keys    []string
	entries map[string]any
}

type arraySpec struct{ items []any }

type textSpec struct{ delta []Delta }

type xmlSpec struct{ node XMLNode }

type fragmentSpec struct{ children []XMLNode }

func capture(value any) any {
	switch v := value.(type) {
	case *crdt.YMap:
		spec := mapSpec{keys: v.Keys(), entries: map[string]any{}}
		for _, key := range spec.keys {
			if entry, ok := v.Get(key); ok {
				spec.entries[key] = capture(entry)
			}
		}
		return spec
	case *crdt.YArray:
		spec := arraySpec{}
		for i := 0; i < v.Len(); i++ {
			spec.items = append(spec.items, capture(v.Get(i)))
		}
		return spec
	case *crdt.YXmlElement:
		return xmlSpec{node: XMLNode{Element: v.NodeName, Attributes: v.GetAttributeValues(), Children: xmlChildren(&v.YXmlFragment)}}
	case *crdt.YXmlText:
		return xmlSpec{node: XMLNode{IsText: true, Text: v.ToDelta()}}
	case *crdt.YXmlFragment:
		return fragmentSpec{children: xmlChildren(v)}
	case *crdt.YText:
		return textSpec{delta: v.ToDelta()}
	case *crdt.Doc:
		return nil
	}
	return value
}

func isShared(value any) bool {
	switch value.(type) {
	case mapSpec, arraySpec, textSpec, xmlSpec, fragmentSpec,
		*crdt.YMap, *crdt.YArray, *crdt.YText, *crdt.YXmlElement, *crdt.YXmlText, *crdt.YXmlFragment, *crdt.Doc:
		return true
	}
	return false
}

func materialize(txn *crdt.Transaction, value any) any {
	switch v := value.(type) {
	case mapSpec:
		m := crdt.NewMapPrelim()
		for _, key := range v.keys {
			if entry, ok := v.entries[key]; ok {
				m.Set(txn, key, materialize(txn, entry))
			}
		}
		return m
	case arraySpec:
		a := crdt.NewArrayPrelim()
		for _, item := range v.items {
			if isShared(item) {
				pushShared(txn, a, materialize(txn, item))
				continue
			}
			a.Push(txn, []any{item})
		}
		return a
	case textSpec:
		t := crdt.NewTextPrelim()
		if len(v.delta) > 0 {
			t.ApplyDelta(txn, plainDelta(v.delta))
		}
		return t
	case xmlSpec:
		if v.node.IsText {
			t := crdt.NewYXmlText()
			if len(v.node.Text) > 0 {
				t.ApplyDelta(txn, plainDelta(v.node.Text))
			}
			return t
		}
		e := crdt.NewYXmlElement(v.node.Element)
		for key, attr := range v.node.Attributes {
			e.SetAttributeValue(txn, key, attr)
		}
		for i, child := range v.node.Children {
			switch made := materialize(txn, xmlSpec{node: child}).(type) {
			case *crdt.YXmlElement:
				e.InsertElement(txn, i, made)
			case *crdt.YXmlText:
				e.InsertText(txn, i, made)
			}
		}
		return e
	case fragmentSpec:
		return nil
	}
	return value
}

func pushShared(txn *crdt.Transaction, a *crdt.YArray, made any) {
	switch v := made.(type) {
	case *crdt.YMap:
		a.PushType(txn, v)
	case *crdt.YArray:
		a.PushType(txn, v)
	case *crdt.YText:
		a.PushType(txn, v)
	case *crdt.YXmlElement:
		a.PushType(txn, v)
	case *crdt.YXmlText:
		a.PushType(txn, v)
	}
}

func insertShared(txn *crdt.Transaction, a *crdt.YArray, index int, made any) bool {
	switch v := made.(type) {
	case *crdt.YMap:
		a.InsertType(txn, index, v)
	case *crdt.YArray:
		a.InsertType(txn, index, v)
	case *crdt.YText:
		a.InsertType(txn, index, v)
	case *crdt.YXmlElement:
		a.InsertType(txn, index, v)
	case *crdt.YXmlText:
		a.InsertType(txn, index, v)
	default:
		return false
	}
	return true
}

func plainDelta(delta []Delta) []Delta {
	out := make([]Delta, 0, len(delta))
	for _, op := range delta {
		if op.Op != crdt.DeltaOpInsert {
			continue
		}
		if _, text := op.Insert.(string); !text && isShared(op.Insert) {
			continue
		}
		out = append(out, op)
	}
	return out
}

func sameJSON(left, right any) bool {
	a, errA := json.Marshal(left)
	b, errB := json.Marshal(right)
	return errA == nil && errB == nil && string(a) == string(b)
}

type restorePlan struct {
	name      string
	kind      Kind
	mapTarget []string
	current   map[string]any
	source    any
	text      *crdt.YText
	ymap      *crdt.YMap
	array     *crdt.YArray
	xml       *crdt.YXmlFragment
}

func (d *Doc) Restore(update []byte, origin any) error {
	sourceRoots, err := RootsOf(update)
	if err != nil {
		return err
	}
	source, err := Load(binaryAsAny(update), false)
	if err != nil {
		return err
	}
	defer source.Destroy()
	targetRoots, err := d.Roots()
	if err != nil {
		return err
	}
	kinds := map[string]Kind{}
	order := []string{}
	for _, root := range targetRoots {
		kinds[root.Name] = root.Kind
		order = append(order, root.Name)
	}
	for _, root := range sourceRoots {
		if _, known := kinds[root.Name]; !known {
			kinds[root.Name] = root.Kind
			order = append(order, root.Name)
		}
	}
	plans := make([]*restorePlan, 0, len(order))
	for _, name := range order {
		plan, err := d.planRestore(source, name, kinds[name])
		if errors.Is(err, crdt.ErrRootTypeMismatch) {
			continue
		}
		if err != nil {
			return err
		}
		plans = append(plans, plan)
	}
	return d.run(func() error {
		d.doc.Transact(func(txn *crdt.Transaction) {
			for _, plan := range plans {
				plan.apply(txn)
			}
		}, origin)
		return nil
	})
}

func (d *Doc) planRestore(source *Doc, name string, kind Kind) (*restorePlan, error) {
	plan := &restorePlan{name: name, kind: kind}
	err := d.run(func() error {
		switch kind {
		case KindMap:
			plan.ymap = d.doc.GetMap(name)
			plan.mapTarget = plan.ymap.Keys()
			plan.current = map[string]any{}
			for _, key := range plan.mapTarget {
				if value, ok := plan.ymap.Get(key); ok {
					plan.current[key] = value
				}
			}
		case KindArray:
			plan.array = d.doc.GetArray(name)
		case KindText:
			plan.text = d.doc.GetText(name)
		case KindXML:
			plan.xml = d.doc.GetXmlFragment(name)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = guard(func() error {
		switch kind {
		case KindMap:
			plan.source = capture(source.doc.GetMap(name))
		case KindArray:
			plan.source = capture(source.doc.GetArray(name))
		case KindText:
			plan.source = capture(source.doc.GetText(name))
		case KindXML:
			plan.source = capture(source.doc.GetXmlFragment(name))
		}
		return nil
	})
	return plan, err
}

func (plan *restorePlan) apply(txn *crdt.Transaction) {
	switch plan.kind {
	case KindMap:
		from, _ := plan.source.(mapSpec)
		for _, key := range plan.mapTarget {
			if _, kept := from.entries[key]; !kept {
				plan.ymap.Delete(txn, key)
			}
		}
		for _, key := range from.keys {
			value, ok := from.entries[key]
			if !ok {
				continue
			}
			current, had := plan.current[key]
			if had && !isShared(value) && !isShared(current) && sameJSON(value, current) {
				continue
			}
			plan.ymap.Set(txn, key, materialize(txn, value))
		}
	case KindArray:
		from, _ := plan.source.(arraySpec)
		if n := plan.array.Len(); n > 0 {
			plan.array.Delete(txn, 0, n)
		}
		index := 0
		var plain []any
		flush := func() {
			if len(plain) > 0 {
				plan.array.Insert(txn, index, plain)
				index += len(plain)
				plain = nil
			}
		}
		for _, item := range from.items {
			if !isShared(item) {
				plain = append(plain, item)
				continue
			}
			flush()
			if insertShared(txn, plan.array, index, materialize(txn, item)) {
				index++
			}
		}
		flush()
	case KindText:
		from, _ := plan.source.(textSpec)
		if n := plan.text.Len(); n > 0 {
			plan.text.Delete(txn, 0, n)
		}
		if delta := plainDelta(from.delta); len(delta) > 0 {
			plan.text.ApplyDelta(txn, delta)
		}
	case KindXML:
		from, _ := plan.source.(fragmentSpec)
		if n := plan.xml.Len(); n > 0 {
			plan.xml.Delete(txn, 0, n)
		}
		for i, node := range from.children {
			insertXMLNode(txn, plan.xml, nil, i, node)
		}
	}
}

func (d *Doc) Clear(origin any) error {
	roots, err := d.Roots()
	if err != nil {
		return err
	}
	empty := New(true)
	defer empty.Destroy()
	plans := make([]*restorePlan, 0, len(roots))
	for _, root := range roots {
		plan, err := d.planRestore(empty, root.Name, root.Kind)
		if errors.Is(err, crdt.ErrRootTypeMismatch) {
			continue
		}
		if err != nil {
			return err
		}
		plans = append(plans, plan)
	}
	return d.run(func() error {
		d.doc.Transact(func(txn *crdt.Transaction) {
			for _, plan := range plans {
				plan.apply(txn)
			}
		}, origin)
		return nil
	})
}

func (d *Doc) JSON() (map[string]any, error) {
	update, err := d.Update()
	if err != nil {
		return nil, err
	}
	copied, err := Load(binaryAsAny(update), d.gc)
	if err != nil {
		return nil, err
	}
	defer copied.Destroy()
	roots, err := copied.Roots()
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	err = copied.run(func() error {
		for _, root := range roots {
			var value any
			switch root.Kind {
			case KindText:
				out[root.Name] = copied.doc.GetText(root.Name).ToString()
				continue
			case KindXML:
				out[root.Name] = xmlFragmentString(copied.doc.GetXmlFragment(root.Name))
				continue
			case KindMap:
				value = copied.doc.GetMap(root.Name)
			case KindArray:
				value = copied.doc.GetArray(root.Name)
			}
			raw, err := marshalShared(value)
			if err != nil {
				return err
			}
			out[root.Name] = raw
		}
		return nil
	})
	return out, err
}
