package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// memberRequestParentTx proves the requester owns the live run asking for
// help and bounds how deep delegated work can go.
func memberRequestParentTx(ctx context.Context, tx *sql.Tx, userID, agentID, runID string) (parentRunID, parentInvocationID string, depth int, err error) {
	if strings.HasPrefix(runID, MentionRequesterPrefix) {
		// A person asked in Space chat; requireMentionMessageTx checks the message.
		if agentID != "" {
			return "", "", 0, ErrSpaceForbidden
		}
		return "", "", 1, nil
	}
	if strings.HasPrefix(runID, ReplyRequesterPrefix) {
		// An agent's chat reply, answering a request this person made, mentioned
		// another agent. It continues that request's work one level deeper.
		var parentAgentID string
		var parentDepth int
		err = tx.QueryRowContext(ctx, `SELECT q.child_run_id,q.target_agent_id,r.delegation_depth FROM space_messages m
			JOIN agent_member_requests q ON q.id=m.origin->>'agent_request_id'
			JOIN space_runs r ON r.id=q.child_run_id
			WHERE m.id=$1 AND m.sender_kind='agent' AND m.sender_agent_id=q.target_agent_id AND q.requester_user_id=$2`,
			strings.TrimPrefix(runID, ReplyRequesterPrefix), userID).Scan(&parentRunID, &parentAgentID, &parentDepth)
		if err != nil || agentID == "" || agentID != parentAgentID {
			return "", "", 0, ErrSpaceForbidden
		}
		if parentDepth+1 > memberRequestMaxDepth {
			return "", "", 0, ErrAgentRequestLimit
		}
		return parentRunID, "", parentDepth + 1, nil
	}
	var chained bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_member_requests WHERE child_run_id=$1)`, runID).Scan(&chained); err != nil {
		return "", "", 0, err
	}
	if chained {
		return "", "", 0, ErrAgentRequestChained
	}
	if invocationRunIdentity(runID) {
		var owner, state string
		if err = tx.QueryRowContext(ctx, `SELECT user_id,state FROM ai_invocations WHERE id=$1 FOR UPDATE`, runID).Scan(&owner, &state); err != nil || owner != userID || state != "running" {
			return "", "", 0, ErrSpaceForbidden
		}
		return "", runID, 1, nil
	}
	var owner, state string
	var parentDepth int
	if err = tx.QueryRowContext(ctx, `SELECT owner_user_id,state,delegation_depth FROM space_runs WHERE id=$1 FOR UPDATE`, runID).Scan(&owner, &state, &parentDepth); err != nil || owner != userID {
		return "", "", 0, ErrSpaceForbidden
	}
	if state != "running" && state != "awaiting_device" {
		return "", "", 0, ErrSpaceForbidden
	}
	if parentDepth+1 > memberRequestMaxDepth {
		return "", "", 0, ErrAgentRequestLimit
	}
	return runID, "", parentDepth + 1, nil
}

type memberRequestTarget struct {
	ownerUserID, name, instructions, modelID, effort, defaultMode, versionID string
	version                                                                  int64
}

// memberRequestTargetTx finds the agent asked. A private agent is refused to
// everyone but its owner, the same way as an agent not in the Space.
func memberRequestTargetTx(ctx context.Context, tx *sql.Tx, input AgentMemberRequestInput) (memberRequestTarget, error) {
	var target memberRequestTarget
	err := tx.QueryRowContext(ctx, `SELECT m.owner_user_id,a.name,a.instructions,a.model_id,a.reasoning_effort,a.default_run_mode,a.version,v.id`+
		spaceAgentJoins+` JOIN misty_ask_identity_versions v ON v.agent_id=a.id AND v.version=a.version
		WHERE m.space_id=$1 AND m.agent_id=$2 AND (m.visibility='public' OR m.owner_user_id=$3) FOR SHARE OF m`, input.SpaceID, input.TargetAgentID, input.RequesterUserID).
		Scan(&target.ownerUserID, &target.name, &target.instructions, &target.modelID, &target.effort, &target.defaultMode, &target.version, &target.versionID)
	if errors.Is(err, sql.ErrNoRows) {
		return target, ErrAgentNotInSpace
	}
	if err != nil {
		return target, err
	}
	if input.RequesterAgentID != "" && input.RequesterAgentID == input.TargetAgentID {
		return target, ErrAgentNotInSpace
	}
	if !validAgentRunMode(target.defaultMode) {
		target.defaultMode = "auto"
	}
	return target, nil
}

func memberRequestLimitsTx(ctx context.Context, tx *sql.Tx, input AgentMemberRequestInput) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "agent-member-request:"+input.TargetAgentID); err != nil {
		return err
	}
	var open, perRun, lastHour int
	if err := tx.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM agent_member_requests q JOIN space_runs r ON r.id=q.child_run_id WHERE q.target_agent_id=$1 AND r.state IN (`+memberRequestActiveRunList+`)),
		(SELECT COUNT(*) FROM agent_member_requests WHERE requester_run_id=$2),
		(SELECT COUNT(*) FROM agent_member_requests WHERE requester_user_id=$3 AND created_at>NOW()-INTERVAL '1 hour')`,
		input.TargetAgentID, input.RequesterRunID, input.RequesterUserID).Scan(&open, &perRun, &lastHour); err != nil {
		return err
	}
	if open >= memberRequestMaxOpen {
		return ErrAgentRequestBusy
	}
	external := strings.HasPrefix(input.RequesterRunID, MentionRequesterPrefix) ||
		strings.HasPrefix(input.RequesterRunID, ReplyRequesterPrefix)
	if (!external && perRun >= memberRequestsPerRun) || lastHour >= memberRequestsPerUserHour {
		return ErrAgentRequestLimit
	}
	return nil
}
