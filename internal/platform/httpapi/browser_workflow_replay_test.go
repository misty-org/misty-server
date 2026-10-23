package api

import (
	"encoding/json"
	"testing"

	"github.com/kannachi323/misty/server/internal/platform/db"
)

func job(operation, input, output string) db.BrowserJobRecord {
	return db.BrowserJobRecord{Operation: operation, Input: json.RawMessage(input), Output: json.RawMessage(output)}
}

func TestBrowserReplayKeepsWhatRanWithLocators(t *testing.T) {
	names := []string{"browser.snapshot", "browser.fill_form", "browser.steps", "browser.wait_for"}
	jobs := []db.BrowserJobRecord{
		job("browser.snapshot", `{}`, `{"url":"https://app.example/signup","tree":"..."}`),
		job("browser.fill_form", `{"documentId":"d","fields":[{"ref":"e1","value":"Ada"},{"ref":"e2","value":true},{"ref":"e3","value":40}]}`,
			`{"steps":[{"index":1,"action":"fill","ok":true,"locator":{"role":"textbox","name":"Name","index":0}},{"index":2,"action":"check","ok":true,"locator":{"role":"checkbox","name":"Agree","index":0}},{"index":3,"action":"fill","ok":true,"locator":{"role":"spinbutton","name":"Age","index":0}}]}`),
		job("browser.steps", `{"documentId":"d","allowConsequential":true,"steps":[{"action":"click","ref":"e9"},{"action":"key","key":"Enter"},{"action":"click","ref":"e10"}]}`,
			`{"steps":[{"index":1,"action":"click","ok":true,"locator":{"role":"button","name":"Send","index":1}},{"index":2,"action":"key","ok":true},{"index":3,"action":"click","ok":false,"error":"covered"}]}`),
		job("browser.wait_for", `{"text":"Sent","timeoutMs":5000}`, `{"met":true}`),
	}
	start, calls, ok := buildBrowserReplay(names, jobs)
	if !ok || start != "https://app.example/signup" || len(calls) != 3 {
		t.Fatalf("replay = %v %v %v", start, calls, ok)
	}
	encoded, _ := json.Marshal(calls)
	want := `[{"args":{"steps":[{"action":"fill","locator":{"index":0,"name":"Name","role":"textbox"},"text":"Ada"},{"action":"check","locator":{"index":0,"name":"Agree","role":"checkbox"}},{"action":"fill","locator":{"index":0,"name":"Age","role":"spinbutton"},"text":"40"}]},"tool":"browser.steps"},` +
		`{"args":{"allowConsequential":true,"steps":[{"action":"click","locator":{"index":1,"name":"Send","role":"button"}},{"action":"key","key":"Enter"}]},"tool":"browser.steps"},` +
		`{"args":{"text":"Sent","timeoutMs":5000},"tool":"browser.wait_for"}]`
	if string(encoded) != want {
		t.Fatalf("calls\n got %s\nwant %s", encoded, want)
	}
}

func TestBrowserReplayRefusesRunsItCannotReproduce(t *testing.T) {
	steps := job("browser.steps", `{"steps":[{"action":"click","ref":"e1"}]}`, `{"steps":[{"index":1,"action":"click","ok":true,"locator":{"role":"button","name":"Go","index":0}}]}`)
	for name, run := range map[string]struct {
		names []string
		jobs  []db.BrowserJobRecord
	}{
		"another tool's effect": {[]string{"browser.steps", "note.create"}, []db.BrowserJobRecord{steps}},
		"extracted data":        {[]string{"browser.extract", "browser.steps"}, []db.BrowserJobRecord{steps}},
		"screen agent":          {[]string{"browser.act"}, []db.BrowserJobRecord{job("browser.act", `{"goal":"x"}`, `{}`)}},
		"no locator":            {[]string{"browser.steps"}, []db.BrowserJobRecord{job("browser.steps", `{"steps":[{"action":"click","ref":"e1"}]}`, `{"steps":[{"index":1,"action":"click","ok":true}]}`)}},
		"skill with a ref":      {[]string{"browser.skill"}, []db.BrowserJobRecord{job("browser.skill", `{"skill":"editor","action":"insert","args":{"ref":"e4","markdown":"x"}}`, `{"ok":true}`)}},
		"nothing to replay":     {[]string{"browser.snapshot"}, []db.BrowserJobRecord{job("browser.snapshot", `{}`, `{"url":"https://x"}`)}},
	} {
		if _, _, ok := buildBrowserReplay(run.names, run.jobs); ok {
			t.Fatalf("%s: replay accepted", name)
		}
	}
	if _, calls, ok := buildBrowserReplay([]string{"browser.skill"}, []db.BrowserJobRecord{job("browser.skill", `{"skill":"grid","action":"paste","args":{"cell":"A1","rows":[["a"]]}}`, `{"ok":true}`)}); !ok || calls[0]["tool"] != "browser.skill" {
		t.Fatal("a ref-free skill call should replay", calls)
	}
}
