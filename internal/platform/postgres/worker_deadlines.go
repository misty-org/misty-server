package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// libraryJobEligibility is shared by claiming and deadline planning. A queued
// item whose source is unavailable must wait for a source transition, not spin.
// The caller aliases library_processing_jobs as j; kind is an internal constant.
func libraryJobEligibility(kind string) string {
	switch kind {
	case "ai":
		return `EXISTS(SELECT 1 FROM space_library_items i JOIN library_files f ON f.id=i.file_id
   JOIN library_blobs b ON b.id=f.blob_id WHERE i.id=j.target_id
   AND i.lifecycle_state='ready' AND f.lifecycle_state='ready' AND b.lifecycle_state='ready')`
	case "faces":
		return `EXISTS(SELECT 1 FROM space_library_items i JOIN library_files f ON f.id=i.file_id
   JOIN library_blobs b ON b.id=f.blob_id WHERE i.id=j.target_id
   AND i.lifecycle_state='ready' AND b.lifecycle_state='ready')`
	case "edit":
		return `EXISTS(SELECT 1 FROM library_item_versions v JOIN space_library_items i ON i.id=v.space_library_item_id
   JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id
   JOIN space_rendition_reservations r ON r.source_kind='edit' AND r.source_id=v.id AND r.state='active'
   WHERE v.id=j.target_id AND v.lifecycle_state='ready' AND i.lifecycle_state='ready' AND b.lifecycle_state='ready')`
	default:
		return "false"
	}
}

// NextWorkerDelay uses database time so API-host clock skew cannot strand a
// scheduled retry. NULL means no pending work and therefore no timer or scan.
func (db *Database) NextWorkerDelay(ctx context.Context, queue string) (time.Duration, bool, error) {
	var deadline string
	switch queue {
	case "library-ai", "library-edit", "library-faces":
		kind := queue[len("library-"):]
		eligible := libraryJobEligibility(kind)
		deadline = `SELECT min(due) FROM (
   (SELECT available_at AS due FROM library_processing_jobs j
    WHERE job_kind='` + kind + `' AND state='queued' AND ` + eligible + ` ORDER BY available_at LIMIT 1)
   UNION ALL
   (SELECT lease_expires_at AS due FROM library_processing_jobs j
    WHERE job_kind='` + kind + `' AND state IN ('leased','running') AND ` + eligible + ` ORDER BY lease_expires_at LIMIT 1)
   ) pending`
	case "abuse-retention":
		deadline = `SELECT min(blocked_until)+interval '1 day' FROM abuse_blocks`
	case "note-control":
		deadline = `SELECT min(next_attempt_at) FROM space_note_control_outbox WHERE delivered_at IS NULL`
	case "drawing-control":
		deadline = `SELECT min(next_attempt_at) FROM space_drawing_control_outbox WHERE delivered_at IS NULL`
	case "drawing-purge":
		deadline = `SELECT clock_timestamp() FROM space_drawings d WHERE d.lifecycle_state='deleting'
   AND EXISTS(SELECT 1 FROM space_drawing_control_outbox o WHERE o.drawing_id=d.id
   AND o.command='purge' AND o.delivered_at IS NOT NULL) LIMIT 1`
	case "social":
		deadline = socialWorkerDeadline
	case "billing":
		deadline = billingWorkerDeadline
	case "agent-runtime":
		deadline = agentRuntimeWorkerDeadline
	case "agent-tasks":
		deadline = agentTaskWorkerDeadline
	case "scheduled":
		deadline = scheduledWorkerDeadline
	case "ai-cleanup":
		deadline = aiCleanupWorkerDeadline
	case "account-deletion":
		deadline = accountDeletionWorkerDeadline
	case "rendition-reservations":
		deadline = renditionReservationWorkerDeadline
	case "embedding":
		deadline = `SELECT min(due) FROM (
   (SELECT clock_timestamp() AS due FROM ai_retrieval_chunks c JOIN ai_retrieval_documents d ON d.id=c.document_id
    WHERE d.lifecycle_state='active' AND d.owner_user_id IS NOT NULL AND c.embedding IS NULL
    AND c.embedding_lease_until IS NULL LIMIT 1)
   UNION ALL
   (SELECT c.embedding_lease_until AS due FROM ai_retrieval_chunks c JOIN ai_retrieval_documents d ON d.id=c.document_id
    WHERE d.lifecycle_state='active' AND d.owner_user_id IS NOT NULL AND c.embedding IS NULL
    AND c.embedding_lease_until IS NOT NULL ORDER BY c.embedding_lease_until LIMIT 1)
   ) pending`
	default:
		return 0, false, errors.New("unknown worker queue")
	}
	var seconds sql.NullFloat64
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXTRACT(EPOCH FROM ((`+deadline+`)-clock_timestamp()))`).Scan(&seconds)
	})
	if err != nil || !seconds.Valid {
		return 0, false, err
	}
	// Avoid duration overflow for rows deliberately scheduled far in the future.
	delay := time.Duration(max(0, min(seconds.Float64, 365*24*3600)) * float64(time.Second))
	return delay, true, nil
}
