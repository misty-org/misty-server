package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
)

type aiArtifactSpec struct {
	Title          string
	Risk           string
	ApprovalPolicy string
	Prompt         string
	Shape          string
}

var aiArtifactSpecs = map[string]aiArtifactSpec{
	"calendar_event": {
		Title: "Review calendar event", Risk: "consequential", ApprovalPolicy: "confirm",
		Prompt: "Propose one calendar event using explicit dates, timezone, title, description, and location. Never infer invitees.",
		Shape:  `{"title":"...","description":"...","starts_at":"RFC3339","ends_at":"RFC3339","timezone":"IANA timezone","all_day":false,"location":"...","invitees":[]}`,
	},
	"roadmap_patch": {
		Title: "Review roadmap changes", Risk: "draft", ApprovalPolicy: "visible_apply",
		Prompt: "Propose a revision-anchored roadmap graph patch using only add, update, or connect operations and exact visible node identifiers.",
		Shape:  `{"base_revision":"...","changes":[{"op":"add|update|connect","kind":"milestone|goal|node|edge","id":"opaque id when updating","fields":{}}]}`,
	},
	"drawing_patch": {
		Title: "Review drawing changes", Risk: "draft", ApprovalPolicy: "visible_apply",
		Prompt: "Propose constrained scene operations for the current drawing selection. Preserve unrelated elements. For layout updates, copy the exact selection content hash into base_hash and change only x/y positions of selected elements.",
		Shape:  `{"base_revision":"...","base_hash":"exact selection content hash","changes":[{"op":"add|update|delete","element_id":"opaque id","element":{"x":0,"y":0}}]}`,
	},
	"file_plan": {
		Title: "Review file plan", Risk: "dangerous", ApprovalPolicy: "always_confirm",
		Prompt: "Propose a file operation plan using opaque device scope identifiers, display names, and explicit conflict policies. Never emit raw local paths.",
		Shape:  `{"steps":[{"action":"copy|move|rename|trash|mkdir","source_scope_id":"opaque id","destination_scope_id":"opaque id","display_name":"...","conflict_policy":"ask|skip|rename"}]}`,
	},
	"mail_draft": {
		Title: "Review email draft", Risk: "draft", ApprovalPolicy: "visible_apply",
		Prompt: "Draft an email for review. This creates draft content only and must never send it.",
		Shape:  `{"thread_scope_id":"opaque id","to":["address"],"cc":[],"bcc":[],"subject":"...","text":"..."}`,
	},
	"message_draft": {
		Title: "Review message draft", Risk: "draft", ApprovalPolicy: "visible_apply",
		Prompt: "Draft a Space message for review. Do not post it.",
		Shape:  `{"conversation_id":"opaque id","text":"...","reply_to_message_id":"optional opaque id"}`,
	},
	"code_patch": {
		Title: "Review code patch", Risk: "draft", ApprovalPolicy: "visible_apply",
		Prompt: "Propose a reviewable code patch anchored to supplied content hashes. Use filenames and opaque buffer identifiers, never raw local paths.",
		Shape:  `{"files":[{"buffer_id":"opaque id","filename":"...","base_hash":"...","patch":"unified diff"}],"tests":[]}`,
	},
	"terminal_command": {
		Title: "Review terminal command", Risk: "dangerous", ApprovalPolicy: "always_confirm",
		Prompt: "Propose commands but do not execute them. Explain exact effects, targets, and rollback; use an opaque terminal scope.",
		Shape:  `{"terminal_scope_id":"opaque id","commands":[{"command":"...","effect":"...","destructive":false}],"rollback":"..."}`,
	},
	"browser_action": {
		Title: "Review browser action", Risk: "dangerous", ApprovalPolicy: "always_confirm",
		Prompt: "Propose browser interaction steps but do not navigate, click, type, upload, or submit. Name exact visible effects and require a fresh tab grant.",
		Shape:  `{"tab_scope_id":"opaque id","steps":[{"action":"navigate|click|type|upload|submit","target":"visible description","value":"reviewable value","effect":"..."}]}`,
	},
	"transfer_plan": {
		Title: "Review transfer recovery", Risk: "consequential", ApprovalPolicy: "confirm",
		Prompt: "Propose retry or recovery operations for exact transfer identifiers without starting them.",
		Shape:  `{"transfers":[{"transfer_id":"opaque id","action":"retry|resume|cancel|change_conflict_policy","effect":"..."}]}`,
	},
	"extension_action": {
		Title: "Review extension action", Risk: "dangerous", ApprovalPolicy: "always_confirm",
		Prompt: "Propose extension installation, configuration, or execution steps with exact extension identifiers, permissions, and effects. Do not run them.",
		Shape:  `{"extension_id":"...","action":"install|configure|run","permissions":[],"configuration":{},"effect":"..."}`,
	},
	"image_edit": {
		Title: "Review image edit", Risk: "draft", ApprovalPolicy: "visible_apply",
		Prompt: "Propose a non-destructive image adjustment that creates a new rendition version and preserves the original. Use only the supported filter and bounded adjustment fields; do not crop, rotate, add markup, or claim generative pixel changes without image-model output.",
		Shape:  `{"asset_id":"opaque id","instruction":"...","output":"new_version","preserve_original":true,"edit_definition":{"auto_enhance":false,"filter":"|vivid|dramatic|warm|cool|mono|noir","brightness":1,"contrast":1,"saturation":1,"grayscale":0,"exposure":0,"brilliance":0,"highlights":0,"shadows":0,"black_point":0,"vibrance":0,"warmth":0,"tint":0,"sharpness":0,"definition":0,"noise_reduction":0,"vignette":0,"straighten":0}}`,
	},
}

