package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/db"
)

func TestTeachModeAcceptsALessonOnlyOnLessonTurns(t *testing.T) {
	lesson := &aiLessonState{ID: "conv-1", Title: "two sum", Objectives: []aiLessonObjective{{ID: "o1", Title: "make a dict", Status: "in_progress"}}, AnswerPolicy: "hints", HintLevel: "nudge"}
	body := aiInvocationInput{CollaborationMode: "act", Lesson: lesson}
	if validateTeachInput(&body) == nil {
		t.Fatal("lesson state accepted outside teach mode")
	}
	body.CollaborationMode = db.ConversationModeTeach
	if err := validateTeachInput(&body); err != nil {
		t.Fatal(err)
	}
	for _, broken := range []func(*aiLessonState){
		func(l *aiLessonState) { l.ID = "" },
		func(l *aiLessonState) { l.AnswerPolicy = "everything" },
		func(l *aiLessonState) { l.HintLevel = "answer" },
		func(l *aiLessonState) { l.Objectives[0].Status = "solved" },
	} {
		copy := *lesson
		copy.Objectives = append([]aiLessonObjective(nil), lesson.Objectives...)
		broken(&copy)
		body.Lesson = &copy
		if validateTeachInput(&body) == nil {
			t.Fatalf("invalid lesson accepted: %+v", copy)
		}
	}
}

func TestTeachModeBoundsThePausedLessonIndex(t *testing.T) {
	body := aiInvocationInput{CollaborationMode: db.ConversationModeTeach}
	for index := 0; index < maxLessonIndex+5; index++ {
		body.LessonIndex = append(body.LessonIndex, aiLessonSummary{ID: "c" + strings.Repeat("x", index%3+1), Title: strings.Repeat("t", 500)})
	}
	if err := validateTeachInput(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.LessonIndex) != maxLessonIndex || len([]rune(body.LessonIndex[0].Title)) > 121 {
		t.Fatalf("index not bounded: %d, %d", len(body.LessonIndex), len(body.LessonIndex[0].Title))
	}
}

func TestTeachPromptIsTheTutorWithoutTheAgentLoop(t *testing.T) {
	body := aiInvocationInput{CollaborationMode: db.ConversationModeTeach, Prompt: "show me how to solve this"}
	system := teachSystemPrompt(body, &db.AskIdentity{Name: "Misty"}, time.Date(2026, 10, 19, 20, 0, 0, 0, time.UTC))
	for _, want := range []string{"Teach exactly one step", "[ROUTE:continue]", "[FOCUS:", "[LESSON:", "Your name is Misty."} {
		if !strings.Contains(system, want) {
			t.Fatalf("teach prompt is missing %q", want)
		}
	}
	for _, unwanted := range []string{agentExecutionGuidance, "agent_finish"} {
		if strings.Contains(system, unwanted) {
			t.Fatalf("teach prompt carries the agent loop: %q", unwanted)
		}
	}
	body.Lesson = &aiLessonState{ID: "conv-1", Title: "two sum", Status: "active"}
	body.LessonIndex = []aiLessonSummary{{ID: "conv-0", Title: "binary search", Progress: "2 of 4"}}
	context := teachLessonContext(body)
	if !strings.Contains(context, `"title":"two sum"`) || !strings.Contains(context, "binary search") {
		t.Fatalf("lesson context = %q", context)
	}
	body.Lesson.Status = "finished"
	if !strings.Contains(teachLessonContext(body), "no current lesson") {
		t.Fatal("a finished lesson stayed current")
	}
}

func TestTeachTurnsTakeAFocusCloseUp(t *testing.T) {
	image := companionTestCapture(t)
	screens := []aiDisplayCapture{{aiCaptureAttachment: image, Screen: "screen1", Primary: true}, {aiCaptureAttachment: image, Screen: teachFocusCapture}}
	body := aiInvocationInput{Mode: "companion", CollaborationMode: db.ConversationModeTeach, DisplayCaptures: screens}
	if err := validateCompanionInput(&body); err != nil {
		t.Fatal(err)
	}
	body = aiInvocationInput{Mode: "companion", DisplayCaptures: append([]aiDisplayCapture(nil), screens...)}
	if validateCompanionInput(&body) == nil {
		t.Fatal("close-up accepted outside a lesson")
	}
}

func TestLessonTagsAreNeverSpoken(t *testing.T) {
	reply := "[ROUTE:new:two sum]\nfirst, make an empty dictionary.\n[FOCUS:120,180,860,520:screen1]\n" +
		`[LESSON:{"objectives":[{"id":"o1","title":"dict","status":"in_progress"}],"current":"o1"}]`
	payload, _ := json.Marshal(map[string]string{"text": reply})
	text, err := agentSpeechText("completed", []db.AIInvocationEventRecord{{EventType: "assistant.message", Payload: payload}})
	if err != nil || text != "first, make an empty dictionary." {
		t.Fatalf("spoken = %q, err = %v", text, err)
	}
}

func TestCompanionRepliesMayDrawButNeverSpeakTheDrawing(t *testing.T) {
	for _, system := range []string{
		companionSystemPrompt(aiInvocationInput{Mode: "companion"}, nil),
		teachSystemPrompt(aiInvocationInput{CollaborationMode: db.ConversationModeTeach}, nil, time.Now()),
	} {
		if !strings.Contains(system, "misty-sketch") || !strings.Contains(system, "only when the person asks") {
			t.Fatal("a companion prompt lacks the drawing rules")
		}
	}
	fence := "```"
	for reply, want := range map[string]string{
		"here's the idea.\n" + fence + "misty-sketch\n{\"surface\":\"board\"}\n{\"id\":\"a\"}\n" + fence + "\nthat's it.": "here's the idea.\n\nthat's it.",
		"here's the idea.\n" + fence + "misty-sketch\n{\"surface\":\"board\"}\n{\"id\":":                                  "here's the idea.",
	} {
		payload, _ := json.Marshal(map[string]string{"text": reply})
		text, err := agentSpeechText("completed", []db.AIInvocationEventRecord{{EventType: "assistant.message", Payload: payload}})
		if err != nil || text != want {
			t.Fatalf("spoken = %q, err = %v", text, err)
		}
	}
}

func TestLessonTurnsLookThingsUpWithinABudget(t *testing.T) {
	if got := teachTools([]string{"web.search", "web.fetch", "browser.act", "screen.open"}); strings.Join(got, ",") != "web.search,web.fetch" {
		t.Fatalf("teach tools = %v", got)
	}
	planning := aiInvocationInput{CollaborationMode: db.ConversationModeTeach}
	step := aiInvocationInput{CollaborationMode: db.ConversationModeTeach, Lesson: &aiLessonState{ID: "c", Status: "active"}}
	if teachToolBudget(planning) != 4 || teachToolBudget(step) != 1 || teachToolBudget(aiInvocationInput{}) != 0 {
		t.Fatal("wrong lookup budgets")
	}
	step.Lesson.Course = &aiLessonCourse{Title: "react", Lessons: []aiLessonCoursePart{{Title: "components", Status: "current"}, {Title: "hooks", Status: "weird"}}}
	if err := validateTeachInput(&step); err != nil || step.Lesson.Course.Lessons[1].Status != "next" {
		t.Fatalf("course not cleaned: %v", err)
	}
}
