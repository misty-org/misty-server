package api

import (
	"encoding/json"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func githubValidateMutation(operation string, payload, installationPermissions, repositoryPermissions json.RawMessage) error {
	var input map[string]any
	var app map[string]string
	var repo map[string]bool
	if json.Unmarshal(payload, &input) != nil || json.Unmarshal(installationPermissions, &app) != nil || json.Unmarshal(repositoryPermissions, &repo) != nil {
		return db.ErrSpaceInvalid
	}
	if !(repo["push"] || repo["admin"] || repo["maintain"]) {
		return db.ErrSpaceForbidden
	}
	required := ""
	fields := []string{}
	switch operation {
	case "create_issue":
		required = "issues"
		fields = []string{"title"}
	case "comment_issue":
		required = "issues"
		fields = []string{"number", "body"}
	case "create_branch":
		required = "contents"
		fields = []string{"ref", "sha"}
	case "create_pull_request":
		required = "pull_requests"
		fields = []string{"title", "head", "base"}
	default:
		return db.ErrSpaceInvalid
	}
	if app[required] != "write" {
		return db.ErrSpaceForbidden
	}
	for _, field := range fields {
		if TestingFindWorkflowString(input, field) == "" {
			return db.ErrSpaceInvalid
		}
	}
	return nil
}

func githubMutationTarget(payload json.RawMessage) string {
	var input map[string]any
	_ = json.Unmarshal(payload, &input)
	return githubFirstNonEmpty(TestingFindWorkflowString(input, "number"), TestingFindWorkflowString(input, "ref"), TestingFindWorkflowString(input, "head"))
}
func githubFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
