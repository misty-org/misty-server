package db

import (
	"context"
	"encoding/json"
	"testing"
	"time"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)


import ()

func TestPersonalAgentRuntimeIsFIFOAndSingleActivePerAgent(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Runtime Owner", "runtime-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Runtime Scheduling")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("Other Ask", "other-scheduler@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	otherSpace, err := database.TestingCreateSpace(ctx, other.ID, "Other Ask")
	if err != nil {
		t.Fatal(err)
	}
	firstAgent, err := database.EnsureAskIdentity(ctx, owner.ID, "google/gemini-2.5-flash-lite")
	if err != nil {
		t.Fatal(err)
	}
	secondAgent, err := database.EnsureAskIdentity(ctx, other.ID, "google/gemini-2.5-flash-lite")
	if err != nil {
		t.Fatal(err)
	}
	queue := func(agent *AskIdentity, title string) *SpaceRun {
		spaceID := space.ID
		if agent.ID == secondAgent.ID {
			spaceID = otherSpace.ID
		}
		run, err := database.CreateCreatorAgentRun(ctx, agent.OwnerUserID, spaceID, agent.ID, CreatorAgentRunInput{Instruction: title})
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	first := queue(firstAgent, "First task")
	second := queue(firstAgent, "Second task")
	third := queue(firstAgent, "Third task")
	parallel := queue(secondAgent, "Parallel task")

	claimed, err := database.ClaimPersonalAgentTaskRunJobs(ctx, "scheduler-a", 4, time.Minute)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("initial claims = %#v, %v", claimed, err)
	}
	byAgent := map[string]PersonalAgentTaskRunJob{}
	for _, job := range claimed {
		byAgent[job.Run.AgentID] = job
	}
	if byAgent[firstAgent.ID].Run.ID != first.ID || byAgent[secondAgent.ID].Run.ID != parallel.ID {
		t.Fatalf("initial scheduler order = %#v", byAgent)
	}
	if _, err := database.ActivatePersonalAgentTaskRuntime(ctx, first.ID, "vercel-workflow", "workflow-first"); err != nil {
		t.Fatal(err)
	}
	if marked, err := database.MarkPersonalAgentTaskRunDispatched(ctx, first.ID, "scheduler-a", "vercel-workflow", "workflow-first"); err != nil || marked.RuntimeRunID != "workflow-first" {
		t.Fatalf("record dispatch after the runtime activated first = %#v, %v", marked, err)
	}
	if _, err := database.ActivatePersonalAgentTaskRuntime(ctx, parallel.ID, "vercel-workflow", "workflow-parallel"); err != nil {
		t.Fatal(err)
	}
	blocked, err := database.ClaimPersonalAgentTaskRunJobs(ctx, "scheduler-b", 4, time.Minute)
	if err != nil || len(blocked) != 0 {
		t.Fatalf("claims while both agents active = %#v, %v", blocked, err)
	}
	if _, err := database.FinishSpaceRun(ctx, first.ID, "completed", json.RawMessage(`{"text":"done"}`), ""); err != nil {
		t.Fatal(err)
	}
	if err := database.FinishDispatchedPersonalAgentTaskRunJob(ctx, first.ID, "workflow-first", "completed"); err != nil {
		t.Fatal(err)
	}
	next, err := database.ClaimPersonalAgentTaskRunJobs(ctx, "scheduler-c", 1, time.Minute)
	if err != nil || len(next) != 1 || next[0].Run.ID != second.ID {
		t.Fatalf("second FIFO claim = %#v, %v", next, err)
	}
	if third.ID == second.ID {
		t.Fatal("expected distinct queued runs")
	}
}