func parseAIStructuredArtifact(value string) (string, map[string]any, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "```json")
	value = strings.TrimPrefix(value, "```")
	value = strings.TrimSuffix(value, "```")
	if len(value) > 256<<10 {
		return "", nil, errors.New("artifact proposal is too large")
	}
	var envelope struct {
		Summary    string         `json:"summary"`
		Operations map[string]any `json:"operations"`
	}
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil || envelope.Operations == nil {
		if err == nil {
			err = errors.New("artifact operations are required")
		}
		return "", nil, err
	}
	envelope.Summary = strings.Join(strings.Fields(envelope.Summary), " ")
	if len([]rune(envelope.Summary)) > 500 {
		envelope.Summary = string([]rune(envelope.Summary)[:500]) + "…"
	}
	return envelope.Summary, envelope.Operations, nil
}

func (hub *aiInvocationHub) addStructuredArtifact(userID, invocationID, kind, summary string, operations map[string]any, resolved []aiResolvedContext, body aiInvocationInput, spec aiArtifactSpec) *aiArtifact {
	sources := make([]aiCitation, 0, len(resolved)+1)
	for _, item := range resolved {
		sources = append(sources, item.Citation)
	}
	if selection := aiSelectionCitation(body); selection != nil {
		sources = append(sources, *selection)
	}
	target := map[string]any{}
	var baseRevision any
	if body.Selection != nil {
		for key, value := range body.Selection.Object {
			target[key] = value
		}
		baseRevision = body.Selection.Object["revision"]
	} else if len(body.Context) > 0 {
		reference := body.Context[0]
		target = map[string]any{"kind": reference.Kind, "id": reference.ID}
		if reference.SpaceID != "" {
			target["spaceId"] = reference.SpaceID
		}
		if reference.Href != "" {
			target["href"] = reference.Href
		}
		baseRevision = reference.Revision
	}
	if summary == "" {
		summary = "Review the exact proposed operations before applying them."
	}
	artifact := &aiArtifact{
		ID: "artifact_" + uuid.NewString(), SchemaVersion: 1, Kind: kind, Title: spec.Title, Summary: summary,
		Sources: sources, Target: target, BaseRevision: baseRevision, Operations: operations,
		Risk: spec.Risk, ApprovalPolicy: spec.ApprovalPolicy, IdempotencyKey: "artifact:" + invocationID + ":" + kind,
		ExpiresAt: time.Now().UTC().Add(aiInvocationTTL).Format(time.RFC3339Nano), State: "proposed", InvocationID: invocationID, OwnerUserID: userID,
	}
	hub.mu.Lock()
	hub.artifacts[artifact.ID] = artifact
	database := hub.database
	hub.mu.Unlock()
	if database != nil {
		payload, err := json.Marshal(artifact)
		if err == nil {
			err = database.UpsertAIArtifact(context.Background(), userID, invocationID, payload)
		}
		if err != nil {
			log.Printf("persist AI artifact %s: %v", artifact.ID, err)
		}
	}
	copy := *artifact
	return &copy
}

