package db

import "database/sql"

// ReleaseUnstartedWorkflowDeviceNodeJob puts back a job whose claim never
// reached its device: a held claim answered just as the device hung up. Only
// the claim's own token can release it, and only before it began, so nothing
// can have run. The release does not count as a delivery attempt.
func (db *Database) ReleaseUnstartedWorkflowDeviceNodeJob(userID, deviceID, jobID, token string) error {
	return db.agentTx(userID, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE workflow_device_node_jobs SET state='queued',leased_device_id=NULL,lease_token_hash=NULL,lease_expires_at=NULL,
   delivery_attempts=GREATEST(delivery_attempts-1,0)
   WHERE id=$1 AND user_id=$2 AND leased_device_id=$3 AND lease_token_hash=$4 AND state='leased' AND execution_started_at IS NULL`,
			jobID, userID, deviceID, HashToken(token))
		return err
	})
}
