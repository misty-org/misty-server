package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/kannachi323/misty/server/internal/billingadapter"
)

func (s BillingOutbox) BeginAdmission(ctx context.Context, r billingadapter.Request) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	result, err := s.Database.Conn.ExecContext(ctx, `INSERT INTO billing_adapter_intents(account_id,key,admission) VALUES($1,$2,$3::jsonb)
 ON CONFLICT(account_id,key) DO UPDATE SET key=EXCLUDED.key
 WHERE billing_adapter_intents.admission=EXCLUDED.admission AND billing_adapter_intents.state='pending' AND billing_adapter_intents.expires_at>now()`, r.AccountID, r.Key, string(raw))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return errors.New("billing admission expired or conflicted; start new work")
	}
	return err
}
func (s BillingOutbox) AbandonedAdmissions(ctx context.Context, limit int) ([]billingadapter.Request, error) {
	rows, err := s.Database.Conn.QueryContext(ctx, `WITH expired AS (
 SELECT account_id,key FROM billing_adapter_intents WHERE state IN ('pending','abandoned') AND expires_at<=now() ORDER BY expires_at LIMIT $1 FOR UPDATE SKIP LOCKED
 ) UPDATE billing_adapter_intents i SET state='abandoned',expires_at=now()+interval '1 minute' FROM expired e
 WHERE i.account_id=e.account_id AND i.key=e.key RETURNING i.admission`, min(max(limit, 1), 100))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var requests []billingadapter.Request
	for rows.Next() {
		var raw []byte
		var r billingadapter.Request
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		requests = append(requests, r)
	}
	return requests, rows.Err()
}
func (s BillingOutbox) AdmissionRecovered(ctx context.Context, r billingadapter.Request) error {
	_, err := s.Database.Conn.ExecContext(ctx, `UPDATE billing_adapter_intents SET state='recovered' WHERE account_id=$1 AND key=$2 AND state='abandoned'`, r.AccountID, r.Key)
	return err
}
func (s BillingOutbox) saveAdmittedReservation(ctx context.Context, tx *sql.Tx, r billingadapter.Reservation) error {
	request := r.Admission
	result, err := tx.ExecContext(ctx, `UPDATE billing_adapter_intents SET state='admitted' WHERE account_id=$1 AND key=$2 AND (state='admitted' OR (state='pending' AND expires_at>now()))`, request.AccountID, request.Key)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("billing admission abandoned before provider execution")
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO billing_adapter_reservations(account_id,key,reservation_id,admission) VALUES($1,$2,$3,$4::jsonb)
 ON CONFLICT(account_id,key) DO UPDATE SET key=EXCLUDED.key WHERE billing_adapter_reservations.reservation_id=EXCLUDED.reservation_id AND billing_adapter_reservations.admission=EXCLUDED.admission`, request.AccountID, request.Key, r.ID, string(raw))
	if err != nil {
		return err
	}
	n, err = result.RowsAffected()
	if err == nil && n != 1 {
		return errors.New("billing admission conflict")
	}
	return err
}