type aiTaskDraft struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Notes    string `json:"notes"`
	Priority string `json:"priority"`
}

func parseAITaskDrafts(value string) ([]aiTaskDraft, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "```json")
	value = strings.TrimPrefix(value, "```")
	value = strings.TrimSuffix(value, "```")
	var envelope struct {
		Tasks []aiTaskDraft `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(value)), &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Tasks) > 20 {
		return nil, errors.New("too many task drafts")
	}
	result := make([]aiTaskDraft, 0, len(envelope.Tasks))
	seen := map[string]bool{}
	for _, task := range envelope.Tasks {
		task.Title = strings.Join(strings.Fields(task.Title), " ")
		task.Notes = strings.TrimSpace(task.Notes)
		task.Priority = strings.ToLower(strings.TrimSpace(task.Priority))
		if task.Priority == "" {
			task.Priority = "medium"
		}
		key := strings.ToLower(task.Title)
		if task.Title == "" || len([]rune(task.Title)) > 240 || len([]rune(task.Notes)) > 20_000 || seen[key] {
			continue
		}
		if task.Priority != "high" && task.Priority != "medium" && task.Priority != "low" {
			task.Priority = "medium"
		}
		seen[key] = true
		task.ID = "task_" + uuid.NewString()
		result = append(result, task)
	}
	return result, nil
}

func (hub *aiInvocationHub) addTaskSetArtifact(userID, invocationID string, tasks []aiTaskDraft, resolved []aiResolvedContext, body aiInvocationInput) *aiArtifact {
	spaceID := ""
	if body.Selection != nil {
		spaceID, _ = body.Selection.Object["spaceId"].(string)
	}
	if spaceID == "" {
		for _, reference := range body.Context {
			if reference.SpaceID != "" {
				spaceID = reference.SpaceID
				break
			}
		}
	}
	if spaceID == "" {
		return nil
	}
	sources := make([]aiCitation, 0, len(resolved))
	for _, item := range resolved {
		sources = append(sources, item.Citation)
	}
	suffix := "s"
	if len(tasks) == 1 {
		suffix = ""
	}
	artifact := &aiArtifact{
		ID: "artifact_" + uuid.NewString(), SchemaVersion: 1, Kind: "task_set",
		Title:   fmt.Sprintf("Create %d task%s", len(tasks), suffix),
		Summary: "Create these reviewed tasks in the current Space. No assignees or due dates will be inferred.", Sources: sources,
		Target: map[string]any{"kind": "space", "id": spaceID, "spaceId": spaceID}, Operations: map[string]any{"tasks": tasks},
		Risk: "consequential", ApprovalPolicy: "confirm", IdempotencyKey: "artifact:" + invocationID,
		ExpiresAt: time.Now().UTC().Add(aiInvocationTTL).Format(time.RFC3339Nano), State: "proposed", InvocationID: invocationID, OwnerUserID: userID,
	}
	hub.mu.Lock()
	hub.artifacts[artifact.ID] = artifact
	database := hub.database
	hub.mu.Unlock()
	if database != nil {
		payload, err := json.Marshal(artifact)
		if err == nil {
			err = database.UpsertAIArtifact(context.Background(), userID, invocationID, payload)
		}
		if err != nil {
			log.Printf("persist AI artifact %s: %v", artifact.ID, err)
		}
	}
	copy := *artifact
	return &copy
}
