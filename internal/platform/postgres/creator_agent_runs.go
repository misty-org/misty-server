package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type CreatorAgentRunInput struct {
	Instruction        string                         `json:"instruction"`
	Mode               string                         `json:"mode,omitempty"`
	ConversationTarget string                         `json:"conversation_target,omitempty"`
	ContextReferences  []CreatorAgentContextReference `json:"context_references,omitempty"`
	ParentRunID        string                         `json:"parent_run_id,omitempty"`
	SourceMessageID    string                         `json:"source_message_id,omitempty"`
	SourceType         string                         `json:"source_type,omitempty"`
	InputModality      string                         `json:"input_modality,omitempty"`
	Timezone           string                         `json:"timezone,omitempty"`
	ContextNoteID      string                         `json:"context_note_id,omitempty"`
}

type CreatorAgentContextReference struct {
	DeviceID     string          `json:"device_id"`
	Kind         string          `json:"kind"`
	OpaqueRef    string          `json:"opaque_ref"`
	DisplayName  string          `json:"display_name,omitempty"`
	Capabilities json.RawMessage `json:"capabilities"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
}

func validAgentRunMode(mode string) bool { return mode == "ask" || mode == "auto" || mode == "full" }

// CreateCreatorAgentRun is the canonical entry point for direct instructions.
// It deliberately proves ownership and Space membership in the same transaction
// that snapshots the Agent and queues its durable work.
func (db *Database) CreateCreatorAgentRun(ctx context.Context, ownerUserID, spaceID, agentID string, input CreatorAgentRunInput) (*SpaceRun, error) {
	input.Instruction = strings.TrimSpace(input.Instruction)
	input.Mode = strings.ToLower(strings.TrimSpace(input.Mode))
	input.ConversationTarget = strings.TrimSpace(input.ConversationTarget)
	input.SourceMessageID = strings.TrimSpace(input.SourceMessageID)
	input.SourceType = strings.TrimSpace(input.SourceType)
	input.InputModality = strings.ToLower(strings.TrimSpace(input.InputModality))
	input.Timezone = strings.TrimSpace(input.Timezone)
	input.ContextNoteID = strings.TrimSpace(input.ContextNoteID)
	if input.Instruction == "" || len([]rune(input.Instruction)) > 32_000 {
		return nil, ErrSpaceInvalid
	}
	if len(input.ContextReferences) > 8 {
		return nil, ErrSpaceInvalid
	}
	if input.ContextReferences == nil {
		input.ContextReferences = []CreatorAgentContextReference{}
	}
	if input.SourceType == "" {
		input.SourceType = "direct"
	}
	if input.InputModality == "" {
		input.InputModality = "text"
	}
	if input.Timezone == "" {
		input.Timezone = "UTC"
	}
	if (input.SourceType != "direct" && input.SourceType != "mention") || (input.InputModality != "text" && input.InputModality != "voice") {
		return nil, ErrSpaceInvalid
	}
	if _, err := time.LoadLocation(input.Timezone); err != nil {
		return nil, ErrSpaceInvalid
	}
	contextBindings := mustJSON(input.ContextReferences)
	out := &SpaceRun{ID: "run_" + uuid.NewString()}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, ownerUserID); err != nil {
			return err
		}
		if input.ContextNoteID != "" {
			access, err := noteAccessForTx(ctx, tx, ownerUserID, input.ContextNoteID)
			if err != nil || !access.CanView {
				return ErrSpaceNotFound
			}
			var noteSpaceID string
			if err := tx.QueryRowContext(ctx, `SELECT space_id FROM space_notes WHERE id=$1`, input.ContextNoteID).Scan(&noteSpaceID); err != nil || noteSpaceID != spaceID {
				return ErrSpaceNotFound
			}
		}
		if input.ConversationTarget != "" {
			if err := requireSpaceConversationMemberTx(ctx, tx, ownerUserID, spaceID, input.ConversationTarget); err != nil {
				return err
			}
		}
		var name, instructions, modelID, effort, defaultMode, versionID string
		var version int64
		if err := tx.QueryRowContext(ctx, `SELECT a.name,a.instructions,a.model_id,a.reasoning_effort,a.default_run_mode,a.version,v.id
			FROM personal_agents a JOIN personal_agent_versions v ON v.agent_id=a.id AND v.version=a.version
			WHERE a.id=$1 AND a.owner_user_id=$2 AND a.enabled AND a.deleted_at IS NULL`, agentID, ownerUserID).
			Scan(&name, &instructions, &modelID, &effort, &defaultMode, &version, &versionID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrPersonalAgentNotFound
			}
			return err
		}
		mode := input.Mode
		if mode == "" {
			mode = defaultMode
		}
		if !validAgentRunMode(mode) {
			return ErrSpaceInvalid
		}
		depth := 0
		if input.ParentRunID != "" {
			var parentOwner, parentSpace, parentAgent, parentMode string
			var parentDepth int
			if err := tx.QueryRowContext(ctx, `SELECT owner_user_id,space_id,agent_id,initial_run_mode,delegation_depth FROM space_runs WHERE id=$1 AND state IN ('queued','running','awaiting_approval','awaiting_device')`, input.ParentRunID).
				Scan(&parentOwner, &parentSpace, &parentAgent, &parentMode, &parentDepth); err != nil {
				return ErrSpaceForbidden
			}
			if parentOwner != ownerUserID || parentSpace != spaceID || parentAgent == agentID || parentDepth >= 2 {
				return ErrSpaceForbidden
			}
			if runModeRank(mode) > runModeRank(defaultMode) {
				mode = defaultMode
			}
			if runModeRank(mode) > runModeRank(parentMode) {
				mode = parentMode
			}
			var childCount int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM space_runs WHERE parent_run_id=$1`, input.ParentRunID).Scan(&childCount); err != nil {
				return err
			}
			if childCount >= 3 {
				return ErrSpaceConflict
			}
			depth = parentDepth + 1
		}
		snapshot := mustJSON(map[string]any{"id": agentID, "version": version, "version_id": versionID, "name": name, "instructions": instructions, "model_id": modelID, "reasoning_effort": effort, "default_run_mode": defaultMode})
		runInput := mustJSON(map[string]any{"instruction": input.Instruction, "conversation_target": input.ConversationTarget, "input_modality": input.InputModality, "timezone": input.Timezone})
		trigger := "direct_instruction"
		if input.ParentRunID != "" {
			trigger = "delegated"
		}
		if err := scanSpaceRun(tx.QueryRowContext(ctx, `INSERT INTO space_runs(
			id,space_id,resource_kind,resource_id,initiated_by_user_id,billing_user_id,trigger_kind,state,input,result,
			requesting_member_id,source_conversation_id,source_type,agent_id,capability_id,outputs,artifacts,agent_version_id,source_message_id,
			attempt,conversation_scope_kind,scope_conversation_id,owner_user_id,initial_run_mode,effective_run_mode,
			agent_version_snapshot,parent_run_id,delegation_depth,context_bindings,action_envelope)
			VALUES($1,$2,'agent',$3,$4,$4,$5,'queued',$6,'{}'::jsonb,$4,NULLIF($7,''),$8,$3,'companion','{}'::jsonb,'[]'::jsonb,NULL,NULLIF($9,''),
			1,'everyone',NULL,$4,$10,$10,$11,NULLIF($12,''),$13,$14,'{}'::jsonb) RETURNING `+spaceRunColumns,
			out.ID, spaceID, agentID, ownerUserID, trigger, runInput, input.ConversationTarget, input.SourceType, input.SourceMessageID, mode, snapshot, input.ParentRunID, depth, contextBindings), out); err != nil {
			return err
		}
		for _, ref := range input.ContextReferences {
			ref.Kind = strings.TrimSpace(ref.Kind)
			ref.OpaqueRef = strings.TrimSpace(ref.OpaqueRef)
			ref.DeviceID = strings.TrimSpace(ref.DeviceID)
			ref.DisplayName = strings.TrimSpace(ref.DisplayName)
			capabilities, normalizeErr := normalizeDeviceAgentCapabilities(ref.Capabilities)
			if normalizeErr != nil || (ref.Kind != "browser_tab" && ref.Kind != "project_root") || ref.OpaqueRef == "" || ref.DeviceID == "" {
				return ErrSpaceInvalid
			}
			if len(ref.Metadata) == 0 {
				ref.Metadata = json.RawMessage(`{}`)
			}
			var metadata map[string]any
			if json.Unmarshal(ref.Metadata, &metadata) != nil {
				return ErrSpaceInvalid
			}
			var deviceExists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM trusted_devices WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL)`, ref.DeviceID, ownerUserID).Scan(&deviceExists); err != nil || !deviceExists {
				return ErrDeviceNotFound
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO agent_run_contexts(id,run_id,owner_user_id,space_id,device_id,kind,opaque_ref,display_name,capabilities,metadata,expires_at)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NOW()+INTERVAL '24 hours')`, `context_`+uuid.NewString(), out.ID, ownerUserID, spaceID, ref.DeviceID, ref.Kind, ref.OpaqueRef, ref.DisplayName, capabilities, ref.Metadata); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_run_jobs(run_id,space_id,task_id,agent_id,trigger_kind) VALUES($1,$2,NULL,$3,$4)`, out.ID, spaceID, agentID, trigger); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, ownerUserID, "agent.run.queued", out.ID, map[string]any{"agent_id": agentID, "mode": mode, "trigger_kind": trigger})
		return err
	})
	if err != nil && input.SourceMessageID != "" {
		// A duplicate HTTP delivery may lose the unique-index race above. Resolve
		// it to the already queued run so callers observe idempotent success.
		var existing SpaceRun
		lookupErr := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
			return scanSpaceRun(tx.QueryRowContext(ctx, `SELECT `+spaceRunColumns+` FROM space_runs
				WHERE source_message_id=$1 AND agent_id=$2 AND owner_user_id=$3 AND trigger_kind='direct_instruction'
				ORDER BY created_at LIMIT 1`, input.SourceMessageID, agentID, ownerUserID), &existing)
		})
		if lookupErr == nil {
			return &existing, nil
		}
	}
	return out, err
}

func runModeRank(mode string) int {
	switch mode {
	case "ask":
		return 0
	case "auto":
		return 1
	case "full":
		return 2
	default:
		return -1
	}
}
