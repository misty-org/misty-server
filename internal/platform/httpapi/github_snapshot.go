package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func normalizeGitHubSnapshot(repo GitHubRepositoryInfo, branches, commits, issues, pulls []map[string]any) []db.GitHubRepositoryRecord {
	items := []db.GitHubRepositoryRecord{{RepositoryID: repo.ID, RecordType: "repository",
		ExternalID: stringInt(repo.ID), Title: repo.FullName, URL: repo.HTMLURL,
		RefName: repo.DefaultBranch, Fingerprint: githubFingerprint(repo),
		Provenance: mustJSONRaw(map[string]any{"source": "initial_sync", "repository_id": repo.ID})}}
	for _, branch := range branches {
		name := githubString(branch, "name")
		sha := githubNestedString(branch, "commit", "sha")
		if name == "" {
			continue
		}
		items = append(items, db.GitHubRepositoryRecord{RepositoryID: repo.ID, RecordType: "branch",
			ExternalID: name, RefName: name, SHA: sha, Title: name, Fingerprint: githubFingerprint(branch),
			Provenance: mustJSONRaw(map[string]any{"source": "initial_sync", "repository_id": repo.ID, "full_name": repo.FullName})})
	}
	for _, commit := range commits {
		sha := githubString(commit, "sha")
		if sha == "" {
			continue
		}
		message := githubNestedString(commit, "commit", "message")
		title, _, _ := strings.Cut(message, "\n")
		occurred := githubNestedTime(commit, "commit", "author", "date")
		items = append(items, db.GitHubRepositoryRecord{RepositoryID: repo.ID, RecordType: "commit",
			ExternalID: sha, SHA: sha, Title: title, URL: githubString(commit, "html_url"),
			ActorLogin: githubNestedString(commit, "author", "login"), OccurredAt: occurred,
			Fingerprint: githubFingerprint(commit), Provenance: mustJSONRaw(map[string]any{"source": "initial_sync", "repository_id": repo.ID, "full_name": repo.FullName})})
	}
	for _, issue := range issues {
		if _, isPull := issue["pull_request"]; isPull {
			continue
		}
		if item, ok := normalizeGitHubNumbered(repo, "issue", issue); ok {
			items = append(items, item)
		}
	}
	for _, pull := range pulls {
		if item, ok := normalizeGitHubNumbered(repo, "pull_request", pull); ok {
			items = append(items, item)
		}
	}
	return items
}

func normalizeGitHubNumbered(repo GitHubRepositoryInfo, kind string, value map[string]any) (db.GitHubRepositoryRecord, bool) {
	numberValue, ok := value["number"].(float64)
	if !ok || numberValue <= 0 {
		return db.GitHubRepositoryRecord{}, false
	}
	number := int64(numberValue)
	occurred := githubTime(value, "updated_at")
	return db.GitHubRepositoryRecord{RepositoryID: repo.ID, RecordType: kind, ExternalID: stringInt(number),
		Number: &number, State: githubString(value, "state"), Title: githubString(value, "title"),
		URL: githubString(value, "html_url"), ActorLogin: githubNestedString(value, "user", "login"),
		OccurredAt: occurred, Fingerprint: githubFingerprint(value),
		Provenance: mustJSONRaw(map[string]any{"source": "initial_sync", "repository_id": repo.ID, "full_name": repo.FullName})}, true
}

func githubFingerprint(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func mustJSONRaw(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func stringInt(value int64) string          { raw, _ := json.Marshal(value); return string(raw) }
func githubString(value map[string]any, key string) string {
	result, _ := value[key].(string)
	return result
}
func githubNestedString(value map[string]any, keys ...string) string {
	var current any = value
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	result, _ := current.(string)
	return result
}
func githubTime(value map[string]any, key string) *time.Time {
	raw := githubString(value, key)
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil
	}
	return &parsed
}
func githubNestedTime(value map[string]any, keys ...string) *time.Time {
	raw := githubNestedString(value, keys...)
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil
	}
	return &parsed
}
