package db

import (
	"context"
	"database/sql"
	"time"
)

type ProviderGatewayState struct {
	Provider        string     `json:"provider"`
	SessionID       string     `json:"session_id"`
	ResumeURL       string     `json:"resume_url"`
	Sequence        int64      `json:"sequence"`
	LastHeartbeatAt *time.Time `json:"last_heartbeat_at,omitempty"`
	LastEventAt     *time.Time `json:"last_event_at,omitempty"`
	Status          string     `json:"status"`
	LastErrorCode   string     `json:"last_error_code,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (db *Database) ProviderGatewayState(ctx context.Context, provider string) (*ProviderGatewayState, error) {
	out := &ProviderGatewayState{Provider: provider, Status: "disconnected"}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT provider,session_id,resume_url,sequence,last_heartbeat_at,last_event_at,status,last_error_code,updated_at
			FROM provider_gateway_state WHERE provider=$1`, provider).Scan(&out.Provider, &out.SessionID, &out.ResumeURL, &out.Sequence, &out.LastHeartbeatAt, &out.LastEventAt, &out.Status, &out.LastErrorCode, &out.UpdatedAt)
	})
	if err == sql.ErrNoRows {
		return out, nil
	}
	return out, err
}

func (db *Database) SaveProviderGatewayState(ctx context.Context, item ProviderGatewayState, heartbeat, event bool) error {
	if item.Provider != "discord" || item.Status != "connected" && item.Status != "degraded" && item.Status != "disconnected" {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO provider_gateway_state(provider,session_id,resume_url,sequence,last_heartbeat_at,last_event_at,status,last_error_code)
			VALUES($1,$2,$3,$4,CASE WHEN $5 THEN NOW() END,CASE WHEN $6 THEN NOW() END,$7,$8)
			ON CONFLICT(provider) DO UPDATE SET session_id=EXCLUDED.session_id,resume_url=EXCLUDED.resume_url,sequence=EXCLUDED.sequence,
			last_heartbeat_at=CASE WHEN $5 THEN NOW() ELSE provider_gateway_state.last_heartbeat_at END,
			last_event_at=CASE WHEN $6 THEN NOW() ELSE provider_gateway_state.last_event_at END,status=EXCLUDED.status,last_error_code=EXCLUDED.last_error_code,updated_at=NOW()`, item.Provider, item.SessionID, item.ResumeURL, item.Sequence, heartbeat, event, item.Status, item.LastErrorCode)
		return err
	})
}

func (db *Database) SetProviderSharedResourceHealth(ctx context.Context, resourceID, status, errorCode string) error {
	if status != "active" && status != "needs_attention" && status != "disabled" {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE provider_shared_resources SET status=$1,last_error_code=$2,updated_at=NOW() WHERE id=$3`, status, errorCode, resourceID)
		return err
	})
}
