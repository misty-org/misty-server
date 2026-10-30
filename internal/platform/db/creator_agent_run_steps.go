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

// creatorAgentForRun is the Agent snapshot a direct run is pinned to.
type creatorAgentForRun struct {
	name, instructions, modelID, effort, defaultMode, versionID string
	systemManaged                                               bool
	version                                                     int64
}

// normalizeCreatorAgentRunInput trims, defaults and validates a direct run request.
func normalizeCreatorAgentRunInput(input CreatorAgentRunInput) (CreatorAgentRunInput, error) {
	input.Instruction = strings.TrimSpace(input.Instruction)
	input.Mode = strings.ToLower(strings.TrimSpace(input.Mode))
	input.ConversationTarget = strings.TrimSpace(input.ConversationTarget)
	input.SourceMessageID = strings.TrimSpace(input.SourceMessageID)
	input.SourceType = strings.TrimSpace(input.SourceType)
	input.InputModality = strings.ToLower(strings.TrimSpace(input.InputModality))
	input.Timezone = strings.TrimSpace(input.Timezone)
	input.ContextNoteID = strings.TrimSpace(input.ContextNoteID)
	input.AIInvocationID = strings.TrimSpace(input.AIInvocationID)
	input.AIConversationID = strings.TrimSpace(input.AIConversationID)
	input.AIIdempotencyKey = strings.TrimSpace(input.AIIdempotencyKey)
	if input.Instruction == "" || len([]rune(input.Instruction)) > 32_000 {
		return input, ErrSpaceInvalid
	}
	if len(input.AIIdempotencyKey) > 200 {
		return input, ErrSpaceInvalid
	}
	if len(input.ContextReferences) > 8 {
		return input, ErrSpaceInvalid
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
		return input, ErrSpaceInvalid
	}
	if _, err := time.LoadLocation(input.Timezone); err != nil {
		return input, ErrSpaceInvalid
	}
	return input, nil
}

// checkCreatorContextNote proves the owner can view the note the run is grounded in.
func checkCreatorContextNote(ctx context.Context, tx *sql.Tx, ownerUserID, noteID string) error {
	if noteID == "" {
		return nil
	}
	access, err := noteAccessForTx(ctx, tx, ownerUserID, noteID)
	if err != nil || !access.CanView {
		return ErrSpaceNotFound
	}
	return nil
}

// checkCreatorConversationTarget proves the owner is a member of the target conversation.
func checkCreatorConversationTarget(ctx context.Context, tx *sql.Tx, ownerUserID, conversationTarget string) error {
	if conversationTarget == "" {
		return nil
	}
	var destination string
	if err := tx.QueryRowContext(ctx, `SELECT space_id FROM space_conversations WHERE id=$1`, conversationTarget).Scan(&destination); err != nil {
		return err
	}
	return requireSpaceConversationMemberTx(ctx, tx, ownerUserID, destination, conversationTarget)
}

// loadCreatorAgentForRun reads the enabled, owner-scoped Agent and its current version.
func loadCreatorAgentForRun(ctx context.Context, tx *sql.Tx, ownerUserID, agentID string) (creatorAgentForRun, error) {
	var agent creatorAgentForRun
	if err := tx.QueryRowContext(ctx, `SELECT a.name,a.instructions,a.model_id,a.reasoning_effort,a.default_run_mode,a.system_managed,a.version,v.id
				FROM misty_ask_identities a JOIN misty_ask_identity_versions v ON v.agent_id=a.id AND v.version=a.version
				WHERE a.id=$1 AND a.owner_user_id=$2 AND a.enabled AND a.deleted_at IS NULL`, agentID, ownerUserID).
		Scan(&agent.name, &agent.instructions, &agent.modelID, &agent.effort, &agent.defaultMode, &agent.systemManaged, &agent.version, &agent.versionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return agent, ErrPersonalAgentNotFound
		}
		return agent, err
	}
	return agent, nil
}

// resolveCreatorRunMode picks the requested run mode, falling back to the Agent default.
func resolveCreatorRunMode(requested, defaultMode string) (string, error) {
	mode := requested
	if mode == "" {
		mode = defaultMode
	}
	if !validAgentRunMode(mode) {
		return "", ErrSpaceInvalid
	}
	return mode, nil
}

