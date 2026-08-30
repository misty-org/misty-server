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

func (db *Database) SocialBindingsForInbound(ctx context.Context, provider, externalConversationID, externalParentID string) ([]SocialBinding, error) {
	items := []SocialBinding{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+socialBindingColumns+` FROM social_bindings WHERE provider=$1 AND status='active' AND disabled_at IS NULL AND (($1='discord' AND external_resource_id=$2) OR ($1='instagram' AND external_resource_id=$2 AND external_parent_id=$3))`, provider, externalConversationID, externalParentID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SocialBinding
			if err := scanSocialBinding(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) ImportSocialInboundMessage(ctx context.Context, binding SocialBinding, externalID, authorID, authorName, authorHandle, authorKind, text string, createdAt time.Time, raw json.RawMessage) (string, bool, error) {
	messageID := "space_message_" + uuid.NewString()
	inserted := false
	if strings.TrimSpace(authorName) == "" {
		authorName = strings.TrimPrefix(authorHandle, "@")
		if authorName == "" {
			authorName = "External user"
		}
	}
	if authorKind != "bot" && authorKind != "business" {
		authorKind = "person"
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		identityID := "social_identity_" + uuid.NewString()
		if err := tx.QueryRowContext(ctx, `INSERT INTO social_identities(id,binding_id,provider,external_user_id,display_name,handle,kind) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(binding_id,external_user_id) DO UPDATE SET display_name=EXCLUDED.display_name,handle=EXCLUDED.handle,kind=EXCLUDED.kind,updated_at=NOW() RETURNING id`, identityID, binding.ID, binding.Provider, authorID, authorName, authorHandle, authorKind).Scan(&identityID); err != nil {
			return err
		}
		content := mustJSON([]MessageSpan{{Type: "text", Text: text}})
		origin := mustJSON(map[string]any{"system": binding.Provider, "external_id": externalID, "author_id": authorID, "author_name": authorName, "author_handle": authorHandle, "raw": raw})
		result, err := tx.ExecContext(ctx, `INSERT INTO space_messages(id,space_id,conversation_id,sender_user_id,sender_kind,content,origin,expires_at,social_provider,social_external_id,social_external_conversation_id,social_identity_id,social_direction,social_delivery_state,created_at) VALUES($1,$2,$3,$4,'system',$5,$6,NULL,$7,$8,$9,$10,'inbound','delivered',$11) ON CONFLICT(space_id,social_provider,social_external_id) WHERE social_provider IS NOT NULL AND social_external_id<>'' DO NOTHING`, messageID, binding.SpaceID, binding.ConversationID, binding.ConnectedByUserID, content, origin, binding.Provider, externalID, binding.ExternalResourceID, identityID, createdAt)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			return nil
		}
		inserted = true
		if _, err := tx.ExecContext(ctx, `UPDATE space_conversations SET updated_at=GREATEST(updated_at,$2) WHERE id=$1`, binding.ConversationID, createdAt); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, binding.SpaceID, "", "message.created", messageID, map[string]any{"message_id": messageID, "conversation_id": binding.ConversationID, "sender_name": authorName, "provider": binding.Provider})
		return err
	})
	return messageID, inserted, err
}

