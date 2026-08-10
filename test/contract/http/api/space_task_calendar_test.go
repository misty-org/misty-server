package api

import (
	"reflect"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func testSchedule() db.TaskSchedule {
	return db.TaskSchedule{
		Title: "Design review", Description: "Walk through the beta board.", Location: "Room 2",
		StartsAt: "2026-07-27T15:00:00Z", EndsAt: "2026-07-27T16:00:00Z",
		AllDay: false, Timezone: "America/Los_Angeles",
	}
}

func testLink() db.TaskCalendarLink {
	published := testSchedule()
	return db.TaskCalendarLink{
		SourceID: "source-1", GoogleCalendarID: "team@group.calendar.google.com",
		GoogleEventID: "event-1", Published: &published,
	}
}

func TestUnpublishedFields(t *testing.T) {
	link := testLink()
	if fields := db.UnpublishedFields(testSchedule(), &link); len(fields) != 0 {
		t.Fatalf("a settled task reported %v as unpublished", fields)
	}

	edited := testSchedule()
	edited.Title, edited.Location = "Design review v2", "Room 5"
	fields := db.UnpublishedFields(edited, &link)
	if !reflect.DeepEqual(fields, []string{"title", "location"}) {
		t.Fatalf("UnpublishedFields() = %v, want exactly the edited fields", fields)
	}

	if fields := db.UnpublishedFields(testSchedule(), nil); fields != nil {
		t.Fatalf("a Misty-only task reported %v as unpublished", fields)
	}
}

func TestMergeGoogleScheduleAppliesRemoteChangeToUntouchedField(t *testing.T) {
	incoming := testSchedule()
	incoming.Location = "Room 9"

	merged, link, conflicts := db.MergeGoogleSchedule(testSchedule(), testLink(), incoming)
	if merged.Location != "Room 9" {
		t.Fatalf("merged location = %q, want Google's value", merged.Location)
	}
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts %v", conflicts)
	}
	if link.Published.Location != "Room 9" {
		t.Fatal("the snapshot must advance for a field taken from Google")
	}
}

func TestMergeGoogleScheduleKeepsLocalEditGoogleDidNotTouch(t *testing.T) {
	local := testSchedule()
	local.Title = "Design review v2"
	incoming := testSchedule()
	incoming.Location = "Room 9"

	merged, link, conflicts := db.MergeGoogleSchedule(local, testLink(), incoming)
	if merged.Title != "Design review v2" {
		t.Fatal("a local edit Google did not touch must survive the merge")
	}
	if merged.Location != "Room 9" {
		t.Fatal("the untouched field should still take Google's value")
	}
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts %v", conflicts)
	}
	if fields := db.UnpublishedFields(merged, &link); !reflect.DeepEqual(fields, []string{"title"}) {
		t.Fatalf("UnpublishedFields() = %v, want the local edit still pending", fields)
	}
}

func TestMergeGoogleScheduleNeverSilentlyOverwritesALocalEdit(t *testing.T) {
	local := testSchedule()
	local.Location = "Room 5"
	incoming := testSchedule()
	incoming.Location = "Room 9"

	merged, link, conflicts := db.MergeGoogleSchedule(local, testLink(), incoming)
	if merged.Location != "Room 5" {
		t.Fatalf("merged location = %q, want the local edit preserved", merged.Location)
	}
	if !reflect.DeepEqual(conflicts, []string{"location"}) {
		t.Fatalf("conflicts = %v, want location reported", conflicts)
	}
	// The snapshot must keep its old value so the disagreement stays visible
	// until the user resolves it.
	if link.Published.Location != "Room 2" {
		t.Fatalf("snapshot location = %q, want the pre-conflict value", link.Published.Location)
	}
	_, _, again := db.MergeGoogleSchedule(merged, link, incoming)
	if !reflect.DeepEqual(again, []string{"location"}) {
		t.Fatalf("re-merging resolved the conflict on its own: %v", again)
	}
}

func TestMergeGoogleScheduleAdoptsEverythingWithoutASnapshot(t *testing.T) {
	link := testLink()
	link.Published = nil
	incoming := testSchedule()
	incoming.Title = "From Google"

	merged, next, conflicts := db.MergeGoogleSchedule(testSchedule(), link, incoming)
	if merged.Title != "From Google" {
		t.Fatal("a first sync should adopt Google's values")
	}
	if len(conflicts) != 0 {
		t.Fatalf("a first sync cannot conflict, got %v", conflicts)
	}
	if next.Published == nil {
		t.Fatal("a first sync must establish the snapshot")
	}
}

