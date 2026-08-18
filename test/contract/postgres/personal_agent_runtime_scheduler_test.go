package db

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestPersonalAgentRuntimeIsFIFOAndSingleActivePerAgent(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Runtime Owner", "runtime-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Runtime Scheduling")
	if err != nil {
		t.Fatal(err)
	}
	createAgent := func(name string) *PersonalAgent {
		agent, createErr := database.CreatePersonalAgent(ctx, owner.ID, PersonalAgent{Name: name, ModelMode: "pinned", ModelID: "google/gemini-2.5-flash-lite"})
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, createErr = database.AddSpaceAgentMembership(ctx, owner.ID, space.ID, SpaceAgentMembershipInput{AgentID: agent.ID}); createErr != nil {
			t.Fatal(createErr)
		}
		return agent
	}
	firstAgent, secondAgent := createAgent("First Runtime Agent"), createAgent("Second Runtime Agent")
	queue := func(agent *PersonalAgent, title string) *SpaceRun {
		task, createErr := database.CreateSpaceTask(ctx, owner.ID, SpaceTask{SpaceID: space.ID, Title: title, Status: "todo", AssigneeAgentID: agent.ID})
		if createErr != nil {
			t.Fatal(createErr)
		}
		run, claimed, createErr := database.ClaimAssignedAgentTaskRun(ctx, owner.ID, *task)
		if createErr != nil || !claimed {
			t.Fatalf("queue %q = %#v, claimed:%v, err:%v", title, run, claimed, createErr)
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

func TestPersonalAgentRuntimeCancelAndRetryAreOwnerScoped(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Cancel Owner", "cancel-runtime-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	intruder, err := database.CreateUser("Other Owner", "other-runtime-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Cancel Runtime")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := database.CreatePersonalAgent(ctx, owner.ID, PersonalAgent{Name: "Cancelable Agent", ModelMode: "pinned", ModelID: "google/gemini-2.5-flash-lite"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.AddSpaceAgentMembership(ctx, owner.ID, space.ID, SpaceAgentMembershipInput{AgentID: agent.ID}); err != nil {
		t.Fatal(err)
	}
	task, err := database.CreateSpaceTask(ctx, owner.ID, SpaceTask{SpaceID: space.ID, Title: "Cancelable task", Status: "todo", AssigneeAgentID: agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	run, claimed, err := database.ClaimAssignedAgentTaskRun(ctx, owner.ID, *task)
	if err != nil || !claimed {
		t.Fatalf("assignment run = %#v, %v, %v", run, claimed, err)
	}
	jobs, err := database.ClaimPersonalAgentTaskRunJobs(ctx, "cancel-worker", 1, time.Minute)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claimed job = %#v, %v", jobs, err)
	}
	if _, err := database.ActivatePersonalAgentTaskRuntime(ctx, run.ID, "vercel-workflow", "workflow-cancel"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CancelPersonalAgentTaskRunForOwner(ctx, intruder.ID, run.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("cross-owner cancel = %v, want not found", err)
	}
	canceled, err := database.CancelPersonalAgentTaskRunForOwner(ctx, owner.ID, run.ID)
	if err != nil || canceled.State != "canceled" {
		t.Fatalf("owner cancel = %#v, %v", canceled, err)
	}
	if _, _, err := database.ValidatePersonalAgentTaskRuntime(ctx, run.ID, "workflow-cancel"); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("late runtime validation = %v, want forbidden", err)
	}
	retried, err := database.RetryPersonalAgentTaskRunForOwner(ctx, owner.ID, run.ID)
	if err != nil || retried.State != "queued" || retried.RetryOfRunID != run.ID || retried.ID == run.ID {
		t.Fatalf("retried run = %#v, %v", retried, err)
	}
	if _, err := database.RetryPersonalAgentTaskRunForOwner(ctx, intruder.ID, run.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("cross-owner retry = %v, want not found", err)
	}
}
