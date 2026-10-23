package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AgentMemberRequestForUser returns a request to either party.
func (db *Database) AgentMemberRequestForUser(ctx context.Context, userID, requestID string) (*AgentMemberRequest, error) {
	out := &AgentMemberRequest{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return scanAgentMemberRequest(tx.QueryRowContext(ctx, agentMemberRequestSelect+`WHERE q.id=$1 AND (q.requester_user_id=$2 OR q.target_owner_user_id=$2)`, requestID, userID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

// AgentMentionsForUser lists the agent runs the person started by @mentioning
// an agent in Space chat, newest first. Only the person who mentioned sees them;
// everyone else in the chat sees just the agent's reply.
func (db *Database) AgentMentionsForUser(ctx context.Context, userID string, limit int) ([]AgentMemberRequest, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	out := []AgentMemberRequest{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, agentMemberRequestSelect+`WHERE q.requester_user_id=$1 AND q.requester_run_id LIKE $2
			ORDER BY q.created_at DESC LIMIT $3`, userID, MentionRequesterPrefix+"%", limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentMemberRequest
			if err := scanAgentMemberRequest(rows, &item); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}

// AgentRequestStep is one step of an agent's run, as the person who asked sees
// it: what it was, how it ended, and when. Inputs and outputs stay out; they can
// hold the owner's private data.
type AgentRequestStep struct {
	Name        string     `json:"name"`
	State       string     `json:"state"`
	Error       string     `json:"error,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// AgentRequestTranscript returns the steps of the run behind a request to the
// person who made it, and to nobody else (not even the agent's owner).
func (db *Database) AgentRequestTranscript(ctx context.Context, userID, requestID string) ([]AgentRequestStep, error) {
	steps := []AgentRequestStep{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		var runID string
		if err := tx.QueryRowContext(ctx, `SELECT child_run_id FROM agent_member_requests WHERE id=$1 AND requester_user_id=$2`, requestID, userID).Scan(&runID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT node_id,state,COALESCE(error_message,''),started_at,completed_at FROM space_run_steps WHERE run_id=$1 ORDER BY COALESCE(started_at,updated_at),node_id`, runID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var step AgentRequestStep
			if err := rows.Scan(&step.Name, &step.State, &step.Error, &step.StartedAt, &step.CompletedAt); err != nil {
				return err
			}
			steps = append(steps, step)
		}
		return rows.Err()
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return steps, err
}

// AgentMemberRequestForRun finds the request a delegated run is doing.
// ErrSpaceNotFound means the run is not a member request.
func (db *Database) AgentMemberRequestForRun(ctx context.Context, runID string) (*AgentMemberRequest, error) {
	out := &AgentMemberRequest{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return scanAgentMemberRequest(tx.QueryRowContext(ctx, agentMemberRequestSelect+`WHERE q.child_run_id=$1`, runID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

// requireMentionMessageTx proves the requester sent the chat message that
// @mentions the agent, in the Space and chat the request names. The owner must
// be able to post the reply there.
func requireMentionMessageTx(ctx context.Context, tx *sql.Tx, input AgentMemberRequestInput, messageID string) error {
	var mentioned bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_messages m
		WHERE m.id=$1 AND m.space_id=$2 AND m.sender_user_id=$3 AND m.sender_kind='person' AND COALESCE(m.conversation_id,'')=$4
		  AND m.content @> jsonb_build_array(jsonb_build_object('type','mention','agent_id',$5::text)))`,
		messageID, input.SpaceID, input.RequesterUserID, input.SourceConversationID, input.TargetAgentID).Scan(&mentioned); err != nil {
		return err
	}
	if !mentioned {
		return ErrSpaceForbidden
	}
	if input.SourceConversationID == "" {
		return nil
	}
	var ownerInChat bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_conversation_members cm
		JOIN misty_ask_identities a ON a.owner_user_id=cm.user_id
		WHERE cm.conversation_id=$1 AND a.id=$2)`, input.SourceConversationID, input.TargetAgentID).Scan(&ownerInChat); err != nil {
		return err
	}
	if !ownerInChat {
		return ErrAgentNotInSpace
	}
	return nil
}
