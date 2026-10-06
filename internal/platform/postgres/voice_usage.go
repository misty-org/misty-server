package db

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/kannachi323/misty/server/internal/billingadapter"
)

// A crash leaves an active row and its durable billing hold. Missing provider
// usage requires reconciliation, never an automatic full charge or free release.
func (db *Database) RecordVoiceUsage(ctx context.Context, r *billingadapter.Reservation, state string, usage map[string]int64) error {
	if r == nil || r.Admission.Operation != "agent.voice.realtime" {
		return errors.New("invalid voice reservation")
	}
	raw, err := json.Marshal(usage)
	if err != nil {
		return err
	}
	result, err := db.Conn.ExecContext(ctx, `INSERT INTO voice_usage_journal(operation_id,account_id,reservation_id,state,usage)
VALUES($1,$2,$3,$4,$5::jsonb) ON CONFLICT(operation_id) DO UPDATE SET state=EXCLUDED.state,usage=EXCLUDED.usage,updated_at=now()
WHERE voice_usage_journal.account_id=EXCLUDED.account_id AND voice_usage_journal.reservation_id=EXCLUDED.reservation_id`,
		r.Admission.OperationID, r.Admission.AccountID, r.ID, state, string(raw))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return errors.New("voice reservation ownership mismatch")
	}
	return err
}

func (db *Database) RecoverVoiceUsageBatch(ctx context.Context) (int, error) {
	processed := 0
	result, err := db.Conn.ExecContext(ctx, `UPDATE voice_usage_journal SET state='reconcile',updated_at=now() WHERE state='active' AND updated_at<now()-interval '2 minutes'`)
	if err != nil {
		return processed, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	processed = int(n)
	rows, err := db.Conn.QueryContext(ctx, `SELECT r.reservation_id,r.admission,j.usage FROM voice_usage_journal j
JOIN billing_adapter_reservations r ON r.account_id=j.account_id AND r.reservation_id=j.reservation_id
WHERE j.state='settlement_pending' AND j.updated_at<now()-interval '30 seconds' LIMIT 20`)
	if err != nil {
		return processed, err
	}
	type pending struct {
		reservation billingadapter.Reservation
		usage       map[string]int64
	}
	var work []pending
	for rows.Next() {
		var item pending
		var admission, usage []byte
		if err = rows.Scan(&item.reservation.ID, &admission, &usage); err != nil {
			rows.Close()
			return processed, err
		}
		if json.Unmarshal(admission, &item.reservation.Admission) != nil || json.Unmarshal(usage, &item.usage) != nil {
			rows.Close()
			return processed, errors.New("invalid voice usage journal")
		}
		work = append(work, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return processed, err
	}
	for _, item := range work {
		action := "release"
		for _, value := range item.usage {
			if value > 0 {
				action = "settle"
				break
			}
		}
		u := billingadapter.Usage{Provider: item.reservation.Admission.Usage.Provider, Model: item.reservation.Admission.Usage.Model, Units: item.usage}
		if err = db.BillingService().Complete(ctx, action, &item.reservation, item.reservation.Admission.Key+":"+action, u, ""); err != nil {
			return processed, err
		}
		if err = db.RecordVoiceUsage(ctx, &item.reservation, "closed", item.usage); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}
