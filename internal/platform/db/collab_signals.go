package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const (
	CollabSignalSessionEnded       = "collab.session_ended"
	CollabSignalIntegrationEnded   = "collab.integration_ended"
	CollabSignalIntegrationChanged = "collab.integration_changed"
)

func (db *Database) SignalCollabSessionEnded(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.New("session id required")
	}
	return db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		return signalCollabSessionEndedTx(ctx, tx, sessionID)
	})
}

func (db *Database) SignalCollabIntegrationEnded(ctx context.Context, integrationID string) error {
	if integrationID == "" {
		return errors.New("integration id required")
	}
	return db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		return signalCollabIntegrationEndedTx(ctx, tx, integrationID)
	})
}

func signalCollabSessionEndedTx(ctx context.Context, tx *sql.Tx, sessionID string) error {
	return emitCollabSignalTx(ctx, tx, map[string]string{"kind": CollabSignalSessionEnded, "session_id": sessionID})
}

func signalCollabIntegrationChangedTx(ctx context.Context, tx *sql.Tx, integrationID string) error {
	return emitCollabSignalTx(ctx, tx, map[string]string{"kind": CollabSignalIntegrationChanged, "integration_id": integrationID})
}

func signalCollabIntegrationEndedTx(ctx context.Context, tx *sql.Tx, integrationID string) error {
	return emitCollabSignalTx(ctx, tx, map[string]string{"kind": CollabSignalIntegrationEnded, "integration_id": integrationID})
}

func emitCollabSignalTx(ctx context.Context, tx *sql.Tx, payload map[string]string) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `SELECT pg_notify('misty_space_control',$1)`, string(raw))
	return err
}
