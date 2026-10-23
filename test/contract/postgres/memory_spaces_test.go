package db

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	. "github.com/kannachi323/misty/server/internal/platform/db"
)

// The memory tree (docs/plans/spaces-as-profiles.md, phase 7): one per person
// per Space on the server, private to its owner, searched by meaning.

const memoryTestModel = "test/embedding"

type memoryTestFixture struct {
	database          *Database
	owner, teammate   *User
	personal, work    string
	teammateOnlySpace string
}

func newMemoryTestFixture(t *testing.T) memoryTestFixture {
	t.Helper()
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("tree-"+uuid.NewString()+"@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	teammate, err := database.CreateUser("tree-"+uuid.NewString()+"@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	personal, err := database.MemorySpace(ctx, owner.ID, "")
	if err != nil || personal == "" {
		t.Fatalf("personal Space = %q, %v", personal, err)
	}
	work := createTestSpace(t, database, ctx, owner.ID, "Work")
	addTestSpaceMember(t, database, work.ID, teammate.ID)
	elsewhere := createTestSpace(t, database, ctx, teammate.ID, "Teammate only")
	return memoryTestFixture{database: database, owner: owner, teammate: teammate, personal: personal, work: work.ID, teammateOnlySpace: elsewhere.ID}
}

// memoryTestVector points along one axis, with a little of a second.
func memoryTestVector(axis int, mix ...float64) []float64 {
	vector := make([]float64, 768)
	vector[axis] = 1
	if len(mix) == 2 {
		vector[int(mix[0])] = mix[1]
	}
	return vector
}

// embedPendingMemory runs the background embedder by hand: every due chunk
// gets a vector chosen by its words.
func embedPendingMemory(t *testing.T, database *Database, axis func(text string) int) int {
	t.Helper()
	ctx := context.Background()
	embedded := 0
	for {
		chunks, err := database.PendingMemoryChunks(ctx, 32)
		if err != nil {
			t.Fatal(err)
		}
		if len(chunks) == 0 {
			return embedded
		}
		owner := chunks[0].OwnerUserID
		for _, chunk := range chunks {
			if chunk.OwnerUserID != owner {
				t.Fatal("one embedding batch mixed owners")
			}
			if err := database.CompleteMemoryChunk(ctx, chunk, memoryTestVector(axis(chunk.Text)), memoryTestModel); err != nil {
				t.Fatal(err)
			}
			embedded++
		}
	}
}

func TestMemoryFactsAreFoundByMeaningOnlyInTheirSpace(t *testing.T) {
	f := newMemoryTestFixture(t)
	ctx := context.Background()
	aisle, err := f.database.RememberMistyMemory(ctx, f.owner.ID, RememberMistyMemoryInput{SpaceID: f.work, Kind: "preference", Content: "I like aisle seats"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.RememberMistyMemory(ctx, f.owner.ID, RememberMistyMemoryInput{SpaceID: f.work, Kind: "instruction", Content: "Send the status report on Fridays"}); err != nil {
		t.Fatal(err)
	}
	// The same words in another Space are another memory.
	home, err := f.database.RememberMistyMemory(ctx, f.owner.ID, RememberMistyMemoryInput{Kind: "preference", Content: "I like aisle seats"})
	if err != nil || home.ID == aisle.ID || home.SpaceID != f.personal || aisle.SpaceID != f.work {
		t.Fatalf("personal memory = %+v (work %+v), %v", home, aisle, err)
	}
	if _, err := f.database.RememberMistyMemory(ctx, f.owner.ID, RememberMistyMemoryInput{SpaceID: f.teammateOnlySpace, Content: "Not my Space"}); err == nil {
		t.Fatal("memory written into a Space the owner isn't in")
	}
	if _, err := f.database.MemorySpace(ctx, f.owner.ID, f.teammateOnlySpace); err == nil {
		t.Fatal("a Space the owner isn't in resolved as theirs")
	}

	work, err := f.database.MistyMemories(ctx, f.owner.ID, f.work, 100)
	if err != nil || len(work) != 2 || hasMistyMemory(work, home.ID) {
		t.Fatalf("Work memories = %+v, %v", work, err)
	}
	if personal, _ := f.database.MistyMemories(ctx, f.owner.ID, "", 100); len(personal) != 1 || personal[0].ID != home.ID {
		t.Fatalf("Personal memories = %+v", personal)
	}
	if theirs, _ := f.database.MistyMemories(ctx, f.teammate.ID, f.work, 100); len(theirs) != 0 {
		t.Fatalf("a teammate sees the owner's Work memory: %+v", theirs)
	}

	// Vectors are made in the background, with no device online.
	if n := embedPendingMemory(t, f.database, func(text string) int {
		switch {
		case strings.Contains(text, "aisle"):
			return 0
		case strings.Contains(text, "Friday"):
			return 1
		}
		return 2
	}); n != 3 {
		t.Fatalf("embedded %d chunks, want 3", n)
	}
	// "my seat preference" lands near "I like aisle seats".
	query := memoryTestVector(0, 1, 0.2)
	hits, err := f.database.SearchMemory(ctx, f.owner.ID, f.work, []string{MemoryFacts}, query, memoryTestModel, 5)
	if err != nil || len(hits) != 2 || hits[0].RecordID != aisle.ID || hits[0].Distance > hits[1].Distance {
		t.Fatalf("Work search = %+v, %v", hits, err)
	}
	if hits, _ := f.database.SearchMemory(ctx, f.owner.ID, f.personal, []string{MemoryFacts}, query, memoryTestModel, 5); len(hits) != 1 || hits[0].RecordID != home.ID {
		t.Fatalf("Personal search = %+v", hits)
	}
	if hits, _ := f.database.SearchMemory(ctx, f.teammate.ID, f.work, []string{MemoryFacts}, query, memoryTestModel, 5); len(hits) != 0 {
		t.Fatalf("a teammate found the owner's memory: %+v", hits)
	}
	if hits, _ := f.database.SearchMemory(ctx, f.owner.ID, f.work, []string{MemoryFacts}, nil, memoryTestModel, 5); len(hits) != 0 {
		t.Fatal("search without a vector matched words")
	}

	// Forgetting removes it from search too.
	if err := f.database.ForgetMistyMemory(ctx, f.owner.ID, f.personal, aisle.ID); err == nil {
		t.Fatal("a Work memory was forgotten from Personal")
	}
	if err := f.database.ForgetMistyMemory(ctx, f.owner.ID, f.work, aisle.ID); err != nil {
		t.Fatal(err)
	}
	if hits, _ := f.database.SearchMemory(ctx, f.owner.ID, f.work, []string{MemoryFacts}, query, memoryTestModel, 5); len(hits) != 1 || hits[0].RecordID == aisle.ID {
		t.Fatalf("forgotten memory still found: %+v", hits)
	}
}

func TestMemoryMomentsStayInTheWindowsSpace(t *testing.T) {
	f := newMemoryTestFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Minute)
	page := func(space, address, body string) MemoryEvent {
		t.Helper()
		event, err := f.database.RecordMemoryEvent(ctx, f.owner.ID, MemoryEvent{ID: uuid.NewString(), SpaceID: space, Kind: "page", URL: address,
			Title: "Page", Body: body, Simhash: "00000000000000ff", OccurredAt: at}, 10)
		if err != nil {
			t.Fatal(err)
		}
		return event
	}
	work := page(f.work, "https://news.example/bikes", "The council voted to expand bike lanes.")
	// The same page in another Space is its own visit, not a revisit.
	home := page("", "https://news.example/bikes", "The council voted to expand bike lanes.")
	if home.SameAs != "" || home.SpaceID != f.personal || work.SpaceID != f.work {
		t.Fatalf("visits = %+v / %+v", work, home)
	}
	if events, err := f.database.MemoryEvents(ctx, f.owner.ID, MemoryQuery{SpaceID: f.work}); err != nil || len(events) != 1 || events[0].ID != work.ID {
		t.Fatalf("Work timeline = %+v, %v", events, err)
	}
	if events, _ := f.database.MemoryEvents(ctx, f.owner.ID, MemoryQuery{}); len(events) != 1 || events[0].ID != home.ID {
		t.Fatalf("Personal timeline = %+v", events)
	}
	if events, _ := f.database.MemoryEvents(ctx, f.teammate.ID, MemoryQuery{SpaceID: f.work}); len(events) != 0 {
		t.Fatalf("a teammate sees the owner's moments: %+v", events)
	}

	// Moments wait for their hourly batch before they cost anything.
	if n := embedPendingMemory(t, f.database, func(string) int { return 0 }); n != 0 {
		t.Fatalf("embedded %d moments before their batch was due", n)
	}
	if waiting, _ := f.database.MemoryBacklogCount(ctx, f.owner.ID); waiting != 2 {
		t.Fatalf("backlog = %d, want 2", waiting)
	}
	if err := f.database.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE memory_chunks SET due_at=now() WHERE owner_user_id=$1`, f.owner.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n := embedPendingMemory(t, f.database, func(string) int { return 0 }); n != 2 {
		t.Fatalf("embedded %d moments, want 2", n)
	}
	if waiting, _ := f.database.MemoryBacklogCount(ctx, f.owner.ID); waiting != 0 {
		t.Fatalf("backlog after embedding = %d", waiting)
	}
	if hits, _ := f.database.SearchMemory(ctx, f.owner.ID, f.work, []string{MemoryMoments}, memoryTestVector(0), memoryTestModel, 5); len(hits) != 1 || hits[0].RecordID != work.ID {
		t.Fatalf("Work moment search = %+v", hits)
	}

	// Forgetting everything is per Space.
	if n, err := f.database.ForgetMemory(ctx, f.owner.ID, MemoryForget{All: true}); err != nil || n != 1 {
		t.Fatalf("forget Personal = %d, %v", n, err)
	}
	if events, _ := f.database.MemoryEvents(ctx, f.owner.ID, MemoryQuery{SpaceID: f.work}); len(events) != 1 {
		t.Fatalf("forgetting Personal touched Work: %+v", events)
	}
	if hits, _ := f.database.SearchMemory(ctx, f.owner.ID, f.personal, []string{MemoryMoments}, memoryTestVector(0), memoryTestModel, 5); len(hits) != 0 {
		t.Fatalf("forgotten moment still found: %+v", hits)
	}
}

func TestMemoryClosesWhenItsOwnerLeavesTheSpace(t *testing.T) {
	f := newMemoryTestFixture(t)
	ctx := context.Background()
	if _, err := f.database.RememberMistyMemory(ctx, f.teammate.ID, RememberMistyMemoryInput{SpaceID: f.work, Content: "Standup is at ten"}); err != nil {
		t.Fatal(err)
	}
	if err := f.database.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM space_members WHERE space_id=$1 AND user_id=$2`, f.work, f.teammate.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if theirs, _ := f.database.MistyMemories(ctx, f.teammate.ID, f.work, 100); len(theirs) != 0 {
		t.Fatalf("memory stayed open after leaving: %+v", theirs)
	}
	if _, err := f.database.MemorySpace(ctx, f.teammate.ID, f.work); err == nil {
		t.Fatal("a Space the person left still resolved")
	}
}
