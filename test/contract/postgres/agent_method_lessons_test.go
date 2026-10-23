package db

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	. "github.com/kannachi323/misty/server/internal/platform/db"
)

// Learned steps belong to the workflow's owner, version on every save, keep the
// newest ten, and never change the person's own instructions.
func TestAgentMethodLessonsVersionPerOwner(t *testing.T) {
	database := openTestDatabase(t)
	ctx := t.Context()
	owner, err := database.CreateUser("lesson-"+uuid.NewString()+"@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("lesson-"+uuid.NewString()+"@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := database.SavePersonalAgent(ctx, owner.ID, "", AgentProfileInput{Name: "Research", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	definition := AgentMethodDefinition{Title: "Weekly brief", Instructions: "Summarize the week.", Target: "cloud", Inputs: []AgentMethodInput{}, RequiredTools: []string{}}
	method, err := database.SaveAgentMethod(ctx, owner.ID, AgentMethod{AgentID: agent.ID, Kind: "workflow", Enabled: true, Definition: definition}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if latest, err := database.LatestAgentMethodLesson(ctx, owner.ID, method.ID); err != nil || latest.Version != 0 {
		t.Fatalf("a new workflow has no lessons: %+v %v", latest, err)
	}
	for index := 1; index <= 12; index++ {
		lesson, err := database.AddAgentMethodLesson(ctx, owner.ID, method.ID, fmt.Sprintf("1. step version %d", index), "")
		if err != nil || lesson.Version != index {
			t.Fatalf("version %d: %+v %v", index, lesson, err)
		}
	}
	latest, err := database.LatestAgentMethodLesson(ctx, owner.ID, method.ID)
	if err != nil || latest.Version != 12 || latest.Steps != "1. step version 12" {
		t.Fatalf("newest lesson = %+v %v", latest, err)
	}
	if _, err := database.AddAgentMethodLesson(ctx, other.ID, method.ID, "1. hijack", ""); err == nil {
		t.Fatal("another person added steps to this workflow")
	}
	if foreign, err := database.LatestAgentMethodLesson(ctx, other.ID, method.ID); err != nil || foreign.Version != 0 {
		t.Fatalf("another person read this workflow's steps: %+v %v", foreign, err)
	}
	if _, err := database.AddAgentMethodLesson(ctx, owner.ID, method.ID, "   ", ""); err == nil {
		t.Fatal("empty steps were saved")
	}
	methods, err := database.AgentMethods(ctx, owner.ID, "", agent.ID)
	if err != nil || len(methods) != 1 || methods[0].Definition.Instructions != "Summarize the week." {
		t.Fatalf("learning changed the person's instructions: %+v %v", methods, err)
	}
}
