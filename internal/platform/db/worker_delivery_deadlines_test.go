package db

import (
	"context"
	"testing"
	"time"
)

func checkWorkerDelay(t *testing.T, database *Database, queue string, pending bool, low, high time.Duration) {
	t.Helper()
	delay, hasWork, err := database.NextWorkerDelay(context.Background(), queue)
	if err != nil || hasWork != pending || (pending && (delay < low || delay > high)) {
		t.Fatalf("%s: delay=%s pending=%v err=%v", queue, delay, hasWork, err)
	}
}
func workerExec(t *testing.T, database *Database, statement string) {
	t.Helper()
	if _, err := database.Conn.Exec(statement); err != nil {
		t.Fatal(err)
	}
}
func TestWorkerControlAndEmbeddingDeadlines(t *testing.T) {
	database := workerTestDatabase(t)
	workerExec(t, database, `INSERT INTO space_note_control_outbox VALUES('note',now()+interval '1 hour',NULL)`)
	checkWorkerDelay(t, database, "note-control", true, 59*time.Minute, time.Hour)
	workerExec(t, database, `UPDATE space_note_control_outbox SET delivered_at=now()`)
	checkWorkerDelay(t, database, "note-control", false, 0, 0)
	workerExec(t, database, `INSERT INTO space_drawings VALUES('drawing','deleting');
 INSERT INTO space_drawing_control_outbox VALUES('command','drawing','purge',now()+interval '1 hour',NULL)`)
	checkWorkerDelay(t, database, "drawing-control", true, 59*time.Minute, time.Hour)
	checkWorkerDelay(t, database, "drawing-purge", false, 0, 0)
	workerExec(t, database, `UPDATE space_drawing_control_outbox SET delivered_at=now()`)
	checkWorkerDelay(t, database, "drawing-control", false, 0, 0)
	checkWorkerDelay(t, database, "drawing-purge", true, 0, 0)
	if n, err := database.PurgeDeletedDrawings(context.Background(), 1); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	checkWorkerDelay(t, database, "drawing-purge", false, 0, 0)
	workerExec(t, database, `INSERT INTO ai_retrieval_documents VALUES('doc','owner','active');
 INSERT INTO ai_retrieval_chunks VALUES('doc',0,NULL,NULL)`)
	checkWorkerDelay(t, database, "embedding", true, 0, 0)
	workerExec(t, database, `UPDATE ai_retrieval_chunks SET embedding_lease_until=now()+interval '5 minutes'`)
	checkWorkerDelay(t, database, "embedding", true, 4*time.Minute, 5*time.Minute)
	workerExec(t, database, `UPDATE ai_retrieval_documents SET lifecycle_state='deleted'`)
	checkWorkerDelay(t, database, "embedding", false, 0, 0)
}
func TestWorkerSocialDeadlinesNeverReplayUnknownSends(t *testing.T) {
	database := workerTestDatabase(t)
	workerExec(t, database, `INSERT INTO social_bindings VALUES('binding','disabled',now());
 INSERT INTO social_outbound_commands VALUES('command','queued',now(),NULL,'binding')`)
	checkWorkerDelay(t, database, "social", false, 0, 0)
	workerExec(t, database, `UPDATE social_bindings SET status='active',disabled_at=NULL`)
	checkWorkerDelay(t, database, "social", true, 0, 0)
	workerExec(t, database, `UPDATE social_outbound_commands SET state='sending',lease_expires_at=now()-interval '1 hour'`)
	checkWorkerDelay(t, database, "social", false, 0, 0)
	workerExec(t, database, `INSERT INTO social_send_authorities VALUES('authority',false,NULL);
 INSERT INTO social_scheduled_messages VALUES('scheduled','scheduled',now()+interval '1 hour','authority')`)
	checkWorkerDelay(t, database, "social", false, 0, 0)
	workerExec(t, database, `UPDATE social_send_authorities SET allow_scheduled=true`)
	checkWorkerDelay(t, database, "social", true, 59*time.Minute, time.Hour)
	workerExec(t, database, `UPDATE social_send_authorities SET revoked_at=now()`)
	checkWorkerDelay(t, database, "social", false, 0, 0)
}
func TestWorkerBillingDeadlinesKeepUnmeasuredUsageForReconciliation(t *testing.T) {
	database := workerTestDatabase(t)
	workerExec(t, database, `INSERT INTO voice_usage_journal VALUES('voice','account','reservation','active',now())`)
	checkWorkerDelay(t, database, "billing", true, time.Minute, 2*time.Minute)
	workerExec(t, database, `UPDATE voice_usage_journal SET state='reconcile'`)
	checkWorkerDelay(t, database, "billing", false, 0, 0)
	workerExec(t, database, `UPDATE voice_usage_journal SET state='settlement_pending'`)
	checkWorkerDelay(t, database, "billing", false, 0, 0)
	workerExec(t, database, `INSERT INTO billing_adapter_reservations VALUES('account','reservation')`)
	checkWorkerDelay(t, database, "billing", true, 20*time.Second, 30*time.Second)
	workerExec(t, database, `UPDATE voice_usage_journal SET state='closed';
 INSERT INTO billing_adapter_intents VALUES('intent','abandoned',now()+interval '1 hour');
 INSERT INTO billing_adapter_outbox VALUES('completion',now()+interval '1 minute',NULL)`)
	checkWorkerDelay(t, database, "billing", true, 50*time.Second, time.Minute)
	workerExec(t, database, `UPDATE billing_adapter_outbox SET delivered_at=now()`)
	checkWorkerDelay(t, database, "billing", true, 59*time.Minute, time.Hour)
	workerExec(t, database, `UPDATE billing_adapter_intents SET state='recovered'`)
	checkWorkerDelay(t, database, "billing", false, 0, 0)
}
