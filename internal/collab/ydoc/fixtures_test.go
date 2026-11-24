package ydoc

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"testing"
)

type rootExpect struct {
	Kind       Kind    `json:"kind"`
	JSON       *string `json:"json"`
	Empty      bool    `json:"empty"`
	Delta      *string `json:"delta"`
	TextSHA256 string  `json:"textSha256"`
	Length     int     `json:"length"`
}

type caseExpect struct {
	Name                  string                `json:"name"`
	File                  string                `json:"file"`
	Gzip                  bool                  `json:"gzip"`
	GC                    bool                  `json:"gc"`
	Roots                 map[string]rootExpect `json:"roots"`
	StateVector           string                `json:"stateVector"`
	StateVectorFromUpdate string                `json:"stateVectorFromUpdate"`
	Snapshot              string                `json:"snapshot"`
	Fingerprint           string                `json:"fingerprint"`
	Clients               []uint64              `json:"clients"`
	Size                  int                   `json:"size"`
	GCReload              string                `json:"gcReload"`
	GCReloadFingerprint   string                `json:"gcReloadFingerprint"`
	SHA256                string                `json:"sha256"`
	Ops                   int                   `json:"ops"`
}

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if filepath.Ext(name) == ".gz" {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("gunzip %s: %v", name, err)
		}
		data, err = io.ReadAll(zr)
		if err != nil {
			t.Fatalf("gunzip %s: %v", name, err)
		}
	}
	return data
}

func readJSON(t testing.TB, name string, into any) {
	t.Helper()
	if err := json.Unmarshal(readFixture(t, name), into); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
}

func loadCases(t testing.TB) []caseExpect {
	t.Helper()
	var cases []caseExpect
	readJSON(t, "cases.json", &cases)
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	return cases
}

func unb64(t testing.TB, s string) []byte {
	t.Helper()
	out, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("base64: %v", err)
	}
	return out
}

func sha(data []byte) string {
	sum := sha256.Sum256(data)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func decodeLoose(t testing.TB, raw []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("decode json %q: %v", truncate(string(raw)), err)
	}
	return out
}

func truncate(s string) string {
	if len(s) > 400 {
		return s[:400] + "..."
	}
	return s
}

func sameNumber(a, b json.Number) bool {
	if ai, err := strconv.ParseInt(string(a), 10, 64); err == nil {
		if bi, err := strconv.ParseInt(string(b), 10, 64); err == nil {
			return ai == bi
		}
	}
	af, errA := strconv.ParseFloat(string(a), 64)
	bf, errB := strconv.ParseFloat(string(b), 64)
	return errA == nil && errB == nil && af == bf
}

func equalLoose(a, b any) bool {
	switch av := a.(type) {
	case json.Number:
		bv, ok := b.(json.Number)
		return ok && sameNumber(av, bv)
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			w, ok := bv[k]
			if !ok || !equalLoose(v, w) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !equalLoose(av[i], bv[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}

func diffLoose(path string, a, b any) string {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			break
		}
		keys := make([]string, 0, len(av))
		for k := range av {
			keys = append(keys, k)
		}
		for k := range bv {
			if _, ok := av[k]; !ok {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !equalLoose(av[k], bv[k]) {
				return diffLoose(path+"."+k, av[k], bv[k])
			}
		}
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			break
		}
		for i := range av {
			if !equalLoose(av[i], bv[i]) {
				return diffLoose(path+"["+strconv.Itoa(i)+"]", av[i], bv[i])
			}
		}
	}
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return path + ": got " + truncate(string(left)) + " want " + truncate(string(right))
}

func deltaJSON(t testing.TB, delta []Delta) []byte {
	t.Helper()
	out := make([]map[string]any, 0, len(delta))
	for _, op := range delta {
		if op.Op != DeltaInsert {
			continue
		}
		entry := map[string]any{"insert": op.Insert}
		if len(op.Attributes) > 0 {
			entry["attributes"] = map[string]any(op.Attributes)
		}
		out = append(out, entry)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal delta: %v", err)
	}
	return raw
}

func checkRoots(t *testing.T, label string, doc *Doc, want map[string]rootExpect) {
	t.Helper()
	got, err := doc.JSON()
	if err != nil {
		t.Fatalf("%s: JSON: %v", label, err)
	}
	roots, err := doc.Roots()
	if err != nil {
		t.Fatalf("%s: Roots: %v", label, err)
	}
	kinds := map[string]Kind{}
	for _, root := range roots {
		kinds[root.Name] = root.Kind
	}
	for name := range kinds {
		if _, ok := want[name]; !ok {
			t.Errorf("%s: unexpected root %q", label, name)
		}
	}
	for name, exp := range want {
		kind, present := kinds[name]
		if !present {
			if !exp.Empty {
				t.Errorf("%s: root %q (%s) missing from Roots", label, name, exp.Kind)
			}
			continue
		}
		if kind != exp.Kind {
			t.Errorf("%s: root %q kind %s, want %s", label, name, kind, exp.Kind)
			continue
		}
		value, ok := got[name]
		if !ok {
			t.Errorf("%s: root %q missing from JSON", label, name)
			continue
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("%s: marshal %q: %v", label, name, err)
		}
		if exp.Kind == KindText {
			var text string
			if err := json.Unmarshal(raw, &text); err != nil {
				t.Fatalf("%s: text %q: %v", label, name, err)
			}
			if exp.TextSHA256 != "" && sha([]byte(text)) != exp.TextSHA256 {
				t.Errorf("%s: text %q differs from yjs (%d bytes, got %q)", label, name, len(text), truncate(text))
			}
			txt, err := doc.Text(name)
			if err != nil {
				t.Fatalf("%s: Text %q: %v", label, name, err)
			}
			if exp.Length > 0 && txt.Len() != exp.Length {
				t.Errorf("%s: text %q length %d, want %d", label, name, txt.Len(), exp.Length)
			}
			if exp.Delta != nil {
				gotDelta := decodeLoose(t, deltaJSON(t, txt.Delta()))
				wantDelta := decodeLoose(t, []byte(*exp.Delta))
				if !equalLoose(gotDelta, wantDelta) {
					t.Errorf("%s: delta %q: %s", label, name, diffLoose("delta", gotDelta, wantDelta))
				}
			}
		}
		if exp.JSON == nil {
			continue
		}
		gotValue := decodeLoose(t, raw)
		wantValue := decodeLoose(t, []byte(*exp.JSON))
		if !equalLoose(gotValue, wantValue) {
			t.Errorf("%s: root %q: %s", label, name, diffLoose(name, gotValue, wantValue))
		}
	}
}

func mustLoad(t testing.TB, update []byte, gc bool) *Doc {
	t.Helper()
	doc, err := Load(update, gc)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return doc
}

func mustUpdate(t testing.TB, doc *Doc) []byte {
	t.Helper()
	update, err := doc.Update()
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	return update
}

func mustFingerprint(t testing.TB, doc *Doc) string {
	t.Helper()
	fp, err := doc.Fingerprint()
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	return fp
}
