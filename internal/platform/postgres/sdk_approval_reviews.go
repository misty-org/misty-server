package db

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ProtectedSDKApproval contains encrypted, immutable review data. The service
// binds its plaintext to the validated execution before entering this transaction.
type ProtectedSDKApproval struct {
	EffectID   string
	Digest     string
	Ciphertext []byte
}

func persistSDKApprovalReviewTx(ctx context.Context, tx *sql.Tx, approval *AgentToolApproval, reviews []ProtectedSDKApproval) error {
	// Existing creator-browser approvals predate protected reviews. Preserve their
	// compatibility path; the new quick-AI admission requires a review explicitly.
	if !strings.HasPrefix(approval.ToolName, "sdk.") && len(reviews) == 0 {
		return nil
	}
	if !strings.HasPrefix(approval.ToolName, "sdk.") && approval.ToolName != "browser.click" && approval.ToolName != "browser.interact" {
		if len(reviews) != 0 {
			return ErrSpaceInvalid
		}
		return nil
	}
	if len(reviews) != 1 {
		return ErrSpaceInvalid
	}
	review := reviews[0]
	digest, err := hex.DecodeString(review.Digest)
	if _, errID := uuid.Parse(review.EffectID); errID != nil || err != nil || len(digest) != 32 || strings.ToLower(review.Digest) != review.Digest || len(review.Ciphertext) < 32 || len(review.Ciphertext) > 4<<20 {
		return ErrSpaceInvalid
	}
	// Insert once in the same transaction as the wait. A randomized new ciphertext
	// on a transport retry must not replace the original review.
	result, err := tx.ExecContext(ctx, `UPDATE agent_run_tool_approvals SET sdk_effect_id=$2,sdk_review_digest=$3,sdk_review_ciphertext=$4 WHERE id=$1 AND owner_user_id=$5 AND sdk_review_ciphertext IS NULL AND state='pending'`, approval.ID, review.EffectID, review.Digest, review.Ciphertext, approval.OwnerUserID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 1 {
		return err
	}
	var effectID, storedDigest string
	if err := tx.QueryRowContext(ctx, `SELECT sdk_effect_id::text,sdk_review_digest FROM agent_run_tool_approvals WHERE id=$1 AND owner_user_id=$2 AND sdk_review_ciphertext IS NOT NULL`, approval.ID, approval.OwnerUserID).Scan(&effectID, &storedDigest); err != nil {
		return ErrSpaceConflict
	}
	if effectID != review.EffectID || storedDigest != review.Digest {
		return ErrAppRuntimeForbidden
	}
	return nil
}

// SDKApprovalReview is only readable through trusted user controls. App bearers
// and model runtime credentials cannot retrieve the protected approval payload.
func (db *Database) SDKApprovalReview(ctx context.Context, userID, approvalID string) (*AgentToolApproval, *ProtectedSDKApproval, error) {
	if AppAuthorityFromContext(ctx) != nil {
		return nil, nil, ErrAppRuntimeForbidden
	}
	var approval AgentToolApproval
	var review ProtectedSDKApproval
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if err := scanAgentToolApproval(tx.QueryRowContext(ctx, `SELECT id,COALESCE(run_id,invocation_id),owner_user_id,tool_call_id,tool_name,impact,arguments_hash,signed_call,hook_token,summary,state,expires_at,decided_at FROM agent_run_tool_approvals WHERE id=$1 AND owner_user_id=$2 AND sdk_review_ciphertext IS NOT NULL`, approvalID, userID), &approval); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT sdk_effect_id::text,sdk_review_digest,sdk_review_ciphertext FROM agent_run_tool_approvals WHERE id=$1 AND owner_user_id=$2`, approvalID, userID).Scan(&review.EffectID, &review.Digest, &review.Ciphertext)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrAppRuntimeForbidden
	}
	return &approval, &review, err
}

type SDKApprovalSummary struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	ToolName  string    `json:"tool_name"`
	Summary   string    `json:"summary"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}
type SDKApprovalPage struct {
	Approvals  []SDKApprovalSummary `json:"approvals"`
	NextCursor string               `json:"nextCursor,omitempty"`
}

// SDKPendingApprovals lists only actionable, reviewable waits owned by this user.
// The keyset cursor is an approval ID, never a source of authority.
func (db *Database) SDKPendingApprovals(ctx context.Context, userID, cursor string, limit int) (*SDKApprovalPage, error) {
	if AppAuthorityFromContext(ctx) != nil {
		return nil, ErrAppRuntimeForbidden
	}
	if limit < 1 || limit > 100 || len(cursor) > 100 {
		return nil, ErrSpaceInvalid
	}
	page := &SDKApprovalPage{Approvals: []SDKApprovalSummary{}}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT a.id,COALESCE(a.run_id,a.invocation_id),a.tool_name,a.summary,a.expires_at,a.created_at
   FROM agent_run_tool_approvals a LEFT JOIN space_runs r ON r.id=a.run_id LEFT JOIN ai_invocations i ON i.id=a.invocation_id LEFT JOIN sdk_capability_invocations c ON c.invocation_id=i.id
   WHERE a.owner_user_id=$1 AND COALESCE(r.owner_user_id,i.user_id)=$1 AND a.state='pending' AND a.expires_at>NOW() AND a.sdk_review_ciphertext IS NOT NULL AND a.id>$2
    AND COALESCE(r.state,i.state)='awaiting_approval' AND COALESCE(r.approval_wait_id,i.approval_wait_id)=a.id AND c.cancel_requested_at IS NULL
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