func TestGoogleEventPayloadForTimedEvent(t *testing.T) {
	payload := TestingGoogleEventPayload(testSchedule())
	start, _ := payload["start"].(map[string]any)
	if start["dateTime"] != "2026-07-27T15:00:00Z" || start["timeZone"] != "America/Los_Angeles" {
		t.Fatalf("start = %v, want an explicit timezone", start)
	}
	if payload["summary"] != "Design review" {
		t.Fatalf("summary = %v", payload["summary"])
	}
}

func TestGoogleEventPayloadMakesSingleAllDayTaskSpanOneDay(t *testing.T) {
	schedule := testSchedule()
	schedule.AllDay, schedule.StartsAt, schedule.EndsAt = true, "2026-07-27", "2026-07-27"

	payload := TestingGoogleEventPayload(schedule)
	start, _ := payload["start"].(map[string]any)
	end, _ := payload["end"].(map[string]any)
	// Google's all-day end date is exclusive; sending the same day renders a
	// zero-length event.
	if start["date"] != "2026-07-27" || end["date"] != "2026-07-28" {
		t.Fatalf("all-day range = %v..%v, want an exclusive end", start["date"], end["date"])
	}
}

func TestGoogleEventPayloadKeepsMultiDayAllDayRange(t *testing.T) {
	schedule := testSchedule()
	schedule.AllDay, schedule.StartsAt, schedule.EndsAt = true, "2026-07-27", "2026-07-30"

	end, _ := TestingGoogleEventPayload(schedule)["end"].(map[string]any)
	if end["date"] != "2026-07-30" {
		t.Fatalf("multi-day end = %v, want the given date", end["date"])
	}
}

func TestGoogleEventPayloadNeverSendsAnEmptyTitle(t *testing.T) {
	schedule := testSchedule()
	schedule.Title = "   "
	if summary := TestingGoogleEventPayload(schedule)["summary"]; summary != "Untitled" {
		t.Fatalf("summary = %v, want a placeholder", summary)
	}
}

func TestGoogleEventPayloadFallsBackToStartWhenThereIsNoEnd(t *testing.T) {
	schedule := testSchedule()
	schedule.EndsAt = ""
	end, _ := TestingGoogleEventPayload(schedule)["end"].(map[string]any)
	if end["dateTime"] != schedule.StartsAt {
		t.Fatalf("end = %v, want the start time", end["dateTime"])
	}
}

func TestValidateTaskSchedule(t *testing.T) {
	schedule := testSchedule()
	if err := TestingValidateTaskSchedule(&schedule, "UTC"); err != nil {
		t.Fatalf("a valid schedule was rejected: %v", err)
	}

	backwards := testSchedule()
	backwards.EndsAt = "2026-07-27T14:00:00Z"
	if err := TestingValidateTaskSchedule(&backwards, "UTC"); err == nil {
		t.Fatal("an end before the start must be rejected before any write leaves Misty")
	}

	badTime := testSchedule()
	badTime.StartsAt = "not-a-time"
	if err := TestingValidateTaskSchedule(&badTime, "UTC"); err == nil {
		t.Fatal("an unparseable start must be rejected")
	}

	badZone := testSchedule()
	badZone.Timezone = "Mars/Olympus"
	if err := TestingValidateTaskSchedule(&badZone, "UTC"); err == nil {
		t.Fatal("an unknown timezone must be rejected")
	}

	missingZone := testSchedule()
	missingZone.Timezone = ""
	if err := TestingValidateTaskSchedule(&missingZone, "America/New_York"); err != nil {
		t.Fatalf("a missing timezone should fall back to the calendar's: %v", err)
	}
	if missingZone.Timezone != "America/New_York" {
		t.Fatalf("timezone = %q, want the calendar default", missingZone.Timezone)
	}

	allDay := testSchedule()
	allDay.AllDay, allDay.StartsAt, allDay.EndsAt = true, "2026-07-27", "2026-07-28"
	if err := TestingValidateTaskSchedule(&allDay, "UTC"); err != nil {
		t.Fatalf("a valid all-day schedule was rejected: %v", err)
	}
}

func TestDateOnlyNormalizesBothFormats(t *testing.T) {
	if got := TestingDateOnly("2026-07-27T15:00:00Z"); got != "2026-07-27" {
		t.Fatalf("dateOnly(timestamp) = %q", got)
	}
	if got := TestingDateOnly("2026-07-27"); got != "2026-07-27" {
		t.Fatalf("dateOnly(date) = %q", got)
	}
}