// applyParentRunDelegation checks a delegating parent run and returns the child's
// effective mode and delegation depth. A direct run without a parent has depth 0.
func applyParentRunDelegation(ctx context.Context, tx *sql.Tx, ownerUserID, agentID, parentRunID string, agent creatorAgentForRun, mode string) (string, int, error) {
	if parentRunID == "" {
		return mode, 0, nil
	}
	var parentOwner, parentAgent, parentMode string
	var parentDepth int
	if err := tx.QueryRowContext(ctx, `SELECT owner_user_id,agent_id,initial_run_mode,delegation_depth FROM space_runs WHERE id=$1 AND state IN ('queued','running','awaiting_approval','awaiting_device') FOR UPDATE`, parentRunID).
		Scan(&parentOwner, &parentAgent, &parentMode, &parentDepth); err != nil {
		return "", 0, ErrSpaceForbidden
	}
	if parentOwner != ownerUserID || (parentAgent == agentID && !agent.systemManaged) || parentDepth >= 2 {
		return "", 0, ErrSpaceForbidden
	}
	if runModeRank(mode) > runModeRank(agent.defaultMode) {
		mode = agent.defaultMode
	}
	if runModeRank(mode) > runModeRank(parentMode) {
		mode = parentMode
	}
	var childCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM space_runs WHERE parent_run_id=$1`, parentRunID).Scan(&childCount); err != nil {
		return "", 0, err
	}
	if childCount >= 3 {
		return "", 0, ErrSpaceConflict
	}
	return mode, parentDepth + 1, nil
}

// applyParentInvocationDelegation checks a run delegated from a running AI invocation
// and returns the effective mode. The caller sets the delegation depth to 1.
func applyParentInvocationDelegation(ctx context.Context, tx *sql.Tx, ownerUserID, parentInvocationID, parentRunID string, agent creatorAgentForRun, mode string) (string, error) {
	if parentRunID != "" || !agent.systemManaged {
		return "", ErrSpaceForbidden
	}
	var owner string
	if err := tx.QueryRowContext(ctx, `SELECT user_id FROM ai_invocations WHERE id=$1 AND state='running' FOR UPDATE`, parentInvocationID).Scan(&owner); err != nil {
		return "", ErrSpaceForbidden
	}
	if owner != ownerUserID {
		return "", ErrSpaceForbidden
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM space_runs WHERE input->>'parent_invocation_id'=$1`, parentInvocationID).Scan(&count); err != nil {
		return "", err
	}
	if count >= 3 {
		return "", ErrSpaceConflict
	}
	if runModeRank(mode) > runModeRank(agent.defaultMode) {
		mode = agent.defaultMode
	}
	return mode, nil
}

// creatorAgentSnapshotJSON captures the Agent version a run is pinned to.
func creatorAgentSnapshotJSON(agentID string, agent creatorAgentForRun) json.RawMessage {
	return mustJSON(map[string]any{"id": agentID, "version": agent.version, "version_id": agent.versionID, "name": agent.name, "instructions": agent.instructions, "model_id": agent.modelID, "reasoning_effort": agent.effort, "default_run_mode": agent.defaultMode, "system_managed": agent.systemManaged})
}

// creatorRunInputJSON serializes the run's instruction and provenance fields.
func creatorRunInputJSON(input CreatorAgentRunInput) json.RawMessage {
	return mustJSON(map[string]any{
		"instruction": input.Instruction, "conversation_target": input.ConversationTarget,
		"input_modality": input.InputModality, "timezone": input.Timezone,
		"ai_invocation_id": input.AIInvocationID, "ai_conversation_id": input.AIConversationID,
		"ai_idempotency_key": input.AIIdempotencyKey, "parent_invocation_id": input.ParentInvocationID,
	})
}

// creatorRunTriggerKind is "delegated" for runs started by another run or invocation.
func creatorRunTriggerKind(input CreatorAgentRunInput) string {
	if input.ParentRunID != "" || input.ParentInvocationID != "" {
		return "delegated"
	}
	return "direct_instruction"
}

