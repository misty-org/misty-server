package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
)

const methodColumns = `m.id,m.space_id,m.agent_id,m.kind,m.enabled,v.id,v.version,v.definition,COALESCE(v.source_invocation_id,''),m.updated_at`

func scanAgentMethod(row interface{ Scan(...any) error }) (AgentMethod, error) {
	var m AgentMethod
	var raw []byte
	err := row.Scan(&m.ID, &m.SpaceID, &m.AgentID, &m.Kind, &m.Enabled, &m.VersionID, &m.Version, &raw, &m.SourceInvocationID, &m.UpdatedAt)
	if err == nil {
		err = json.Unmarshal(raw, &m.Definition)
	}
	// Workflows saved for the retired separate agent window run in this window.
	if m.Definition.Target == "separate_window" {
		m.Definition.Target = "current_window"
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceNotFound
	}
	return m, err
}

// AgentMethods lists an agent's workflows, templates and skills in one Space
// (empty: the personal Space).
func (db *Database) AgentMethods(ctx context.Context, user, space, agent string) ([]AgentMethod, error) {
	items := []AgentMethod{}
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		space, err := memorySpaceTx(ctx, tx, user, space)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+methodColumns+` FROM agent_methods m JOIN agent_method_versions v ON v.method_id=m.id AND v.version=m.current_version WHERE m.user_id=$1 AND m.space_id=$3 AND m.agent_id=$2 ORDER BY m.updated_at DESC LIMIT 200`, user, agent, space)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			m, e := scanAgentMethod(rows)
			if e != nil {
				return e
			}
			items = append(items, m)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		schedules, err := queryWorkflowSchedules(ctx, tx, `SELECT `+workflowScheduleColumns+` FROM workflow_schedules s
			WHERE s.user_id=$1 AND s.method_id IN (SELECT id FROM agent_methods WHERE user_id=$1 AND space_id=$3 AND agent_id=$2)`, user, agent, space)
		if err != nil {
			return err
		}
		for i := range items {
			for j := range schedules {
				if schedules[j].MethodID == items[i].ID {
					items[i].Schedule = &schedules[j]
				}
			}
		}
		return nil
	})
	return items, err
}

// AgentMethodByID loads a method's latest version.
func (db *Database) AgentMethodByID(ctx context.Context, user, id string) (AgentMethod, error) {
	var m AgentMethod
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		var e error
		m, e = scanAgentMethod(tx.QueryRowContext(ctx, `SELECT `+methodColumns+` FROM agent_methods m JOIN agent_method_versions v ON v.method_id=m.id AND v.version=m.current_version WHERE m.user_id=$1 AND m.id=$2`, user, id))
		return e
	})
	return m, err
}
func (db *Database) AgentMethodVersion(ctx context.Context, user, id string) (AgentMethod, error) {
	var m AgentMethod
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		var e error
		m, e = scanAgentMethod(tx.QueryRowContext(ctx, `SELECT `+methodColumns+` FROM agent_methods m JOIN agent_method_versions v ON v.method_id=m.id WHERE m.user_id=$1 AND v.id=$2`, user, id))
		return e
	})
	return m, err
}
func (db *Database) SaveAgentMethod(ctx context.Context, user string, m AgentMethod, expected int) (AgentMethod, error) {
	if err := ValidateAgentMethod(m.Definition); err != nil {
		return m, err
	}
	if m.Kind != "workflow" && m.Kind != "template" && m.Kind != "skill" {
		return m, ErrSpaceInvalid
	}
	if m.Kind == "skill" && (len(m.Definition.Inputs) > 0 || m.Definition.Target != "cloud") {
		return m, ErrSpaceInvalid
	}
	if _, err := db.AskIdentityByID(ctx, user, m.AgentID); err != nil {
		return m, err
	}
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		// Lock the owner to bound concurrent creation and lock versions to reject stale editors.
		if _, err := tx.ExecContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, user); err != nil {
			return err
		}
		// A workflow stays in the Space it was saved in; an edit that names no
		// Space keeps it there, and a new one without a Space is personal.
		requested := m.SpaceID
		if m.ID != "" {
			if err := tx.QueryRowContext(ctx, `SELECT space_id FROM agent_methods WHERE id=$1 AND user_id=$2`, m.ID, user).Scan(&m.SpaceID); err != nil || requested != "" && requested != m.SpaceID {
				return ErrSpaceNotFound
			}
		} else {
			var err error
			if m.SpaceID, err = memorySpaceTx(ctx, tx, user, m.SpaceID); err != nil {
				return err
			}
		}
		if m.Kind == "skill" && m.Enabled {
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM agent_methods WHERE user_id=$1 AND space_id=$4 AND agent_id=$2 AND kind='skill' AND enabled AND id<>$3`, user, m.AgentID, m.ID, m.SpaceID).Scan(&count); err != nil {
				return err
			}
			if count >= 8 {
				return ErrSpaceConflict
			}
		}
		if m.SourceInvocationID != "" {
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_invocations i JOIN misty_ask_conversations c ON c.id=i.conversation_id WHERE i.id=$1 AND i.user_id=$2 AND c.agent_id=$3 AND i.state='completed' AND EXISTS(SELECT 1 FROM ai_invocation_events e WHERE e.invocation_id=i.id AND e.event_type='assistant.message' AND COALESCE(e.payload->>'text','')<>''))`, m.SourceInvocationID, user, m.AgentID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrSpaceNotFound
			}
		}
		if m.ID == "" {
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM agent_methods WHERE user_id=$1`, user).Scan(&count); err != nil {
				return err
			}
			if count >= 200 {
				return ErrSpaceConflict
			}
			m.ID = "method_" + uuid.NewString()
			m.Version = 1
			if _, err := tx.ExecContext(ctx, `INSERT INTO agent_methods(id,user_id,space_id,agent_id,kind,enabled) VALUES($1,$2,$6,$3,$4,$5)`, m.ID, user, m.AgentID, m.Kind, m.Enabled, m.SpaceID); err != nil {
				return err
			}
		} else {
			var version int
			var agent, kind, space string
			if err := tx.QueryRowContext(ctx, `SELECT current_version,agent_id,kind,space_id FROM agent_methods WHERE id=$1 AND user_id=$2 FOR UPDATE`, m.ID, user).Scan(&version, &agent, &kind, &space); err != nil {
				return ErrSpaceNotFound
			}
			if space != m.SpaceID {
				return ErrSpaceNotFound
			}
			if version != expected || agent != m.AgentID || kind != m.Kind {
				return ErrSpaceConflict
			}
			m.Version = version + 1
		}
		m.VersionID = "method_version_" + uuid.NewString()
		raw, _ := json.Marshal(m.Definition)
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_method_versions(id,method_id,user_id,version,definition,source_invocation_id) VALUES($1,$2,$3,$4,$5,NULLIF($6,''))`, m.VersionID, m.ID, user, m.Version, raw, m.SourceInvocationID); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `UPDATE agent_methods SET current_version=$3,enabled=$4,updated_at=now() WHERE id=$1 AND user_id=$2 RETURNING updated_at`, m.ID, user, m.Version, m.Enabled).Scan(&m.UpdatedAt)
	})
	return m, err
}
