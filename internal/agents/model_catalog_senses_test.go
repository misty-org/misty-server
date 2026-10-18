package agent

import (
	"testing"
	"time"
)

func TestSensesChooseGatewayModelsByTypeAndTags(t *testing.T) {
	cases := []struct {
		kind string
		item gatewayCatalogItem
		want bool
	}{
		{"thinking", gatewayCatalogItem{Type: "language", Tags: []string{"tool-use"}}, true},
		{"thinking", gatewayCatalogItem{Type: "language", Tags: []string{"vision"}}, false},
		{"seeing", gatewayCatalogItem{Type: "language", Tags: []string{"vision", "structured-output"}}, true},
		{"seeing", gatewayCatalogItem{Type: "language", Tags: []string{"vision"}}, false},
		{"listening", gatewayCatalogItem{Type: "transcription"}, true},
		{"listening", gatewayCatalogItem{Type: "transcription", Tags: []string{"websocket-transcription"}}, false},
		{"speaking", gatewayCatalogItem{Type: "realtime"}, true},
		{"speaking", gatewayCatalogItem{Type: "speech"}, false},
		{"thinking", gatewayCatalogItem{Type: "image", Tags: []string{"tool-use"}}, false},
	}
	for _, c := range cases {
		if got := senseFits(c.kind, c.item); got != c.want {
			t.Errorf("senseFits(%s, %+v) = %v, want %v", c.kind, c.item, got, c.want)
		}
	}
}

func TestPickerModelsHidesServiceTiersAndModelsBefore2026(t *testing.T) {
	released := func(year int) time.Time { return time.Date(year, time.June, 1, 0, 0, 0, 0, time.UTC) }
	models := []SenseModel{
		{ID: "openai/gpt-5.6-luna", released: released(2026)},
		{ID: "openai/gpt-5.6-luna-batch", released: released(2026)},
		{ID: "openai/gpt-5.6-luna-fast", released: released(2026)},
		{ID: "minimax/minimax-m3-highspeed", released: released(2026)},
		{ID: "spacexai/grok-4.1-fast-reasoning", released: released(2026)},
		{ID: "openai/gpt-4o", released: released(2024)},
		{ID: "acme/undated"},
	}
	ids := func(models []SenseModel) []string {
		out := []string{}
		for _, model := range models {
			out = append(out, model.ID)
		}
		return out
	}
	got := ids(PickerModels("thinking", models, ""))
	want := []string{"openai/gpt-5.6-luna", "acme/undated"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("thinking picker = %v, want %v", got, want)
	}
	if kept := ids(PickerModels("thinking", models, "openai/gpt-4o")); len(kept) != 3 || kept[1] != "openai/gpt-4o" {
		t.Fatalf("the chosen model must stay listed: %v", kept)
	}
	if all := PickerModels("seeing", models, ""); len(all) != len(models) {
		t.Fatalf("other senses list everything, got %d", len(all))
	}
}

func TestPickerModelsLeadsWithEachLabsCurrentLineup(t *testing.T) {
	at := func(month time.Month) time.Time { return time.Date(2026, month, 1, 0, 0, 0, 0, time.UTC) }
	models := []SenseModel{
		{ID: "openai/gpt-5.6-luna", released: at(7)},
		{ID: "openai/gpt-6-luna", released: at(9)},
		{ID: "openai/gpt-6-sol", released: at(9)},
		{ID: "openai/gpt-5.4-mini", released: at(9)},
		{ID: "openai/gpt-5.5", released: at(4)},
		{ID: "anthropic/claude-opus-4.8", released: at(5)},
		{ID: "anthropic/claude-opus-5.5", released: at(9)},
		{ID: "google/gemma-4-31b-it", released: at(9)},
		{ID: "qwen/qwen3.8-max", released: at(9)},
		{ID: "qwen/qwen3.5-plus", released: at(2)},
		{ID: "poolside/laguna-s-2.1-free", released: at(9)},
		{ID: "acme/undated"},
	}
	more := map[string]bool{}
	for _, model := range PickerModels("thinking", models, "") {
		more[model.ID] = model.More
	}
	for id, want := range map[string]bool{
		"openai/gpt-6-luna": false, "openai/gpt-6-sol": false, "anthropic/claude-opus-5.5": false,
		"openai/gpt-5.4-mini":        false, // still on OpenAI's menu
		"qwen/qwen3.8-max":           false, // every lab counts, not only the biggest
		"acme/undated":               false,
		"openai/gpt-5.6-luna":        true, // replaced by gpt-6-luna
		"openai/gpt-5.5":             true, // OpenAI has moved well past it
		"anthropic/claude-opus-4.8":  true,
		"qwen/qwen3.5-plus":          true,
		"google/gemma-4-31b-it":      true, // an open instruct build
		"poolside/laguna-s-2.1-free": true, // a free tier
	} {
		if got, ok := more[id]; !ok || got != want {
			t.Errorf("%s more = %v (listed %v), want %v", id, got, ok, want)
		}
	}
	for _, model := range PickerModels("thinking", models, "openai/gpt-5.5") {
		if model.ID == "openai/gpt-5.5" && model.More {
			t.Error("the chosen model must not be tucked away")
		}
	}
}
