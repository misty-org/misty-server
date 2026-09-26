package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/kannachi323/misty/server/internal/billingadapter"
)

// BillingOutbox stores raw usage and opaque identifiers, never commercial policy.
type BillingOutbox struct{ Database *Database }

func (s BillingOutbox) Reservations(ctx context.Context, accountID, operationID string) ([]billingadapter.Reservation, error) {
	rows, err := s.Database.Conn.QueryContext(ctx, `SELECT reservation_id,admission FROM billing_adapter_reservations WHERE account_id=$1 AND admission->>'operation_id'=$2 ORDER BY key`, accountID, operationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []billingadapter.Reservation{}
	for rows.Next() {
		var r billingadapter.Reservation
		var raw []byte
		if err = rows.Scan(&r.ID, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &r.Admission); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s BillingOutbox) Enqueue(ctx context.Context, e billingadapter.Entry) error {
	raw, err := json.Marshal(e.Request)
	if err != nil {
		return err
	}
	result, err := s.Database.Conn.ExecContext(ctx, `INSERT INTO billing_adapter_outbox(id,action,payload) VALUES($1,$2,$3::jsonb)
 ON CONFLICT(id) DO UPDATE SET id=EXCLUDED.id WHERE billing_adapter_outbox.action=EXCLUDED.action AND billing_adapter_outbox.payload=EXCLUDED.payload`, e.ID, e.Action, string(raw))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("billing idempotency key reused with different usage")
	}
	return nil
}
func (s BillingOutbox) Pending(ctx context.Context, limit int) ([]billingadapter.Entry, error) {
	rows, err := s.Database.Conn.QueryContext(ctx, `SELECT id,action,payload FROM billing_adapter_outbox WHERE delivered_at IS NULL AND available_at<=now() ORDER BY created_at,id LIMIT $1`, min(max(limit, 1), 100))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []billingadapter.Entry{}
	for rows.Next() {
		var e billingadapter.Entry
		var raw []byte
		if err = rows.Scan(&e.ID, &e.Action, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &e.Request); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}
func (s BillingOutbox) Delivered(ctx context.Context, id string) error {
	_, err := s.Database.Conn.ExecContext(ctx, `UPDATE billing_adapter_outbox SET delivered_at=now() WHERE id=$1 AND delivered_at IS NULL`, id)
	return err
}
func (s BillingOutbox) Retry(ctx context.Context, id string, at time.Time) error {
	_, err := s.Database.Conn.ExecContext(ctx, `UPDATE billing_adapter_outbox SET available_at=$2,attempts=attempts+1 WHERE id=$1 AND delivered_at IS NULL`, id, at)
	return err
}

func (s BillingOutbox) Reservation(ctx context.Context, req billingadapter.Request) (*billingadapter.Reservation, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var id string
	var same bool
	err = s.Database.Conn.QueryRowContext(ctx, `SELECT reservation_id,admission=$3::jsonb FROM billing_adapter_reservations WHERE account_id=$1 AND key=$2`, req.AccountID, req.Key, string(raw)).Scan(&id, &same)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !same {
		return nil, errors.New("billing admission key reused for different work")
	}
	return &billingadapter.Reservation{ID: id, Admission: req}, nil
}
func (s BillingOutbox) SaveReservation(ctx context.Context, r billingadapter.Reservation) error {
	tx, err := s.Database.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.saveAdmittedReservation(ctx, tx, r); err != nil {
		return err
	}
	return tx.Commit()
}
