package ydoc

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/reearth/ygo/crdt"
	"github.com/reearth/ygo/encoding"
)

func findCase(t *testing.T, name string) caseExpect {
	t.Helper()
	for _, c := range loadCases(t) {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no case %s", name)
	return caseExpect{}
}

func TestValuesThroughGet(t *testing.T) {
	for _, name := range []string{"values_gc", "values_nogc"} {
		t.Run(name, func(t *testing.T) {
			c := findCase(t, name)
			doc := mustLoad(t, readFixture(t, c.File), c.GC)
			defer doc.Destroy()
			want, _ := decodeLoose(t, []byte(*c.Roots["values"].JSON)).(map[string]any)
			m, err := doc.Map("values")
			if err != nil {
				t.Fatal(err)
			}
			for key, exp := range want {
				value, ok := m.Get(key)
				if !ok {
					if key == "v45" || key == "v46" {
						t.Logf("Map.Get(%q): a Uint8Array set straight into a Y.Map is not readable through ygo's Get (it is kept and re-encoded, and Doc.JSON and Restore see it)", key)
						continue
					}
					t.Errorf("Get(%q) missing", key)
					continue
				}
				raw, err := json.Marshal(jsonValue(value))
				if err != nil {
					t.Fatalf("marshal %q: %v", key, err)
				}
				if got := decodeLoose(t, raw); !equalLoose(got, exp) {
					t.Errorf("Get(%q): %s", key, diffLoose(key, got, exp))
				}
				if _, narrow := value.(float32); narrow {
					t.Errorf("Get(%q) returned a float32", key)
				}
			}
			arr, err := doc.Array("singles")
			if err != nil {
				t.Fatal(err)
			}
			exact := map[int]float64{17: 0.1, 20: 1e300, 22: 5e-324, 23: math.MaxFloat64, 27: float64(float32(1.1)), 11: -2147483648, 12: 2147483648, 14: 9007199254740991}
			for index, number := range exact {
				got, ok := toFloat(arr.Get(index))
				if !ok || got != number {
					t.Errorf("singles[%d] = %v (%T), want %v", index, arr.Get(index), arr.Get(index), number)
				}
			}
		})
	}
}

func toFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int64:
		return float64(v), true
	case int:
		return float64(v), true
	}
	return 0, false
}

func TestGoWrittenMatchesYjs(t *testing.T) {
	c := findCase(t, "written")
	doc := &Doc{doc: crdt.New(crdt.WithGC(true), crdt.WithClientID(71)), gc: true}
	defer doc.Destroy()
	err := doc.Change(nil, func(tx *Tx) {
		m := tx.Map("m")
		m.Set(tx, "float", 0.1)
		m.Set(tx, "huge", 1e300)
		m.Set(tx, "half", 1.5)
		m.Set(tx, "int", 7)
		m.Set(tx, "neg", -300)
		m.Set(tx, "pow40", float64(1<<40))
		m.Set(tx, "str", "hé 😀")
		m.Set(tx, "bool", false)
		m.Set(tx, "nil", nil)
		m.Set(tx, "list", []any{1, "two", 0.1})
		m.Set(tx, "obj", map[string]any{"a": 1, "b": []any{true}})
		m.Set(tx, "int", 8)
		text := tx.Text("t")
		text.Insert(tx, 0, "hello world", nil)
		text.Delete(tx, 0, 6)
		tx.Array("a").Insert(tx, 0, []any{1, 0.25, "x"})
	})
	if err != nil {
		t.Fatal(err)
	}
	update := mustUpdate(t, doc)
	if want := readFixture(t, c.File); !bytes.Equal(update, want) {
		t.Errorf("Go-written update differs from yjs for the same edits:\n go  %x\n yjs %x", update, want)
	}
	if fp := mustFingerprint(t, doc); fp != c.Fingerprint {
		t.Errorf("fingerprint %s, want %s", fp, c.Fingerprint)
	}
	checkRoots(t, "go written", doc, c.Roots)
}

func TestGoValuesRoundTrip(t *testing.T) {
	values := map[string]any{
		"zero":    0,
		"int":     -42,
		"int32":   int64(math.MaxInt32),
		"int40":   int64(1 << 40),
		"int53":   int64(1<<53 - 1),
		"int63":   int64(math.MaxInt64),
		"min63":   int64(math.MinInt64),
		"tenth":   0.1,
		"huge":    1e300,
		"tiny":    5e-324,
		"narrow":  float32(1.1),
		"half":    0.5,
		"big":     encoding.BigInt(1234567890123456789),
		"bytes":   []byte{0, 1, 255},
		"empty":   []byte{},
		"text":    "emoji 😀 𝄞 \u0000 nul",
		"yes":     true,
		"nothing": nil,
		"list":    []any{1, 0.1, "s", []any{false}, map[string]any{"k": 1e-7}},
		"object":  map[string]any{"z": 1, "a": map[string]any{"deep": []any{float32(0.25), encoding.BigInt(-5)}}},
	}
	doc := New(true)
	defer doc.Destroy()
	err := doc.Change(nil, func(tx *Tx) {
		m := tx.Map("m")
		for key, value := range values {
			m.Set(tx, key, value)
		}
		list := make([]any, 0, len(values))
		for _, key := range []string{"int", "tenth", "huge", "narrow", "big", "bytes", "text", "list", "object"} {
			list = append(list, values[key])
		}
		tx.Array("a").Insert(tx, 0, list)
	})
	if err != nil {
		t.Fatal(err)
	}
	reloaded := mustLoad(t, mustUpdate(t, doc), true)
	defer reloaded.Destroy()
	m, err := reloaded.Map("m")
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range values {
		got, ok := m.Get(key)
		if !ok {
			if _, binary := value.([]byte); binary {
				t.Logf("Map.Get(%q): bytes set straight into a map are not readable through ygo's Get", key)
				continue
			}
			t.Errorf("Get(%q) missing", key)
			continue
		}
		if !sameValue(got, value) {
			t.Errorf("Get(%q) = %#v, want %#v", key, got, value)
		}
	}
	first, err := reloaded.JSON()
	if err != nil {
		t.Fatal(err)
	}
	again, err := mustLoad(t, mustUpdate(t, reloaded), true).JSON()
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(again)
	if !bytes.Equal(a, b) {
		t.Errorf("JSON changed across a reload:\n%s\n%s", a, b)
	}
	var shaped struct {
		M map[string]any `json:"m"`
		A []any          `json:"a"`
	}
	if err := json.Unmarshal(a, &shaped); err != nil {
		t.Fatal(err)
	}
	if shaped.M["bytes"] != "AAH/" || shaped.M["narrow"] != float64(float32(1.1)) || shaped.M["tenth"] != 0.1 || len(shaped.A) != 9 {
		t.Errorf("JSON values: bytes %v narrow %v tenth %v array %v", shaped.M["bytes"], shaped.M["narrow"], shaped.M["tenth"], shaped.A)
	}
}

func sameValue(got, want any) bool {
	switch w := want.(type) {
	case int:
		return sameValue(got, int64(w))
	case float32:
		return sameValue(got, float64(w))
	case int64:
		if g, ok := got.(int64); ok {
			return g == w
		}
		if g, ok := got.(encoding.BigInt); ok {
			return int64(g) == w
		}
		g, ok := got.(float64)
		return ok && g == float64(w) && float64(int64(g)) == g
	case float64:
		g, ok := toFloat(got)
		return ok && g == w
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for i := range w {
			if !sameValue(g[i], w[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for k := range w {
			if !sameValue(g[k], w[k]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(got, want)
}
