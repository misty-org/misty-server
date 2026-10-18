package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAdmissionInputCountsImagesApartFromText(t *testing.T) {
	screenshot := "data:image/png;base64," + strings.Repeat("A", 3_000_000)
	raw, _ := json.Marshal(map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": "identify the group in the video"},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": screenshot}}}},
	}})
	got := admissionInput(raw)
	if got["input_images"] != 1 {
		t.Fatalf("images = %v", got["input_images"])
	}
	if bytes := got["input_bytes"].(int); bytes > 1000 {
		t.Fatalf("a 3 MB screenshot must not count as %d text bytes", bytes)
	}
	plain, _ := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "user", "content": strings.Repeat("word ", 100)}}})
	if got := admissionInput(plain); got["input_images"] != nil || got["input_bytes"].(int) < 500 {
		t.Fatalf("plain text keeps its size: %v", got)
	}
}

func TestRuntimeAdmissionUnitsCapsImages(t *testing.T) {
	units := runtimeAdmissionUnits(json.RawMessage(`{"input_bytes":1200,"input_images":500}`))
	if units["input_bytes"] != 1200 || units["input_images"] != 64 {
		t.Fatalf("units = %v", units)
	}
}
