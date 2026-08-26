package unit_test

import (
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestValidateAIInvocationAcceptsAttachmentOnlyPrompt(t *testing.T) {
	if err := api.TestingValidateAIInvocationAttachmentShape("", []string{"aiatt_one"}); err != nil {
		t.Fatalf("attachment-only Ask should be valid: %v", err)
	}
}

func TestValidateAIInvocationRejectsDuplicateAttachments(t *testing.T) {
	err := api.TestingValidateAIInvocationAttachmentShape(
		"compare",
		[]string{"aiatt_one", "aiatt_one"},
	)
	if err == nil {
		t.Fatal("duplicate attachment IDs must be rejected")
	}
}
