package db

import (
	"context"
	"database/sql"
)

type NativeAgentActionReceipt struct {
	ToolName string `json:"tool"`
	State    string `json:"state"`
	Request  string `json:"request"`
	Result   string `json:"result"`
	Error    string `json:"error,omitempty"`
	AppID    string `json:"-"`
}

// Bounded write receipts make interrupted work inspectable by its next turn.
// Browser screenshots, credentials and unassigned page contents are not replayed.
func (db *Database) NativeAgentConversationReceipts(ctx context.Context, userID, agentID, _legacySpaceID, conversationID, currentInvocation string) ([]NativeAgentActionReceipt, error) {
	items := []NativeAgentActionReceipt{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT j.tool_name,j.state,LEFT(j.request::text,2000),LEFT((j.result-'image')::text,3000),COALESCE(j.error_code,''),COALESCE((SELECT c.metadata->>'app_id' FROM ai_invocation_contexts c WHERE c.user_id=$1 AND c.invocation_id=i.id AND c.opaque_ref=j.request->>'scopeId' LIMIT 1),'') FROM agent_toolbox_action_journal j JOIN ai_invocations i ON i.id=j.run_id AND i.user_id=j.user_id WHERE j.user_id=$1 AND i.request_payload->>'agent_id'=$2 AND i.conversation_id=$3 AND i.id<>$4 ORDER BY j.created_at DESC LIMIT 12`, userID, agentID, conversationID, currentInvocation)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var receipt NativeAgentActionReceipt
			if err := rows.Scan(&receipt.ToolName, &receipt.State, &receipt.Request, &receipt.Result, &receipt.Error, &receipt.AppID); err != nil {
				return err
			}
			items = append(items, receipt)
		}
		return rows.Err()
	})
	return items, err
}
