package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	. "github.com/misty-org/misty-server/internal/platform/db"
)

func newestReplayCursor(t *testing.T, database *Database, ctx context.Context, userID string) int64 {
	t.Helper()
	replay, err := database.SpaceEventsReplay(ctx, userID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var cursor int64
	for _, event := range replay.Events {
		cursor = max(cursor, event.ID)
	}
	return cursor
}

func eventsOfType(t *testing.T, database *Database, ctx context.Context, userID, eventType string) []SpaceEvent {
	t.Helper()
	replay, err := database.SpaceEventsReplay(ctx, userID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	out := []SpaceEvent{}
	for _, event := range replay.Events {
		if event.EventType == eventType {
			out = append(out, event)
		}
	}
	return out
}

func TestReplayAheadOfTheServerAsksForResyncAndSaysTheNewestEvent(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("replay-ahead@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Replay ahead")
	if _, _, err := database.TestingCreateSpaceMessage(ctx, owner.ID, space.ID, []MessageSpan{{Type: "text", Text: "hello"}}, nil); err != nil {
		t.Fatal(err)
	}
	newest := newestReplayCursor(t, database, ctx, owner.ID)
	if newest == 0 {
		t.Fatal("no events were recorded")
	}

	replay, err := database.SpaceEventsReplay(ctx, owner.ID, newest+1000, 500)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Resync || replay.Newest != newest || len(replay.Events) != 0 {
		t.Fatalf("cursor ahead: resync=%v newest=%d events=%d, want resync newest=%d", replay.Resync, replay.Newest, len(replay.Events), newest)
	}

	current, err := database.SpaceEventsReplay(ctx, owner.ID, newest, 500)
	if err != nil {
		t.Fatal(err)
	}
	if current.Resync || current.Newest != newest {
		t.Fatalf("cursor at the newest event: resync=%v newest=%d", current.Resync, current.Newest)
	}
}

func TestReplayRepeatsTheLastFewMinutesSoALateCommitIsNotSkipped(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("replay-overlap@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Replay overlap")
	if _, _, err := database.TestingCreateSpaceMessage(ctx, owner.ID, space.ID, []MessageSpan{{Type: "text", Text: "old"}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.TestingCreateSpaceMessage(ctx, owner.ID, space.ID, []MessageSpan{{Type: "text", Text: "late"}}, nil); err != nil {
		t.Fatal(err)
	}
	created := eventsOfType(t, database, ctx, owner.ID, "message.created")
	if len(created) != 2 {
		t.Fatalf("message.created events = %d, want 2", len(created))
	}
	if err := database.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE space_events SET created_at=NOW()-INTERVAL '10 minutes' WHERE id=$1`, created[0].ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	cursor := newestReplayCursor(t, database, ctx, owner.ID)

	replay, err := database.SpaceEventsReplay(ctx, owner.ID, cursor, 500)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Resync || len(replay.Events) != 0 {
		t.Fatalf("cursor at the newest event: resync=%v events=%d", replay.Resync, len(replay.Events))
	}
	sawLate, sawOld := false, false
	var previous int64
	for _, event := range replay.Recent {
		if event.ID > cursor || event.ID <= previous {
			t.Fatalf("recent events out of order or past the cursor: %d after %d (cursor %d)", event.ID, previous, cursor)
		}
		previous = event.ID
		sawLate = sawLate || event.ID == created[1].ID
		sawOld = sawOld || event.ID == created[0].ID
	}
	if !sawLate || sawOld {
		t.Fatalf("recent events: late=%v old=%v, want only the last few minutes", sawLate, sawOld)
	}

	fresh, err := database.SpaceEventsReplay(ctx, owner.ID, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh.Recent) != 0 {
		t.Fatalf("a fresh replay repeated %d recent events", len(fresh.Recent))
	}
}

func TestReplayPastThePurgeWatermarkAsksForResync(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("replay-purged@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Replay purged")
	cursor := newestReplayCursor(t, database, ctx, owner.ID)

	if _, _, err := database.TestingCreateSpaceMessage(ctx, owner.ID, space.ID, []MessageSpan{{Type: "text", Text: "old"}}, nil); err != nil {
		t.Fatal(err)
	}
	old := eventsOfType(t, database, ctx, owner.ID, "message.created")
	if len(old) != 1 {
		t.Fatalf("message.created events = %d, want 1", len(old))
	}
	if _, _, err := database.TestingCreateSpaceMessage(ctx, owner.ID, space.ID, []MessageSpan{{Type: "text", Text: "new"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := database.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE space_events SET created_at=NOW()-INTERVAL '8 days' WHERE id=$1`, old[0].ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.PurgeExpiredSpaceData(ctx, 1000); err != nil {
		t.Fatal(err)
	}

	behind, err := database.SpaceEventsReplay(ctx, owner.ID, cursor, 500)
	if err != nil {
		t.Fatal(err)
	}
	if !behind.Resync {
		t.Fatal("a cursor behind purged events was not asked to resync")
	}

	past, err := database.SpaceEventsReplay(ctx, owner.ID, old[0].ID, 500)
	if err != nil {
		t.Fatal(err)
	}
	if past.Resync || len(past.Events) == 0 {
		t.Fatalf("a cursor past the purged events: resync=%v events=%d", past.Resync, len(past.Events))
	}
}

func TestConcurrentReactionsSendCountsTakenUnderTheMessageLock(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("reaction-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Reactions")
	reactors := []string{owner.ID}
	for i := 0; i < 5; i++ {
		member, err := database.CreateUser(fmt.Sprintf("reaction-member-%d@example.com", i), "password123")
		if err != nil {
			t.Fatal(err)
		}
		join(t, database, ctx, owner.ID, space.ID, member)
		reactors = append(reactors, member.ID)
	}
	message, _, err := database.TestingCreateSpaceMessage(ctx, owner.ID, space.ID, []MessageSpan{{Type: "text", Text: "react"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(reactors))
	for _, userID := range reactors {
		wg.Add(1)
		go func(userID string) {
			defer wg.Done()
			if _, err := database.AddSpaceMessageReaction(ctx, userID, space.ID, message.ID, "👍"); err != nil {
				errs <- err
			}
		}(userID)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	events := eventsOfType(t, database, ctx, owner.ID, "message.reaction_added")
	if len(events) != len(reactors) {
		t.Fatalf("reaction events = %d, want %d", len(events), len(reactors))
	}
	for index, event := range events {
		var payload struct {
			Reactions []struct {
				Emoji string `json:"emoji"`
				Count int    `json:"count"`
			} `json:"reactions"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Reactions) != 1 || payload.Reactions[0].Count != index+1 {
			t.Fatalf("reaction event %d carried %+v, want a count of %d", index, payload.Reactions, index+1)
		}
	}
}

func TestReadMarkerRecordsAnEventOnlyWhenItMoves(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("read-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := database.CreateUser("read-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Reads")
	join(t, database, ctx, owner.ID, space.ID, reader)
	message, _, err := database.TestingCreateSpaceMessage(ctx, owner.ID, space.ID, []MessageSpan{{Type: "text", Text: "read me"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := database.MarkSpaceRead(ctx, reader.ID, space.ID, message.Seq); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if err := database.MarkSpaceRead(ctx, reader.ID, space.ID, message.Seq-1); err != nil {
		t.Fatal(err)
	}

	reads := eventsOfType(t, database, ctx, reader.ID, "read.updated")
	if len(reads) != 1 {
		t.Fatalf("read.updated events = %d, want 1", len(reads))
	}
}
