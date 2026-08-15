package db

import (
	"context"
	"database/sql"
	"errors"
)

func (db *Database) SelfHostCollaborationState(ctx context.Context, resourceType, resourceID string) ([]byte, string, int64, error) {
	var state []byte
	var checksum string
	var aclVersion int64
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT state,checksum_sha256,acl_version FROM self_host_collaboration_documents WHERE resource_type=$1 AND resource_id=$2`, resourceType, resourceID).Scan(&state, &checksum, &aclVersion)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", 0, nil
	}
	return state, checksum, aclVersion, err
}

func (db *Database) PutSelfHostCollaborationState(ctx context.Context, resourceType, resourceID string, state []byte, checksum string, aclVersion int64) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO self_host_collaboration_documents (resource_type,resource_id,state,checksum_sha256,acl_version)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (resource_type,resource_id) DO UPDATE
			SET state=EXCLUDED.state,checksum_sha256=EXCLUDED.checksum_sha256,
			    acl_version=GREATEST(self_host_collaboration_documents.acl_version,EXCLUDED.acl_version),updated_at=NOW()
		`, resourceType, resourceID, state, checksum, aclVersion)
		return err
	})
}

func (db *Database) DeleteSelfHostCollaborationState(ctx context.Context, resourceType, resourceID string) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM self_host_collaboration_documents WHERE resource_type=$1 AND resource_id=$2`, resourceType, resourceID)
		return err
	})
}