func (db *Database) SocialAutomationForInbound(ctx context.Context, binding SocialBinding, triggerMessageID string) (*SocialAutomationCandidate, error) {
	item := &SocialAutomationCandidate{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT r.id,r.space_id,r.binding_id,COALESCE(r.conversation_id,b.conversation_id),r.authority_id,r.created_by_user_id,r.instructions,r.tone,r.confidence_threshold FROM social_automation_rules r JOIN social_bindings b ON b.id=r.binding_id JOIN social_send_authorities a ON a.id=r.authority_id WHERE r.binding_id=$1 AND (r.conversation_id IS NULL OR r.conversation_id=b.conversation_id) AND r.enabled AND r.paused_at IS NULL AND a.allow_automation AND a.revoked_at IS NULL AND NOT EXISTS(SELECT 1 FROM social_automation_runs run WHERE run.rule_id=r.id AND run.created_at>NOW()-INTERVAL '1 hour' GROUP BY run.rule_id HAVING COUNT(*) FILTER(WHERE run.decision='reply')>=r.max_replies_per_hour) AND NOT EXISTS(SELECT 1 FROM social_automation_runs run WHERE run.rule_id=r.id AND run.created_at>NOW()-INTERVAL '1 day' GROUP BY run.rule_id HAVING COUNT(*) FILTER(WHERE run.decision='reply')>=r.max_replies_per_day) AND NOT EXISTS(SELECT 1 FROM social_automation_runs run WHERE run.rule_id=r.id AND run.decision='reply' AND run.created_at>NOW()-(r.cooldown_seconds||' seconds')::interval) ORDER BY r.created_at LIMIT 1`, binding.ID).Scan(&item.RuleID, &item.SpaceID, &item.BindingID, &item.ConversationID, &item.AuthorityID, &item.UserID, &item.Instructions, &item.Tone, &item.ConfidenceThreshold)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return item, err
}

func (db *Database) RecordSocialAutomationDecision(ctx context.Context, candidate SocialAutomationCandidate, triggerMessageID, decision, reason, reply string, confidence float64) (string, error) {
	runID := "social_run_" + uuid.NewString()
	commandID := ""
	content := mustJSON([]MessageSpan{{Type: "text", Text: reply}})
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		if decision == "reply" {
			commandID = "social_command_" + uuid.NewString()
			if _, err := tx.ExecContext(ctx, `INSERT INTO social_outbound_commands(id,space_id,binding_id,conversation_id,authority_id,requested_by_user_id,source_kind,content,idempotency_key) VALUES($1,$2,$3,$4,$5,$6,'automation',$7,$8)`, commandID, candidate.SpaceID, candidate.BindingID, candidate.ConversationID, candidate.AuthorityID, candidate.UserID, content, "automation:"+triggerMessageID); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO social_automation_runs(id,space_id,rule_id,trigger_message_id,outbound_command_id,decision,reason_code,confidence,draft_content) VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8,$9)`, runID, candidate.SpaceID, candidate.RuleID, triggerMessageID, commandID, decision, reason, confidence, content)
		return err
	})
	return commandID, err
}

func (db *Database) PendingSocialAutomationTriggers(ctx context.Context, limit int) ([]SocialAutomationTrigger, error) {
	if limit < 1 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	items := []SocialAutomationTrigger{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT r.id,r.space_id,r.binding_id,COALESCE(r.conversation_id,b.conversation_id),r.authority_id,r.created_by_user_id,r.instructions,r.tone,r.confidence_threshold,m.id,COALESCE((SELECT string_agg(COALESCE(span->>'text',''),'') FROM jsonb_array_elements(m.content) span),''),COALESCE(i.kind,'person') FROM social_automation_rules r JOIN social_bindings b ON b.id=r.binding_id JOIN social_send_authorities a ON a.id=r.authority_id JOIN space_messages m ON m.conversation_id=b.conversation_id AND m.social_direction='inbound' LEFT JOIN social_identities i ON i.id=m.social_identity_id WHERE r.enabled AND r.paused_at IS NULL AND a.allow_automation AND a.revoked_at IS NULL AND b.status='active' AND b.disabled_at IS NULL AND (r.conversation_id IS NULL OR r.conversation_id=b.conversation_id) AND NOT EXISTS(SELECT 1 FROM social_automation_runs run WHERE run.trigger_message_id=m.id AND run.rule_id=r.id) AND NOT EXISTS(SELECT 1 FROM social_outbound_commands human WHERE human.binding_id=b.id AND human.source_kind='manual' AND human.created_at>m.created_at) AND NOT EXISTS(SELECT 1 FROM social_automation_runs run WHERE run.rule_id=r.id AND run.created_at>NOW()-INTERVAL '1 hour' GROUP BY run.rule_id HAVING COUNT(*) FILTER(WHERE run.decision='reply')>=r.max_replies_per_hour) AND NOT EXISTS(SELECT 1 FROM social_automation_runs run JOIN social_automation_rules rr ON rr.id=run.rule_id JOIN social_bindings bb ON bb.id=rr.binding_id WHERE bb.connection_id=b.connection_id AND run.created_at>NOW()-INTERVAL '1 day' GROUP BY bb.connection_id HAVING COUNT(*) FILTER(WHERE run.decision='reply')>=a.daily_limit) AND NOT EXISTS(SELECT 1 FROM social_automation_runs run WHERE run.rule_id=r.id AND run.decision='reply' AND run.created_at>NOW()-(r.cooldown_seconds||' seconds')::interval) ORDER BY m.created_at LIMIT $1`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SocialAutomationTrigger
			if err := rows.Scan(&item.RuleID, &item.SpaceID, &item.BindingID, &item.ConversationID, &item.AuthorityID, &item.UserID, &item.Instructions, &item.Tone, &item.ConfidenceThreshold, &item.TriggerMessageID, &item.Text, &item.AuthorKind); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}
