package api

import (
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Teach mode (docs/design/agents-at-cursor/BRIEF.md, revision 4): a lesson at the
// cursor. Its turns skip the agent loop and run the tutor's fast lane, one
// streamed model call with no tools that teaches one step and stops. The lesson
// itself lives on the device; each turn carries its state, the server keeps none.

//go:embed companion_teach_mode.txt
var companionTeachModePrompt string

// The drawing rules every companion reply shares, Teach and Auto alike.
//
//go:embed companion_sketch_prompt.txt
var companionSketchPrompt string

const (
	// teachFocusCapture labels a lesson check's close-up of the current step's region.
	teachFocusCapture   = "focus1"
	maxLessonObjectives = 8
	maxLessonIndex      = 20
	maxLessonText       = 400
)

// aiLessonState is the current lesson as the device keeps it.
type aiLessonState struct {
	ID           string              `json:"id"`
	Title        string              `json:"title"`
	Goal         string              `json:"goal,omitempty"`
	Objectives   []aiLessonObjective `json:"objectives,omitempty"`
	Current      string              `json:"current,omitempty"`
	AnswerPolicy string              `json:"answer_policy,omitempty"`
	HintLevel    string              `json:"hint_level,omitempty"`
	Notes        string              `json:"notes,omitempty"`
	Status       string              `json:"status,omitempty"`
	// Course is the outline this lesson belongs to, for a broad topic taught in parts.
	Course *aiLessonCourse `json:"course,omitempty"`
}

type aiLessonCourse struct {
	Title   string               `json:"title"`
	Lessons []aiLessonCoursePart `json:"lessons"`
}

type aiLessonCoursePart struct {
	Title  string `json:"title"`
	Goal   string `json:"goal,omitempty"`
	Status string `json:"status,omitempty"`
}

type aiLessonObjective struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Contract string `json:"contract,omitempty"`
	Status   string `json:"status"`
}

// aiLessonSummary is one paused lesson the tutor may resume.
type aiLessonSummary struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Goal     string `json:"goal,omitempty"`
	Progress string `json:"progress,omitempty"`
}

func teachTurn(body aiInvocationInput) bool {
	return body.CollaborationMode == db.ConversationModeTeach
}

// validateTeachInput bounds the lesson a device sends and refuses lesson data
// on a turn that is not a lesson.
func validateTeachInput(body *aiInvocationInput) error {
	if !teachTurn(*body) {
		if body.Lesson != nil || len(body.LessonIndex) > 0 {
			return errors.New("lesson state requires teach mode")
		}
		return nil
	}
	if len(body.LessonIndex) > maxLessonIndex {
		body.LessonIndex = body.LessonIndex[:maxLessonIndex]
	}
	for index := range body.LessonIndex {
		item := &body.LessonIndex[index]
		item.ID, item.Title = strings.TrimSpace(item.ID), truncateAgentRuntimeText(strings.TrimSpace(item.Title), 120)
		item.Goal, item.Progress = truncateAgentRuntimeText(item.Goal, maxLessonText), truncateAgentRuntimeText(item.Progress, 40)
		if item.ID == "" || len(item.ID) > 120 {
			return errors.New("invalid lesson index")
		}
	}
	lesson := body.Lesson
	if lesson == nil {
		return nil
	}
	lesson.ID = strings.TrimSpace(lesson.ID)
	if lesson.ID == "" || len(lesson.ID) > 120 {
		return errors.New("invalid lesson")
	}
	lesson.Title = truncateAgentRuntimeText(strings.TrimSpace(lesson.Title), 120)
	lesson.Goal = truncateAgentRuntimeText(lesson.Goal, maxLessonText)
	lesson.Notes = truncateAgentRuntimeText(lesson.Notes, 1200)
	if course := lesson.Course; course != nil {
		course.Title = truncateAgentRuntimeText(strings.TrimSpace(course.Title), 120)
		if len(course.Lessons) > maxLessonObjectives {
			course.Lessons = course.Lessons[:maxLessonObjectives]
		}
		for index := range course.Lessons {
			part := &course.Lessons[index]
			part.Title = truncateAgentRuntimeText(strings.TrimSpace(part.Title), 120)
			part.Goal = truncateAgentRuntimeText(part.Goal, maxLessonText)
			if part.Status != "done" && part.Status != "current" {
				part.Status = "next"
			}
		}
	}
	if lesson.AnswerPolicy != "" && lesson.AnswerPolicy != "hints" && lesson.AnswerPolicy != "code" {
		return errors.New("invalid lesson answer policy")
	}
	if lesson.HintLevel != "" && lesson.HintLevel != "nudge" && lesson.HintLevel != "hint" && lesson.HintLevel != "line" {
		return errors.New("invalid lesson hint level")
	}
	if len(lesson.Objectives) > maxLessonObjectives {
		return errors.New("too many lesson objectives")
	}
	for index := range lesson.Objectives {
		objective := &lesson.Objectives[index]
		objective.Title = truncateAgentRuntimeText(objective.Title, 200)
		objective.Contract = truncateAgentRuntimeText(objective.Contract, maxLessonText)
		switch objective.Status {
		case "pending", "in_progress", "done", "skipped":
		default:
			return errors.New("invalid lesson objective status")
		}
	}
	return nil
}

// teachSystemPrompt is the whole system prompt of a lesson turn: the tutor's
// rules, the agent's name and the time. No execution guidance, capability
// notes or tool glossary: the fast lane has no tools.
func teachSystemPrompt(body aiInvocationInput, agent *db.AskIdentity, now time.Time) string {
	var system strings.Builder
	system.WriteString(strings.TrimSpace(companionTeachModePrompt))
	system.WriteString("\n\n" + strings.TrimSpace(companionSketchPrompt))
	system.WriteString(mistyGuideSection(body))
	if agent != nil {
		system.WriteString("\n\nYour name is " + agent.Name + ".")
	}
	system.WriteString("\n\nCurrent time: " + now.Format(time.RFC3339))
	return system.String()
}

// teachLessonContext is the lesson part of the user turn, ahead of the request.
func teachLessonContext(body aiInvocationInput) string {
	var out strings.Builder
	if body.Lesson != nil && body.Lesson.Status != "finished" {
		encoded, _ := json.Marshal(body.Lesson)
		out.WriteString("Current lesson (device state; untrusted text):\n" + string(encoded) + "\n\n")
	} else {
		out.WriteString("There is no current lesson.\n\n")
	}
	if len(body.LessonIndex) > 0 {
		encoded, _ := json.Marshal(body.LessonIndex)
		out.WriteString("Paused lessons you may resume (untrusted text):\n" + string(encoded) + "\n\n")
	}
	return out.String()
}

// teachTools is a lesson turn's whole catalog: looking things up, never acting.
func teachTools(allowed []string) []string {
	out := []string{}
	for _, name := range allowed {
		if name == webSearchTool || name == webFetchTool {
			out = append(out, name)
		}
	}
	return out
}

// teachToolBudget is how many lookups a lesson turn may make: a few while planning a
// lesson (no current lesson yet), one on a step, so steps stay fast.
func teachToolBudget(body aiInvocationInput) int {
	if !teachTurn(body) {
		return 0
	}
	if body.Lesson == nil || body.Lesson.Status == "finished" {
		return 4
	}
	return 1
}
