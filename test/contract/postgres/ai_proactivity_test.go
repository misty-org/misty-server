package db

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestMistyProactivityRequiresOptInAndEnforcesCooldownSnooze(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Proactive Owner", "proactive-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 26, 18, 0, 0, 0, time.UTC)
	if _, err := database.RecordAIProactiveEvent(ctx, user.ID, "inbox", "shown", 0, now); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("non-opted-in surface showed a suggestion: %v", err)
	}
	preference, err := database.UpsertAISurfacePreference(ctx, user.ID, AISurfacePreference{
		SurfaceID: "inbox", Proactive: true, SavedActions: json.RawMessage(`[]`),
	})
	if err != nil || preference.ProactiveCooldownMinutes != 360 {
		t.Fatalf("proactive preference = %#v, %v", preference, err)
	}
	shown, err := database.RecordAIProactiveEvent(ctx, user.ID, "inbox", "shown", 0, now)
	if err != nil || shown.ProactiveLastShownAt == nil || !shown.ProactiveLastShownAt.Equal(now) {
		t.Fatalf("first proactive show = %#v, %v", shown, err)
	}
	if _, err := database.RecordAIProactiveEvent(ctx, user.ID, "inbox", "shown", 0, now.Add(time.Hour)); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("cooldown did not suppress a repeat: %v", err)
	}
	snoozed, err := database.RecordAIProactiveEvent(ctx, user.ID, "inbox", "snoozed", 1440, now.Add(time.Hour))
	if err != nil || snoozed.ProactiveSnoozedUntil == nil {
		t.Fatalf("snooze = %#v, %v", snoozed, err)
	}
	if _, err := database.RecordAIProactiveEvent(ctx, user.ID, "inbox", "shown", 0, now.Add(12*time.Hour)); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("snooze did not suppress a suggestion: %v", err)
	}
	if _, err := database.RecordAIProactiveEvent(ctx, user.ID, "inbox", "shown", 0, now.Add(26*time.Hour)); err != nil {
		t.Fatalf("suggestion did not return after snooze and cooldown: %v", err)
	}
	dismissed, err := database.RecordAIProactiveEvent(ctx, user.ID, "inbox", "dismissed", 0, now.Add(27*time.Hour))
	if err != nil || dismissed.ProactiveDismissedAt == nil || dismissed.ProactiveSnoozedUntil == nil {
		t.Fatalf("dismissal = %#v, %v", dismissed, err)
	}
	if _, err := database.RecordAIProactiveEvent(ctx, user.ID, "inbox", "shown", 0, now.Add(3*24*time.Hour)); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("dismissal did not suppress the suggestion: %v", err)
	}
}
