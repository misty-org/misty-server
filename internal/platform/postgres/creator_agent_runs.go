package db

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/google/uuid"
)

type CreatorAgentRunInput struct {
	ParentInvocationID string                         `json:"-"`
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
	AIInvocationID     string                         `json:"-"`
	AIConversationID   string                         `json:"-"`
	AIIdempotencyKey   string                         `json:"-"`
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
// It proves account ownership in the same transaction
// that snapshots the Agent and queues its durable work.
func (db *Database) CreateCreatorAgentRun(ctx context.Context, ownerUserID, spaceID, agentID string, input CreatorAgentRunInput) (*SpaceRun, error) {
	spaceID = "" // Retained argument for older callers; content destinations are independent.
	input, err := normalizeCreatorAgentRunInput(input)
	if err != nil {
		return nil, err
	}
	contextBindings := mustJSON(input.ContextReferences)
	out := &SpaceRun{ID: "run_" + uuid.NewString()}
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := checkCreatorContextNote(ctx, tx, ownerUserID, input.ContextNoteID); err != nil {
			return err
		}
		if err := checkCreatorConversationTarget(ctx, tx, ownerUserID, input.ConversationTarget); err != nil {
			return err
		}
		agent, err := loadCreatorAgentForRun(ctx, tx, ownerUserID, agentID)
		if err != nil {
			return err
		}
		mode, err := resolveCreatorRunMode(input.Mode, agent.defaultMode)
		if err != nil {
			return err
		}
		mode, depth, err := applyParentRunDelegation(ctx, tx, ownerUserID, agentID, input.ParentRunID, agent, mode)
		if err != nil {
			return err
		}
		if input.ParentInvocationID != "" {
			mode, err = applyParentInvocationDelegation(ctx, tx, ownerUserID, input.ParentInvocationID, input.ParentRunID, agent, mode)
			if err != nil {
				return err
			}
			depth = 1
		}
		snapshot := creatorAgentSnapshotJSON(agentID, agent)
		runInput := creatorRunInputJSON(input)
		trigger := creatorRunTriggerKind(input)
		if err := insertCreatorAgentRun(ctx, tx, out, spaceID, agentID, ownerUserID, trigger, runInput, snapshot, contextBindings, mode, depth, input); err != nil {
			return err
		}
		if err := insertCreatorRunContexts(ctx, tx, out.ID, ownerUserID, spaceID, input.ContextReferences); err != nil {
			return err
		}
		if err := insertCreatorAgentRunJob(ctx, tx, out.ID, spaceID, agentID, trigger); err != nil {
			return err
		}
		// Account event notifications are emitted by the run table trigger.
		return nil
	})
	if err != nil && input.AIIdempotencyKey != "" {
		if replay, ok := db.replayCreatorRunByIdempotencyKey(ctx, ownerUserID, input.AIIdempotencyKey); ok {
			return replay, nil
		}
	}
	if err != nil && input.SourceMessageID != "" {
		// A duplicate HTTP delivery may lose the unique-index race above. Resolve
		// it to the already queued run so callers observe idempotent success.
		if replay, ok := db.replayCreatorRunBySourceMessage(ctx, ownerUserID, agentID, input.SourceMessageID); ok {
			return replay, nil
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