// insertCreatorAgentRun writes the queued space_runs row for a direct run.
func insertCreatorAgentRun(ctx context.Context, tx *sql.Tx, out *SpaceRun, spaceID, agentID, ownerUserID, trigger string, runInput, snapshot, contextBindings json.RawMessage, mode string, depth int, input CreatorAgentRunInput) error {
	return scanSpaceRun(tx.QueryRowContext(ctx, `INSERT INTO space_runs(
				id,space_id,resource_kind,resource_id,initiated_by_user_id,model_user_id,trigger_kind,state,input,result,
				requesting_member_id,source_conversation_id,source_type,agent_id,outputs,artifacts,agent_version_id,source_message_id,
				attempt,conversation_scope_kind,scope_conversation_id,owner_user_id,initial_run_mode,effective_run_mode,
				agent_version_snapshot,parent_run_id,delegation_depth,context_bindings)
				VALUES($1,NULLIF($2,''),'agent',$3,$4,$4,$5,'queued',$6,'{}'::jsonb,$4,NULLIF($7,''),$8,$3,'{}'::jsonb,'[]'::jsonb,NULL,NULLIF($9,''),
				1,'everyone',NULL,$4,$10,$10,$11,NULLIF($12,''),$13,$14) RETURNING `+spaceRunColumns,
		out.ID, spaceID, agentID, ownerUserID, trigger, runInput, input.ConversationTarget, input.SourceType, input.SourceMessageID, mode, snapshot, input.ParentRunID, depth, contextBindings), out)
}

// insertCreatorRunContexts validates each device context reference and stores it for the run.
func insertCreatorRunContexts(ctx context.Context, tx *sql.Tx, runID, ownerUserID, spaceID string, refs []CreatorAgentContextReference) error {
	for _, ref := range refs {
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
					VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10,NOW()+INTERVAL '24 hours')`, `context_`+uuid.NewString(), runID, ownerUserID, spaceID, ref.DeviceID, ref.Kind, ref.OpaqueRef, ref.DisplayName, capabilities, ref.Metadata); err != nil {
			return err
		}
	}
	return nil
}

// insertCreatorAgentRunJob queues the durable work item that executes the run.
func insertCreatorAgentRunJob(ctx context.Context, tx *sql.Tx, runID, spaceID, agentID, trigger string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO agent_run_jobs(run_id,space_id,agent_id,trigger_kind) VALUES($1,NULLIF($2,''),$3,$4)`, runID, spaceID, agentID, trigger)
	return err
}

// replayCreatorRunByIdempotencyKey returns the run already created for this AI idempotency key.
func (db *Database) replayCreatorRunByIdempotencyKey(ctx context.Context, ownerUserID, idempotencyKey string) (*SpaceRun, bool) {
	var existing SpaceRun
	lookupErr := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return scanSpaceRun(tx.QueryRowContext(ctx, `SELECT `+spaceRunColumns+` FROM space_runs
				WHERE owner_user_id=$1 AND input->>'ai_idempotency_key'=$2 AND trigger_kind='direct_instruction'
				ORDER BY created_at LIMIT 1`, ownerUserID, idempotencyKey), &existing)
	})
	if lookupErr != nil {
		return nil, false
	}
	existing.IdempotentReplay = true
	return &existing, true
}

// replayCreatorRunBySourceMessage returns the run already queued for this source message.
func (db *Database) replayCreatorRunBySourceMessage(ctx context.Context, ownerUserID, agentID, sourceMessageID string) (*SpaceRun, bool) {
	var existing SpaceRun
	lookupErr := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return scanSpaceRun(tx.QueryRowContext(ctx, `SELECT `+spaceRunColumns+` FROM space_runs
				WHERE source_message_id=$1 AND agent_id=$2 AND owner_user_id=$3 AND trigger_kind='direct_instruction'
				ORDER BY created_at LIMIT 1`, sourceMessageID, agentID, ownerUserID), &existing)
	})
	if lookupErr != nil {
		return nil, false
	}
	existing.IdempotentReplay = true
	return &existing, true
}
