package api

import (
	"encoding/json"
	"testing"
)

func TestToolActivitySummarizesKnownFieldsOnly(t *testing.T) {
	activity := toolActivity(
		json.RawMessage(`{"query":"vegetarian  weight loss","token":"secret"}`),
		json.RawMessage(`{"results":[{"title":"The vegetarian diet","url":"https://www.nhs.uk/live-well/?q=1"},{"url":"https://eatright.org/a"},{"body":"no title"}],"stdout":"1,846 words\n"}`),
	)
	if activity == nil {
		t.Fatal("expected an activity")
	}
	if activity.Detail != "vegetarian weight loss" || activity.Result != "3 results" || activity.Output != "1,846 words" {
		t.Fatalf("unexpected activity: %+v", activity)
	}
	if len(activity.Rows) != 2 || activity.Rows[0] != (aiToolActivityRow{Title: "The vegetarian diet", Meta: "nhs.uk"}) || activity.Rows[1].Title != "eatright.org" {
		t.Fatalf("unexpected rows: %+v", activity.Rows)
	}
}

func TestToolActivityKeepsTypedTextAndLinksPrivate(t *testing.T) {
	if activity := toolActivity(json.RawMessage(`{"text":"hunter2","prompt":"private"}`), json.RawMessage(`{"ok":true}`)); activity != nil {
		t.Fatalf("typed text must not be shown: %+v", activity)
	}
	activity := toolActivity(json.RawMessage(`{"url":"https://docs.google.com/document/d/abc/edit?token=x"}`), nil)
	if activity == nil || activity.Detail != "docs.google.com" {
		t.Fatalf("a link shows only its site: %+v", activity)
	}
}
