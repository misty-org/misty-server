package ydoc

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/reearth/ygo/crdt"
	"github.com/reearth/ygo/encoding"
)

func plainValue(value any) any {
	switch v := value.(type) {
	case float32:
		return float64(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, entry := range v {
			out[key] = plainValue(entry)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, entry := range v {
			out[i] = plainValue(entry)
		}
		return out
	}
	return value
}

func plainAttributes(attrs map[string]any) map[string]any {
	if attrs == nil {
		return nil
	}
	out := make(map[string]any, len(attrs))
	for key, value := range attrs {
		out[key] = plainValue(value)
	}
	return out
}

func plainDeltas(delta []Delta) []Delta {
	for i := range delta {
		if _, shared := delta[i].Insert.(string); !shared {
			delta[i].Insert = plainValue(delta[i].Insert)
		}
		if delta[i].Attributes != nil {
			delta[i].Attributes = Attributes(plainAttributes(delta[i].Attributes))
		}
	}
	return delta
}

func jsonValue(value any) any {
	switch v := value.(type) {
	case *crdt.YXmlText:
		return xmlTextString(v)
	case *crdt.YXmlElement:
		return xmlElementString(v)
	case *crdt.YXmlFragment:
		return xmlFragmentString(v)
	case *crdt.YText:
		return v.ToString()
	case *crdt.YMap:
		out := map[string]any{}
		for _, key := range v.Keys() {
			if entry, ok := v.Get(key); ok {
				out[key] = jsonValue(entry)
			}
		}
		return out
	case *crdt.YArray:
		n := v.Len()
		out := make([]any, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, jsonValue(v.Get(i)))
		}
		return out
	case *crdt.Doc:
		return nil
	case float32:
		return jsonFloat(float64(v))
	case float64:
		return jsonFloat(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, entry := range v {
			out[key] = jsonValue(entry)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, entry := range v {
			out[i] = jsonValue(entry)
		}
		return out
	}
	return value
}

func jsonFloat(f float64) any {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return f
}

func marshalShared(value any) (json.RawMessage, error) {
	return json.Marshal(jsonValue(value))
}

func xmlFragmentString(f *crdt.YXmlFragment) string {
	var b strings.Builder
	writeXMLChildren(&b, f)
	return b.String()
}

func xmlElementString(e *crdt.YXmlElement) string {
	var b strings.Builder
	writeXMLElement(&b, e)
	return b.String()
}

func xmlTextString(t *crdt.YXmlText) string {
	var b strings.Builder
	writeXMLText(&b, t)
	return b.String()
}

func writeXMLChildren(b *strings.Builder, f *crdt.YXmlFragment) {
	for _, child := range f.Children() {
		switch node := any(child).(type) {
		case *crdt.YXmlElement:
			writeXMLElement(b, node)
		case *crdt.YXmlText:
			writeXMLText(b, node)
		}
	}
}

func writeXMLElement(b *strings.Builder, e *crdt.YXmlElement) {
	name := strings.ToLower(e.NodeName)
	attrs := e.GetAttributeValues()
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	b.WriteString("<" + name)
	for _, key := range keys {
		b.WriteString(" " + key + `="` + jsString(attrs[key]) + `"`)
	}
	b.WriteString(">")
	writeXMLChildren(b, &e.YXmlFragment)
	b.WriteString("</" + name + ">")
}

type xmlFormat struct {
	name  string
	attrs [][2]string
}

func writeXMLText(b *strings.Builder, t *crdt.YXmlText) {
	for _, op := range t.ToDelta() {
		if op.Op != crdt.DeltaOpInsert {
			continue
		}
		formats := make([]xmlFormat, 0, len(op.Attributes))
		for name, value := range op.Attributes {
			formats = append(formats, xmlFormat{name: name, attrs: forInPairs(value)})
		}
		sort.Slice(formats, func(i, j int) bool { return formats[i].name < formats[j].name })
		for _, format := range formats {
			b.WriteString("<" + format.name)
			for _, attr := range format.attrs {
				b.WriteString(" " + attr[0] + `="` + attr[1] + `"`)
			}
			b.WriteString(">")
		}
		b.WriteString(jsString(op.Insert))
		for i := len(formats) - 1; i >= 0; i-- {
			b.WriteString("</" + formats[i].name + ">")
		}
	}
}

func forInPairs(value any) [][2]string {
	var pairs [][2]string
	switch v := value.(type) {
	case map[string]any:
		for key, entry := range v {
			pairs = append(pairs, [2]string{key, jsString(entry)})
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i][0] < pairs[j][0] })
	case string:
		units := utf16.Encode([]rune(v))
		for i, unit := range units {
			pairs = append(pairs, [2]string{strconv.Itoa(i), string(utf16.Decode([]uint16{unit}))})
		}
	case []any:
		for i, entry := range v {
			pairs = append(pairs, [2]string{strconv.Itoa(i), jsString(entry)})
		}
	}
	return pairs
}

func jsString(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case encoding.BigInt:
		return strconv.FormatInt(int64(v), 10)
	case float32:
		return jsNumber(float64(v))
	case float64:
		return jsNumber(v)
	case []byte:
		parts := make([]string, len(v))
		for i, c := range v {
			parts[i] = strconv.Itoa(int(c))
		}
		return strings.Join(parts, ",")
	case []any:
		parts := make([]string, len(v))
		for i, entry := range v {
			if entry != nil {
				parts[i] = jsString(entry)
			}
		}
		return strings.Join(parts, ",")
	case *crdt.YXmlText:
		return xmlTextString(v)
	case *crdt.YXmlElement:
		return xmlElementString(v)
	case *crdt.YXmlFragment:
		return xmlFragmentString(v)
	case *crdt.YText:
		return v.ToString()
	}
	return "[object Object]"
}

func jsNumber(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0"
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return string(raw)
}
