package db

import (
	"context"
	"database/sql"
)

// AgentPendingBrowserApprovals excludes retired SDK invocations before pagination.
func (db *Database) AgentPendingBrowserApprovals(ctx context.Context, userID, cursor string, limit int) (*SDKApprovalPage, error) {
	if AppAuthorityFromContext(ctx) != nil {
		return nil, ErrAppRuntimeForbidden
	}
	if limit < 1 || limit > 100 || len(cursor) > 100 {
		return nil, ErrSpaceInvalid
	}
	page := &SDKApprovalPage{Approvals: []SDKApprovalSummary{}}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT a.id,COALESCE(a.run_id,a.invocation_id),a.tool_name,a.summary,a.expires_at,a.created_at
   FROM agent_run_tool_approvals a LEFT JOIN space_runs r ON r.id=a.run_id LEFT JOIN ai_invocations i ON i.id=a.invocation_id
   WHERE a.owner_user_id=$1 AND COALESCE(r.owner_user_id,i.user_id)=$1 AND a.state='pending' AND a.expires_at>NOW() AND a.sdk_review_ciphertext IS NOT NULL AND a.id>$2
    AND COALESCE(r.state,i.state)='awaiting_approval' AND COALESCE(r.approval_wait_id,i.approval_wait_id)=a.id AND (i.surface_id IS NULL OR i.surface_id <> 'sdk') AND (a.tool_name IN ('browser.click','browser.interact','browser.workspace.interact') OR a.tool_name LIKE 'sdk.%')
   ORDER BY a.id LIMIT $3`, userID, cursor, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SDKApprovalSummary
			if err := rows.Scan(&item.ID, &item.RunID, &item.ToolName, &item.Summary, &item.ExpiresAt, &item.CreatedAt); err != nil {
				return err
			}
			page.Approvals = append(page.Approvals, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	if len(page.Approvals) > limit {
		page.Approvals = page.Approvals[:limit]
		page.NextCursor = page.Approvals[limit-1].ID
	}
	return page, nil
}
