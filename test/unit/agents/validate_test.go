package agent

import (
	"testing"

	. "github.com/kannachi323/misty/server/internal/agents"
)

func TestValidateFilePlanAcceptsSafePlan(t *testing.T) {
	plan := FileOperationPlan{
		Summary: "Organize downloads.",
		Operations: []FileOperation{
			{Type: "mkdir", Path: "Documents"},
			{Type: "move", From: "invoice.pdf", To: "Documents/invoice.pdf"},
			{Type: "rename", From: "notes.txt", To: "meeting-notes.txt"},
		},
	}
	problems := ValidateFilePlan(plan, PlanValidationContext{KnownPaths: []string{"invoice.pdf", "notes.txt"}})
	if len(problems) != 0 {
		t.Fatalf("ValidateFilePlan() problems = %#v, want none", problems)
	}
}

func TestValidateFilePlanRejectsUnsafePlan(t *testing.T) {
	confidence := 0.9
	plan := FileOperationPlan{
		Summary: "Unsafe.",
		Operations: []FileOperation{
			{Type: "delete", Path: "old.txt"},
			{Type: "move", From: "../secret.txt", To: "Documents/secret.txt", Confidence: &confidence},
			{Type: "move", From: "missing.pdf", To: "Documents/invoice.pdf"},
			{Type: "move", From: "invoice.pdf", To: "Documents/invoice.pdf"},
			{Type: "rename", From: "notes.txt", To: "/tmp/notes.txt"},
			{Type: "mkdir", Path: ".hidden"},
		},
	}
	problems := ValidateFilePlan(plan, PlanValidationContext{
		KnownPaths:         []string{"invoice.pdf", "notes.txt", "Documents/invoice.pdf"},
		RequireKnownSource: true,
	})
	if len(problems) < 6 {
		t.Fatalf("ValidateFilePlan() problems = %#v, want multiple unsafe rejections", problems)
	}
}

func TestValidateFilePlanDoesNotRequireKnownSourceByDefault(t *testing.T) {
	plan := FileOperationPlan{
		Summary: "Move screenshots.",
		Operations: []FileOperation{
			{Type: "move", From: "Screenshot 2026-06-28.png", To: "Screenshots/Screenshot 2026-06-28.png"},
		},
	}
	problems := ValidateFilePlan(plan, PlanValidationContext{KnownPaths: []string{"other-file.txt"}})
	if len(problems) != 0 {
		t.Fatalf("ValidateFilePlan() problems = %#v, want none", problems)
	}
}
