package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type AgentActivity struct {
	ID        int64     `json:"id"`
	UserID    string    `json:"user_id"`
	AgentID   string    `json:"agent_id"`
	Name      string    `json:"name"`
	State     string    `json:"state"`
	Doing     string    `json:"doing"`
	CreatedAt time.Time `json:"created_at"`
}

const AgentActivityEvent = "agent.activity"

func (db *Database) RecordAgentActivity(ctx context.Context, userID, spaceID, agentID, name, state, doing string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, AgentActivityEvent, agentID,
			map[string]any{"name": name, "state": state, "doing": doing})
		return err
	})
}

func (db *Database) ListAgentActivity(ctx context.Context, userID, spaceID string, limit int) ([]AgentActivity, error) {
	if limit < 1 || limit > 200 {
		limit = 200
	}
	lines := []AgentActivity{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,COALESCE(actor_user_id::text,''),COALESCE(entity_id,''),payload,created_at
			FROM space_events WHERE space_id=$1 AND event_type=$2 ORDER BY id DESC LIMIT $3`,
			spaceID, AgentActivityEvent, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var line AgentActivity
			var raw []byte
			if err := rows.Scan(&line.ID, &line.UserID, &line.AgentID, &raw, &line.CreatedAt); err != nil {
				return err
			}
			var payload struct {
				Name  string `json:"name"`
				State string `json:"state"`
				Doing string `json:"doing"`
			}
			if json.Unmarshal(raw, &payload) != nil {
				continue
			}
			line.Name, line.State, line.Doing = payload.Name, payload.State, payload.Doing
			lines = append(lines, line)
		}
		return rows.Err()
	})
	return lines, err
}
