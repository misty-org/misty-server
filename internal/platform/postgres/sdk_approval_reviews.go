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
	if !strings.HasPrefix(approval.ToolName, "sdk.") && approval.ToolName != "browser.click" && approval.ToolName != "browser.interact" && approval.ToolName != "browser.workspace.interact" {
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
