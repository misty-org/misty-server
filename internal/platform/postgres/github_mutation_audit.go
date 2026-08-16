package db

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
)

func (db *Database) RecordGitHubMutationAudit(ctx context.Context, userID, spaceID, workspaceID, source, operation, targetRef, errorCode string, confirmed, success bool) error {
	if !oneOf(source, "user", "agent") || !oneOf(operation, "create_issue", "comment_issue", "create_branch", "create_pull_request") {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO github_mutation_audit
			(id,space_id,workspace_id,actor_user_id,source,operation,confirmed,success,target_ref,error_code)
			VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10)`, "ghaudit_"+uuid.NewString(), spaceID, workspaceID, userID, source, operation, confirmed, success, targetRef, errorCode)
		return err
	})
}
