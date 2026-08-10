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

func (db *Database) ExpireSpaceActionSuggestions(ctx context.Context) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `UPDATE space_action_suggestion_batches SET status='expired',version=version+1,updated_at=NOW() WHERE status IN ('active','partial') AND expires_at<=NOW() RETURNING id,space_id,COALESCE(conversation_id,'')`)
		if err != nil {
			return err
		}
		type expiredBatch struct{ id, spaceID, conversationID string }
		items := []expiredBatch{}
		for rows.Next() {
			var item expiredBatch
			if err := rows.Scan(&item.id, &item.spaceID, &item.conversationID); err != nil {
				return err
			}
			items = append(items, item)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, item := range items {
			if _, err := recordSpaceEventTx(ctx, tx, item.spaceID, "", "action_suggestion.updated", item.id, map[string]any{"batch_id": item.id, "conversation_id": item.conversationID, "status": "expired"}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (db *Database) DismissSpaceActionSuggestion(ctx context.Context, userID, spaceID, batchID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO space_action_suggestion_dismissals(batch_id,user_id) SELECT id,$3 FROM space_action_suggestion_batches WHERE id=$1 AND space_id=$2 ON CONFLICT DO NOTHING`, batchID, spaceID, userID)
		return err
	})
}

func (db *Database) SpaceSuggestionParticipatingAgentIDs(ctx context.Context, userID, spaceID string, scope SpaceConversationScopeRef) (map[string]bool, error) {
	out := map[string]bool{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		query := `SELECT agent_id FROM personal_agent_space_grants WHERE space_id=$1 AND enabled AND removed_at IS NULL AND approved_version_id IS NOT NULL`
		args := []any{spaceID}
		if scope.Kind == ConversationScopePrivate {
			if err := requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, scope.ConversationID); err != nil {
				return err
			}
			query = `SELECT cm.agent_id FROM space_conversation_members cm JOIN personal_agent_space_grants g ON g.space_id=$1 AND g.agent_id=cm.agent_id AND g.enabled AND g.removed_at IS NULL AND g.approved_version_id IS NOT NULL WHERE cm.conversation_id=$2 AND cm.actor_kind='agent'`
			args = append(args, scope.ConversationID)
		}
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			out[id] = true
		}
		return rows.Err()
	})
	return out, err
}

func suggestionPermissionForCapability(capability string) string {
	switch capability {
	case "tasks.create", "calendar.events.create", "roadmaps.items.create":
		return PermissionTasksManage
	case "journal.notes.create", "conversation.follow_up.schedule":
		return PermissionMessagesWrite
	default:
		return ""
	}
}

// AuthorizeSuggestionAction recomputes the complete member/agent/version/
// participation/capability intersection immediately before acceptance.
func (db *Database) AuthorizeSuggestionAction(ctx context.Context, userID, spaceID, agentID, capability string, scope SpaceConversationScopeRef) error {
	permission := suggestionPermissionForCapability(capability)
	if permission == "" {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAgentsRun); err != nil {
			return err
		}
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, permission); err != nil {
			return err
		}
		if scope.Kind == ConversationScopePrivate {
			if err := requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, scope.ConversationID); err != nil {
				return err
			}
		}
		var grants json.RawMessage
		var enabled, participates bool
		err := tx.QueryRowContext(ctx, `SELECT g.capability_grants,g.enabled AND g.removed_at IS NULL AND g.approved_version_id IS NOT NULL,
			CASE WHEN $3='everyone' THEN TRUE ELSE EXISTS(SELECT 1 FROM space_conversation_members cm WHERE cm.conversation_id=$4 AND cm.actor_kind='agent' AND cm.agent_id=g.agent_id) END
			FROM personal_agent_space_grants g WHERE g.space_id=$1 AND g.agent_id=$2`, spaceID, agentID, scope.Kind, scope.ConversationID).
			Scan(&grants, &enabled, &participates)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		if !enabled || !participates || !AgentCapabilityGranted(grants, capability, "write") {
			return ErrSpaceForbidden
		}
		return nil
	})
}

func (db *Database) AcceptSpaceActionSuggestion(ctx context.Context, userID, spaceID, batchID string, review SpaceActionSuggestionAcceptance) (*SpaceActionSuggestionBatch, []SpaceActionSuggestionItem, error) {
	if review.Version < 1 || len(review.Items) < 1 || len(review.Items) > 3 {
		return nil, nil, ErrSpaceInvalid
	}
	selected := map[string]SpaceActionSuggestionReviewItem{}
	for _, item := range review.Items {
		if item.ItemID == "" || item.SelectedAgentID == "" || !validJSONObject(item.ApprovedInput) {
			return nil, nil, ErrSpaceInvalid
		}
		if _, duplicate := selected[item.ItemID]; duplicate {
			return nil, nil, ErrSpaceInvalid
		}
		selected[item.ItemID] = item
	}
	accepted := []SpaceActionSuggestionItem{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		var scopeKind, conversationID, status string
		var version int64
		err := tx.QueryRowContext(ctx, `SELECT scope_kind,COALESCE(conversation_id,''),status,version FROM space_action_suggestion_batches WHERE id=$1 AND space_id=$2 AND expires_at>NOW() FOR UPDATE`, batchID, spaceID).
			Scan(&scopeKind, &conversationID, &status, &version)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		if status != "active" && status != "partial" || version != review.Version {
			return ErrSpaceConflict
		}
		if scopeKind == ConversationScopePrivate {
			if err := requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, conversationID); err != nil {
				return err
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,batch_id,action_kind,title,summary,proposed_input,COALESCE(approved_input,'null'::jsonb),required_capability,COALESCE(selected_agent_id,''),COALESCE(accepted_by_user_id,''),COALESCE(run_id,''),COALESCE(follow_up_id,''),status,ordinal FROM space_action_suggestion_items WHERE batch_id=$1 ORDER BY ordinal FOR UPDATE`, batchID)
		if err != nil {
			return err
		}
		all := []SpaceActionSuggestionItem{}
		for rows.Next() {
			var item SpaceActionSuggestionItem
			if err := rows.Scan(&item.ID, &item.BatchID, &item.ActionKind, &item.Title, &item.Summary, &item.ProposedInput, &item.ApprovedInput, &item.RequiredCapability, &item.SelectedAgentID, &item.AcceptedByUserID, &item.RunID, &item.FollowUpID, &item.Status, &item.Ordinal); err != nil {
				rows.Close()
				return err
			}
			all = append(all, item)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, item := range all {
			reviewed, ok := selected[item.ID]
			if !ok {
				continue
			}
			if item.Status != "active" {
				return ErrSpaceConflict
			}
			item.ApprovedInput, item.SelectedAgentID, item.AcceptedByUserID, item.Status = reviewed.ApprovedInput, reviewed.SelectedAgentID, userID, "accepted"
			if _, err := tx.ExecContext(ctx, `UPDATE space_action_suggestion_items SET approved_input=$2,selected_agent_id=$3,accepted_by_user_id=$4,status='accepted',updated_at=NOW() WHERE id=$1`, item.ID, item.ApprovedInput, item.SelectedAgentID, userID); err != nil {
				return err
			}
			accepted = append(accepted, item)
		}
		if len(accepted) != len(selected) {
			return ErrSpaceInvalid
		}
		_, err = tx.ExecContext(ctx, `UPDATE space_action_suggestion_batches SET status='partial',version=version+1,updated_at=NOW() WHERE id=$1`, batchID)
		if err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "action_suggestion.updated", batchID, map[string]any{"batch_id": batchID, "conversation_id": conversationID, "status": "partial"})
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	batch, err := db.SpaceActionSuggestion(ctx, userID, spaceID, batchID)
	return batch, accepted, err
}

func (db *Database) CompleteSpaceActionSuggestionItem(ctx context.Context, userID, spaceID, itemID, state, runID, followUpID string) error {
	if state != "completed" && state != "failed" {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var batchID string
		if err := tx.QueryRowContext(ctx, `UPDATE space_action_suggestion_items i SET status=$2,run_id=NULLIF($3,''),follow_up_id=NULLIF($4,''),updated_at=NOW() FROM space_action_suggestion_batches b WHERE i.id=$1 AND b.id=i.batch_id AND b.space_id=$5 AND i.accepted_by_user_id=$6 RETURNING i.batch_id`, itemID, state, runID, followUpID, spaceID, userID).Scan(&batchID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE space_action_suggestion_batches b SET status=CASE WHEN EXISTS(SELECT 1 FROM space_action_suggestion_items WHERE batch_id=b.id AND status='active') THEN 'partial' ELSE 'resolved' END,resolved_at=CASE WHEN EXISTS(SELECT 1 FROM space_action_suggestion_items WHERE batch_id=b.id AND status='active') THEN NULL ELSE NOW() END,version=version+1,updated_at=NOW() WHERE id=$1`, batchID)
		return err
	})
}

func (db *Database) SpaceActionSuggestionForRun(ctx context.Context, userID, runID string) (*SpaceActionSuggestionBatch, *SpaceActionSuggestionItem, error) {
	var batchID, spaceID, itemID string
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT i.batch_id,b.space_id,i.id FROM space_action_suggestion_items i JOIN space_action_suggestion_batches b ON b.id=i.batch_id WHERE i.run_id=$1 AND i.accepted_by_user_id=$2`, runID, userID).Scan(&batchID, &spaceID, &itemID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrSpaceNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	batch, err := db.SpaceActionSuggestion(ctx, userID, spaceID, batchID)
	if err != nil {
		return nil, nil, err
	}
	for index := range batch.Items {
		if batch.Items[index].ID == itemID {
			return batch, &batch.Items[index], nil
		}
	}
	return nil, nil, ErrSpaceNotFound
}

func (db *Database) CreateConversationFollowUp(ctx context.Context, userID string, item SpaceConversationFollowUp, itemID string) (*SpaceConversationFollowUp, error) {
	item.ReminderText = strings.TrimSpace(item.ReminderText)
	item.Timezone = strings.TrimSpace(item.Timezone)
	if item.Timezone == "" {
		item.Timezone = "UTC"
	}
	if item.ReminderText == "" || item.DeliverAt.Before(time.Now().UTC()) || len(item.RecipientUserIDs) < 1 {
		return nil, ErrSpaceInvalid
	}
	if _, err := time.LoadLocation(item.Timezone); err != nil {
		return nil, ErrSpaceInvalid
	}
	item.ID, item.AuthorizingUserID, item.State = "followup_"+uuid.NewString(), userID, "queued"
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, item.SpaceID, PermissionMessagesWrite); err != nil {
			return err
		}
		if item.SourceScope.Kind == ConversationScopePrivate {
			if err := requireSpaceConversationMemberTx(ctx, tx, userID, item.SpaceID, item.SourceScope.ConversationID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_conversation_follow_ups(id,space_id,source_scope_kind,source_conversation_id,source_message_id,suggestion_item_id,authorizing_user_id,agent_id,reminder_text,deliver_at,timezone) VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,$7,$8,$9,$10,$11)`, item.ID, item.SpaceID, item.SourceScope.Kind, item.SourceScope.ConversationID, item.SourceMessageID, itemID, userID, item.AgentID, item.ReminderText, item.DeliverAt, item.Timezone); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, recipient := range item.RecipientUserIDs {
			if recipient == "" || seen[recipient] {
				return ErrSpaceInvalid
			}
			seen[recipient] = true
			var valid bool
			if item.SourceScope.Kind == ConversationScopeEveryone {
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_members WHERE space_id=$1 AND user_id=$2)`, item.SpaceID, recipient).Scan(&valid); err != nil {
					return err
				}
			} else if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_conversation_members WHERE conversation_id=$1 AND actor_kind='person' AND user_id=$2)`, item.SourceScope.ConversationID, recipient).Scan(&valid); err != nil {
				return err
			}
			if !valid {
				return ErrSpaceForbidden
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_conversation_follow_up_recipients(follow_up_id,user_id) VALUES($1,$2)`, item.ID, recipient); err != nil {
				return err
			}
		}
		_, err := recordSpaceEventTx(ctx, tx, item.SpaceID, userID, "conversation_follow_up.created", item.ID, map[string]any{"follow_up_id": item.ID, "conversation_id": item.SourceScope.ConversationID, "state": "queued"})
		return err
	})
	return &item, err
}

