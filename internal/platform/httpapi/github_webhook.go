package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestingValidGitHubWebhookSignature(secret string, payload []byte, signature string) bool {
	prefix, encoded, found := strings.Cut(strings.TrimSpace(signature), "=")
	if !found || prefix != "sha256" {
		return false
	}
	provided, err := hex.DecodeString(encoded)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return hmac.Equal(provided, mac.Sum(nil))
}

func (s *SpacesService) GitHubWebhook() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		secret := strings.TrimSpace(envconfig.Getenv("GITHUB_WEBHOOK_SECRET"))
		delivery := strings.TrimSpace(r.Header.Get("X-GitHub-Delivery"))
		eventName := strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
		payload, err := io.ReadAll(io.LimitReader(r.Body, 4<<20+1))
		if err != nil || len(payload) > 4<<20 || secret == "" || delivery == "" || eventName == "" || !TestingValidGitHubWebhookSignature(secret, payload, r.Header.Get("X-Hub-Signature-256")) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_github_webhook"})
			return
		}
		var event map[string]any
		if json.Unmarshal(payload, &event) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_github_payload"})
			return
		}
		action := githubString(event, "action")
		installationID := githubNestedInt64(event, "installation", "id")
		repositoryID := githubNestedInt64(event, "repository", "id")
		fresh, err := s.database.BeginGitHubWebhookDelivery(r.Context(), delivery, eventName, action, installationID, repositoryID, githubPayloadHash(payload))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if !fresh {
			writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "duplicate": true})
			return
		}
		if eventName == "installation" {
			status, errorCode := "", ""
			switch action {
			case "suspended":
				status, errorCode = "suspended", "installation_suspended"
			case "unsuspended", "new_permissions_accepted":
				status = "active"
			case "deleted":
				status, errorCode = "disabled", "installation_deleted"
			}
			state := "ignored"
			if status != "" {
				if err := s.database.UpdateGitHubInstallationLifecycle(r.Context(), installationID, status, errorCode); err != nil {
					_ = s.database.FinishGitHubWebhookDelivery(r.Context(), delivery, "failed", "installation_update_failed")
					writeSpaceError(w, err)
					return
				}
				state = "processed"
			}
			_ = s.database.FinishGitHubWebhookDelivery(r.Context(), delivery, state, "")
			writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "installation_status": status})
			return
		}
		if eventName == "installation_repositories" {
			removed := githubRepositoryIDs(event["repositories_removed"])
			if err := s.database.DisableGitHubInstallationRepositories(r.Context(), installationID, removed); err != nil {
				_ = s.database.FinishGitHubWebhookDelivery(r.Context(), delivery, "failed", "repository_update_failed")
				writeSpaceError(w, err)
				return
			}
			_ = s.database.FinishGitHubWebhookDelivery(r.Context(), delivery, "processed", "")
			writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "repositories_removed": len(removed)})
			return
		}
		workspaces, err := s.database.GitHubCodeWorkspacesForRepository(r.Context(), installationID, repositoryID)
		if err != nil {
			_ = s.database.FinishGitHubWebhookDelivery(r.Context(), delivery, "failed", "workspace_lookup_failed")
			writeSpaceError(w, err)
			return
		}
		records := normalizeGitHubWebhookRecords(eventName, action, event, repositoryID, delivery)
		processed := 0
		for _, workspace := range workspaces {
			for _, record := range records {
				record.WorkspaceID = workspace.ID
				if err := s.database.UpsertGitHubRepositoryRecord(r.Context(), record); err != nil {
					_ = s.database.FinishGitHubWebhookDelivery(r.Context(), delivery, "failed", "record_upsert_failed")
					writeSpaceError(w, err)
					return
				}
				processed++
			}
			_ = s.database.SetGitHubCodeWorkspaceSync(r.Context(), workspace.ID, delivery, "active", "")
		}
		state := "processed"
		if len(workspaces) == 0 || len(records) == 0 {
			state = "ignored"
		}
		_ = s.database.FinishGitHubWebhookDelivery(r.Context(), delivery, state, "")
		writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "records_processed": processed})
	}
}

func normalizeGitHubWebhookRecords(eventName, action string, event map[string]any, repositoryID int64, delivery string) []db.GitHubRepositoryRecord {
	provenance := mustJSONRaw(map[string]any{"source": "webhook", "delivery_id": delivery, "event": eventName, "action": action, "repository_id": repositoryID})
	makeRecord := func(kind, id string, value any) db.GitHubRepositoryRecord {
		return db.GitHubRepositoryRecord{RepositoryID: repositoryID, RecordType: kind, ExternalID: id, Fingerprint: githubFingerprint(value), Provenance: provenance}
	}
	switch eventName {
	case "push":
		ref := strings.TrimPrefix(githubString(event, "ref"), "refs/heads/")
		records := []db.GitHubRepositoryRecord{}
		if ref != "" {
			branch := makeRecord("branch", ref, event)
			branch.RefName = ref
			branch.SHA = githubString(event, "after")
			branch.Title = ref
			records = append(records, branch)
		}
		if commits, ok := event["commits"].([]any); ok {
			for _, raw := range commits {
				value, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				sha := githubString(value, "id")
				if sha == "" {
					continue
				}
				record := makeRecord("commit", sha, value)
				record.SHA = sha
				record.RefName = ref
				record.Title = githubString(value, "message")
				record.URL = githubString(value, "url")
				record.ActorLogin = githubNestedString(value, "author", "username")
				record.OccurredAt = githubTime(value, "timestamp")
				records = append(records, record)
			}
		}
		return records
	case "issues", "pull_request":
		key, kind := "issue", "issue"
		if eventName == "pull_request" {
			key, kind = "pull_request", "pull_request"
		}
		value, _ := event[key].(map[string]any)
		number := githubNestedInt64(event, key, "number")
		if number == 0 {
			return nil
		}
		record := makeRecord(kind, strconv.FormatInt(number, 10), value)
		record.Number = &number
		record.State = githubString(value, "state")
		record.Title = githubString(value, "title")
		record.URL = githubString(value, "html_url")
		record.ActorLogin = githubNestedString(value, "user", "login")
		record.OccurredAt = githubTime(value, "updated_at")
		return []db.GitHubRepositoryRecord{record}
	case "create", "delete":
		if githubString(event, "ref_type") != "branch" {
			return nil
		}
		ref := githubString(event, "ref")
		if ref == "" {
			return nil
		}
		record := makeRecord("branch", ref, event)
		record.RefName = ref
		record.Title = ref
		if eventName == "delete" {
			now := time.Now().UTC()
			record.DeletedAt = &now
		}
		return []db.GitHubRepositoryRecord{record}
	case "repository":
		value, _ := event["repository"].(map[string]any)
		record := makeRecord("repository", strconv.FormatInt(repositoryID, 10), value)
		record.Title = githubString(value, "full_name")
		record.URL = githubString(value, "html_url")
		record.RefName = githubString(value, "default_branch")
		return []db.GitHubRepositoryRecord{record}
	}
	return nil
}

func githubNestedInt64(value map[string]any, keys ...string) int64 {
	var current any = value
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return 0
		}
		current = object[key]
	}
	number, ok := current.(float64)
	if ok {
		return int64(number)
	}
	return 0
}

func githubRepositoryIDs(value any) []int64 {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	ids := []int64{}
	for _, raw := range items {
		object, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id := githubNestedInt64(object, "id"); id > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}
