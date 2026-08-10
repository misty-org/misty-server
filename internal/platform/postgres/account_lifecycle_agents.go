package db

import (
	"context"
	"database/sql"
)

// disableAccountAgentsTx immediately prevents new work when deletion begins.
// Shared attribution remains intact during the retention window, but no Space
// can invoke an Agent owned by an account that can no longer sign in.
func disableAccountAgentsTx(ctx context.Context, tx *sql.Tx, userID string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE personal_agents SET enabled=FALSE,updated_at=NOW()
		WHERE owner_user_id=$1 AND deleted_at IS NULL`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE personal_agent_space_grants g
		SET enabled=FALSE,removed_at=COALESCE(removed_at,NOW()),updated_at=NOW()
		FROM personal_agents a WHERE g.agent_id=a.id AND a.owner_user_id=$1`, userID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE space_runs SET state='canceled',canceled_at=NOW(),completed_at=NOW(),updated_at=NOW()
		WHERE state IN ('queued','running','cooldown','awaiting_approval') AND
		(agent_id IN (SELECT id FROM personal_agents WHERE owner_user_id=$1) OR requesting_member_id=$1)`, userID)
	return err
}

// purgeAccountAgentsTx removes private conversations and memory, and redacts
// owner-authored behavioral versions. IDs remain for historical attribution in
// shared Task activity and audit records, while prompts and instructions do not.
func purgeAccountAgentsTx(ctx context.Context, tx *sql.Tx, userID string) error {
	if err := disableAccountAgentsTx(ctx, tx, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_conversations WHERE user_id=$1`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM personal_agent_instances
		WHERE invoker_user_id=$1 OR agent_id IN (SELECT id FROM personal_agents WHERE owner_user_id=$1)`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM personal_agent_member_grants WHERE user_id=$1`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE personal_agent_versions SET
		name='Deleted Agent',role='',description='',icon='',avatar='{"kind":"preset","preset_id":"bot","accent":"neutral"}'::jsonb,instructions='',model_mode='pinned',model_id='deleted',reasoning_effort='',
		checksum_sha256=md5(id || ':deleted') || md5(agent_id || ':deleted')
		WHERE agent_id IN (SELECT id FROM personal_agents WHERE owner_user_id=$1)`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE personal_agents SET
		name='Deleted Agent',role='',description='',icon='',avatar='{"kind":"preset","preset_id":"bot","accent":"neutral"}'::jsonb,instructions='',model_mode='pinned',model_id='deleted',reasoning_effort='',
		context_permissions='{}'::jsonb,tool_permissions='{}'::jsonb,enabled=FALSE,
		deleted_at=COALESCE(deleted_at,NOW()),updated_at=NOW()
		WHERE owner_user_id=$1`, userID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE space_runs SET input='{}'::jsonb,result='{"redacted":true}'::jsonb,
		outputs='{}'::jsonb,artifacts='[]'::jsonb,action_envelope='{}'::jsonb,error_message=NULL,updated_at=NOW()
		WHERE requesting_member_id=$1 OR billing_user_id=$1 OR initiated_by_user_id=$1`, userID)
	return err
}