func (db *Database) LeaseDueConversationFollowUps(ctx context.Context, limit int) ([]SpaceConversationFollowUp, error) {
	if limit < 1 || limit > 50 {
		limit = 10
	}
	items := []SpaceConversationFollowUp{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `UPDATE space_conversation_follow_ups f SET state='working',updated_at=NOW()
			WHERE f.id IN (SELECT id FROM space_conversation_follow_ups WHERE (state='queued' AND deliver_at<=NOW()) OR (state='working' AND updated_at<NOW()-INTERVAL '5 minutes') ORDER BY deliver_at FOR UPDATE SKIP LOCKED LIMIT $1)
			RETURNING id,space_id,source_scope_kind,COALESCE(source_conversation_id,''),COALESCE(source_message_id,''),authorizing_user_id,agent_id,reminder_text,deliver_at,timezone,state`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceConversationFollowUp
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.SourceScope.Kind, &item.SourceScope.ConversationID, &item.SourceMessageID, &item.AuthorizingUserID, &item.AgentID, &item.ReminderText, &item.DeliverAt, &item.Timezone, &item.State); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) ConversationFollowUpRecipients(ctx context.Context, followUpID string) ([]string, error) {
	items := []string{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT user_id FROM space_conversation_follow_up_recipients WHERE follow_up_id=$1 AND state='queued' ORDER BY user_id`, followUpID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			items = append(items, id)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) ConversationFollowUpRecipientEligible(ctx context.Context, item SpaceConversationFollowUp, userID string) (bool, error) {
	var eligible bool
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if item.SourceScope.Kind == ConversationScopeEveryone {
			return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_members WHERE space_id=$1 AND user_id=$2)`, item.SpaceID, userID).Scan(&eligible)
		}
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_conversation_members cm JOIN space_conversations c ON c.id=cm.conversation_id WHERE cm.conversation_id=$1 AND cm.actor_kind='person' AND cm.user_id=$2 AND c.space_id=$3)`, item.SourceScope.ConversationID, userID, item.SpaceID).Scan(&eligible)
	})
	return eligible, err
}

func (db *Database) FinishConversationFollowUpRecipient(ctx context.Context, followUpID, userID, state, conversationID, messageID, errorCode string) error {
	if state != "delivered" && state != "skipped" && state != "failed" {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE space_conversation_follow_up_recipients SET state=$3,direct_conversation_id=NULLIF($4,''),delivered_message_id=NULLIF($5,''),error_code=$6,updated_at=NOW() WHERE follow_up_id=$1 AND user_id=$2 AND state='queued'`, followUpID, userID, state, conversationID, messageID, errorCode); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_conversation_follow_ups f SET state=CASE
			WHEN EXISTS(SELECT 1 FROM space_conversation_follow_up_recipients WHERE follow_up_id=f.id AND state='queued') THEN 'working'
			WHEN EXISTS(SELECT 1 FROM space_conversation_follow_up_recipients WHERE follow_up_id=f.id AND state='delivered') AND EXISTS(SELECT 1 FROM space_conversation_follow_up_recipients WHERE follow_up_id=f.id AND state IN ('skipped','failed','opted_out')) THEN 'partially_delivered'
			WHEN EXISTS(SELECT 1 FROM space_conversation_follow_up_recipients WHERE follow_up_id=f.id AND state='delivered') THEN 'delivered' ELSE 'failed' END,updated_at=NOW() WHERE id=$1`, followUpID); err != nil {
			return err
		}
		var spaceID, sourceConversationID, followUpState string
		if err := tx.QueryRowContext(ctx, `SELECT space_id,COALESCE(source_conversation_id,''),state FROM space_conversation_follow_ups WHERE id=$1`, followUpID).Scan(&spaceID, &sourceConversationID, &followUpState); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, "", "conversation_follow_up.updated", followUpID, map[string]any{"follow_up_id": followUpID, "conversation_id": sourceConversationID, "state": followUpState, "recipient_user_id": userID, "recipient_state": state})
		return err
	})
}

func (db *Database) CancelConversationFollowUp(ctx context.Context, userID, spaceID, followUpID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE space_conversation_follow_ups SET state='canceled',updated_at=NOW() WHERE id=$1 AND space_id=$2 AND authorizing_user_id=$3 AND state IN ('queued','working')`, followUpID, spaceID, userID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return ErrSpaceConflict
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "conversation_follow_up.updated", followUpID, map[string]any{"follow_up_id": followUpID, "state": "canceled"})
		return err
	})
}

