package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	. "github.com/kannachi323/misty/server/internal/platform/db"
)

func TestRecapsAndWorkflowsStayInTheirSpace(t *testing.T) {
	f := newMemoryTestFixture(t)
	ctx := context.Background()
	recap := AIRecap{SurfaceID: "global", Enabled: true, Cadence: "daily", LocalTime: "08:00", Timezone: "UTC", Prompt: "Summarize"}
	recap.SpaceID = f.work
	if _, err := f.database.UpsertAIRecap(ctx, f.owner.ID, recap, time.Now()); err != nil {
		t.Fatal(err)
	}
	recap.SpaceID = ""
	if _, err := f.database.UpsertAIRecap(ctx, f.owner.ID, recap, time.Now()); err != nil {
		t.Fatal(err)
	}
	for space, want := range map[string]string{f.work: f.work, "": f.personal} {
		items, err := f.database.AIRecaps(ctx, f.owner.ID, space)
		if err != nil || len(items) != 1 || items[0].SpaceID != want {
			t.Fatalf("recaps in %q = %+v, %v", space, items, err)
		}
	}

	agent, err := f.database.SavePersonalAgent(ctx, f.owner.ID, "", AgentProfileInput{Name: "Planner", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	definition := AgentMethodDefinition{Title: "Book travel", Instructions: "Book the trip with an aisle seat.", Target: "cloud", Inputs: []AgentMethodInput{}, RequiredTools: []string{}}
	method, err := f.database.SaveAgentMethod(ctx, f.owner.ID, AgentMethod{SpaceID: f.work, AgentID: agent.ID, Kind: "workflow", Enabled: true, Definition: definition}, 0)
	if err != nil || method.SpaceID != f.work {
		t.Fatalf("saved workflow = %+v, %v", method, err)
	}
	if listed, _ := f.database.AgentMethods(ctx, f.owner.ID, "", agent.ID); len(listed) != 0 {
		t.Fatalf("a Work workflow is listed in Personal: %+v", listed)
	}
	if listed, _ := f.database.AgentMethods(ctx, f.owner.ID, f.work, agent.ID); len(listed) != 1 || listed[0].ID != method.ID {
		t.Fatalf("Work workflows = %+v", listed)
	}
	moved := method
	moved.SpaceID = f.personal
	if _, err := f.database.SaveAgentMethod(ctx, f.owner.ID, moved, method.Version); err == nil {
		t.Fatal("a workflow moved between Spaces")
	}
	if _, err := f.database.AddAgentMethodLesson(ctx, f.owner.ID, method.ID, "1. Open the airline site.", ""); err != nil {
		t.Fatal(err)
	}
	var lessonSpace, versionSpace string
	if err := f.database.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT space_id FROM agent_method_lessons WHERE method_id=$1`, method.ID).Scan(&lessonSpace); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT space_id FROM agent_method_versions WHERE id=$1`, method.VersionID).Scan(&versionSpace)
	}); err != nil || lessonSpace != f.work || versionSpace != f.work {
		t.Fatalf("lesson in %q, version in %q, %v", lessonSpace, versionSpace, err)
	}
	// The workflow is indexed for search by meaning in its Space.
	var indexed int
	if err := f.database.SpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM memory_chunks WHERE branch='workflows' AND record_id=$1 AND space_id=$2`, method.ID, f.work).Scan(&indexed)
	}); err != nil || indexed != 1 {
		t.Fatalf("workflow chunks = %d, %v", indexed, err)
	}
}

func TestMemoryEventsPointRevisitsCapAndForgetRanges(t *testing.T) {
	f := newMemoryTestFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	visit := func(address, hash, body string, at time.Duration) MemoryEvent {
		t.Helper()
		e, err := f.database.RecordMemoryEvent(ctx, f.owner.ID, MemoryEvent{ID: uuid.NewString(), SpaceID: f.work, Kind: "page", URL: address, Title: "Bike lanes",
			Body: body, Simhash: hash, OccurredAt: base.Add(at)}, 3)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	first := visit("https://www.news.example/bikes", "00000000000000ff", "The council voted to expand bike lanes.", 0)
	if first.Site != "news.example" || first.SameAs != "" {
		t.Fatalf("first visit = %+v", first)
	}
	// Two bits apart: the same page, so the revisit points at the first.
	again := visit("https://www.news.example/bikes", "00000000000000fc", "The council voted to expand bike lanes. 1 minute ago", time.Hour)
	if again.SameAs != first.ID || again.Body != "" {
		t.Fatalf("revisit = %+v", again)
	}
	// Far apart: a changed page keeps its own text.
	changed := visit("https://www.news.example/bikes", "ffffffffffff0000", "Update: the vote was delayed.", 2*time.Hour)
	if changed.SameAs != "" {
		t.Fatalf("changed page pointed at %s", changed.SameAs)
	}
	if waiting, _ := f.database.MemoryBacklogCount(ctx, f.owner.ID); waiting != 2 {
		t.Fatalf("backlog = %d, want 2 (revisits wait for nothing)", waiting)
	}
	var chunks int
	if err := f.database.SpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM memory_chunks WHERE branch='moments' AND owner_user_id=$1 AND space_id=$2`, f.owner.ID, f.work).Scan(&chunks)
	}); err != nil || chunks != 2 {
		t.Fatalf("indexed %d moments, want 2: %v", chunks, err)
	}
	visit("https://other.example/", "0f0f0f0f0f0f0f0f", "Recipes.", 3*time.Hour)
	// The cap is 3: the fourth event with text is refused, plainly.
	if _, err := f.database.RecordMemoryEvent(ctx, f.owner.ID, MemoryEvent{ID: uuid.NewString(), SpaceID: f.work, Kind: "page", URL: "https://x.example/", Title: "X", Body: "x", OccurredAt: base}, 3); !errors.Is(err, ErrMemoryBacklogFull) {
		t.Fatalf("over the cap = %v", err)
	}
	if _, err := f.database.RecordMemoryEvent(ctx, f.owner.ID, MemoryEvent{ID: uuid.NewString(), SpaceID: f.work, Kind: "page", URL: "file:///etc/passwd", OccurredAt: base}, 3); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("file URL = %v", err)
	}
	// Forgetting the first hour keeps the revisit, which now holds the text.
	if n, err := f.database.ForgetMemory(ctx, f.owner.ID, MemoryForget{SpaceID: f.work, From: base, To: base.Add(30 * time.Minute)}); err != nil || n != 1 {
		t.Fatalf("forget range = %d, %v", n, err)
	}
	kept, _ := f.database.MemoryEvents(ctx, f.owner.ID, MemoryQuery{SpaceID: f.work, From: base.Add(-time.Hour), Site: "news.example"})
	if len(kept) != 2 || kept[1].ID != again.ID || kept[1].SameAs != "" || kept[1].Body == "" {
		t.Fatalf("after forgetting the range = %+v", kept)
	}
	if n, err := f.database.ForgetMemory(ctx, f.owner.ID, MemoryForget{SpaceID: f.work, Site: "news.example"}); err != nil || n != 2 {
		t.Fatalf("forget site = %d, %v", n, err)
	}
	if _, err := f.database.ForgetMemory(ctx, f.owner.ID, MemoryForget{SpaceID: f.work}); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("forget nothing = %v", err)
	}
	if n, err := f.database.ForgetMemory(ctx, f.owner.ID, MemoryForget{SpaceID: f.work, All: true}); err != nil || n != 1 {
		t.Fatalf("forget all = %d, %v", n, err)
	}
	if waiting, _ := f.database.MemoryBacklogCount(ctx, f.owner.ID); waiting != 0 {
		t.Fatalf("backlog after forgetting everything = %d", waiting)
	}
	if err := f.database.SpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM memory_chunks WHERE branch='moments' AND owner_user_id=$1`, f.owner.ID).Scan(&chunks)
	}); err != nil || chunks != 0 {
		t.Fatalf("forgotten moments left %d chunks, %v", chunks, err)
	}
}
