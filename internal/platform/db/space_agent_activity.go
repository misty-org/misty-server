package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// AgentActivity is one line of a Space's activity log: a member's agent changed what it is doing.
// It holds the agent's name, which its owner wrote, and fixed codes for its state and the kind of
// work. Nothing a model wrote is ever part of it: no prompt, no tool result, no answer.
type AgentActivity struct {
	ID        int64     `json:"id"`
	UserID    string    `json:"user_id"`
	AgentID   string    `json:"agent_id"`
	Name      string    `json:"name"`
	State     string    `json:"state"`
	Doing     string    `json:"doing"`
	CreatedAt time.Time `json:"created_at"`
}

// AgentActivityEvent is the Space event type an activity line is stored under.
const AgentActivityEvent = "agent.activity"

// RecordAgentActivity appends a line to the Space's activity log and tells its members.
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

// ListAgentActivity returns the Space's most recent activity lines, newest first.
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