func (db *Database) OptOutConversationFollowUp(ctx context.Context, userID, spaceID, followUpID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE space_conversation_follow_up_recipients r SET state='opted_out',updated_at=NOW() FROM space_conversation_follow_ups f WHERE r.follow_up_id=$1 AND r.user_id=$2 AND f.id=r.follow_up_id AND f.space_id=$3 AND r.state='queued'`, followUpID, userID, spaceID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return ErrSpaceConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_conversation_follow_ups f SET state='canceled',updated_at=NOW() WHERE id=$1 AND NOT EXISTS(SELECT 1 FROM space_conversation_follow_up_recipients WHERE follow_up_id=f.id AND state='queued') AND NOT EXISTS(SELECT 1 FROM space_conversation_follow_up_recipients WHERE follow_up_id=f.id AND state='delivered')`, followUpID); err != nil {
			return err
		}
		var conversationID string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(source_conversation_id,'') FROM space_conversation_follow_ups WHERE id=$1`, followUpID).Scan(&conversationID); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "conversation_follow_up.updated", followUpID, map[string]any{"follow_up_id": followUpID, "conversation_id": conversationID, "recipient_user_id": userID, "recipient_state": "opted_out"})
		return err
	})
}

func (db *Database) InvalidateSpaceActionSuggestionsForMessage(ctx context.Context, spaceID, conversationID, messageID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE space_action_suggestion_batches b SET status='invalidated',version=version+1,updated_at=NOW() WHERE b.space_id=$1 AND b.status IN ('active','partial') AND (b.anchor_message_id=$3 OR EXISTS(SELECT 1 FROM jsonb_array_elements(b.evidence) e WHERE e->>'message_id'=$3)) AND COALESCE(b.conversation_id,'')=$2`, spaceID, conversationID, messageID)
		return err
	})
}

func (db *Database) InvalidateActiveSpaceActionSuggestions(ctx context.Context, spaceID, conversationID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE space_action_suggestion_batches SET status='invalidated',version=version+1,updated_at=NOW() WHERE space_id=$1 AND COALESCE(conversation_id,'')=$2 AND status IN ('active','partial')`, spaceID, conversationID)
		return err
	})
}
