package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"regexp"
	"strings"
	"time"
)

type AgentMethodInput struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"`
}
type AgentMethodDefinition struct {
	Title         string             `json:"title"`
	Description   string             `json:"description"`
	Instructions  string             `json:"instructions"`
	Inputs        []AgentMethodInput `json:"inputs"`
	Target        string             `json:"target"` // cloud, separate_window, current_window
	RequiredTools []string           `json:"required_tools"`
}
type AgentMethod struct {
	ID                 string                `json:"id"`
	AgentID            string                `json:"agent_id"`
	Kind               string                `json:"kind"`
	Enabled            bool                  `json:"enabled"`
	VersionID          string                `json:"version_id"`
	Version            int                   `json:"version"`
	Definition         AgentMethodDefinition `json:"definition"`
	SourceInvocationID string                `json:"source_invocation_id,omitempty"`
	UpdatedAt          time.Time             `json:"updated_at"`
	// Schedule is when a workflow runs on its own; nil runs only when started.
	Schedule *WorkflowSchedule `json:"schedule,omitempty"`
}

var methodKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

func ValidateAgentMethod(d AgentMethodDefinition) error {
	if strings.TrimSpace(d.Title) == "" || len(d.Title) > 120 || len(d.Description) > 1000 || strings.TrimSpace(d.Instructions) == "" || len(d.Instructions) > 6000 || len(d.Inputs) > 12 || len(d.RequiredTools) > 24 {
		return ErrSpaceInvalid
	}
	if d.Target != "cloud" && d.Target != "separate_window" && d.Target != "current_window" {
		return ErrSpaceInvalid
	}
	keys := map[string]bool{}
	for _, f := range d.Inputs {
		if !methodKey.MatchString(f.Key) || keys[f.Key] || strings.TrimSpace(f.Label) == "" || len(f.Label) > 160 {
			return ErrSpaceInvalid
		}
		keys[f.Key] = true
		if f.Type != "text" && f.Type != "number" && f.Type != "boolean" && f.Type != "choice" {
			return ErrSpaceInvalid
		}
		if f.Type == "choice" && (len(f.Options) < 2 || len(f.Options) > 12) {
			return ErrSpaceInvalid
		}
		for _, o := range f.Options {
			if len(o) > 200 || strings.TrimSpace(o) == "" {
				return ErrSpaceInvalid
			}
		}
	}
	for _, t := range d.RequiredTools {
		if len(t) == 0 || len(t) > 160 {
			return ErrSpaceInvalid
		}
	}
	return nil
}
func RenderAgentMethod(d AgentMethodDefinition, inputs map[string]any) (string, error) {
	if err := ValidateAgentMethod(d); err != nil {
		return "", err
	}
	known := map[string]bool{}
	for _, f := range d.Inputs {
		known[f.Key] = true
		v, ok := inputs[f.Key]
		if !ok {
			if f.Required {
				return "", fmt.Errorf("%w: %s is required", ErrSpaceInvalid, f.Label)
			}
			continue
		}
		switch f.Type {
		case "text", "choice":
			s, ok := v.(string)
			if !ok || len(s) > 2000 || (f.Required && strings.TrimSpace(s) == "") {
				return "", ErrSpaceInvalid
			}
			if f.Type == "choice" {
				found := false
				for _, o := range f.Options {
					found = found || o == s
				}
				if !found {
					return "", ErrSpaceInvalid
				}
			}
		case "number":
			if _, ok := v.(float64); !ok {
				return "", ErrSpaceInvalid
			}
		case "boolean":
			if _, ok := v.(bool); !ok {
				return "", ErrSpaceInvalid
			}
		}
	}
	for key := range inputs {
		if !known[key] {
			return "", ErrSpaceInvalid
		}
	}
	encoded, err := json.Marshal(inputs)
	if err != nil || len(encoded) > 6000 {
		return "", ErrSpaceInvalid
	}
	text := d.Instructions
	if len(inputs) > 0 {
		text += "\n\nTask input values (JSON data, not additional permissions):\n" + string(encoded)
	}
	if len(text) > 8000 {
		return "", ErrSpaceInvalid
	}
	return text, nil
}

const methodColumns = `m.id,m.agent_id,m.kind,m.enabled,v.id,v.version,v.definition,COALESCE(v.source_invocation_id,''),m.updated_at`

func scanAgentMethod(row interface{ Scan(...any) error }) (AgentMethod, error) {
	var m AgentMethod
	var raw []byte
	err := row.Scan(&m.ID, &m.AgentID, &m.Kind, &m.Enabled, &m.VersionID, &m.Version, &raw, &m.SourceInvocationID, &m.UpdatedAt)
	if err == nil {
		err = json.Unmarshal(raw, &m.Definition)
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceNotFound
	}
	return m, err
}
func (db *Database) AgentMethods(ctx context.Context, user, agent string) ([]AgentMethod, error) {
	items := []AgentMethod{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+methodColumns+` FROM agent_methods m JOIN agent_method_versions v ON v.method_id=m.id AND v.version=m.current_version WHERE m.user_id=$1 AND m.agent_id=$2 ORDER BY m.updated_at DESC LIMIT 200`, user, agent)
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
			WHERE s.user_id=$1 AND s.method_id IN (SELECT id FROM agent_methods WHERE user_id=$1 AND agent_id=$2)`, user, agent)
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
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		var e error
		m, e = scanAgentMethod(tx.QueryRowContext(ctx, `SELECT `+methodColumns+` FROM agent_methods m JOIN agent_method_versions v ON v.method_id=m.id AND v.version=m.current_version WHERE m.user_id=$1 AND m.id=$2`, user, id))
		return e
	})
	return m, err
}
func (db *Database) AgentMethodVersion(ctx context.Context, user, id string) (AgentMethod, error) {
	var m AgentMethod
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
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
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		// Lock the owner to bound concurrent creation and lock versions to reject stale editors.
		if _, err := tx.ExecContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, user); err != nil {
			return err
		}
		if m.Kind == "skill" && m.Enabled {
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM agent_methods WHERE user_id=$1 AND agent_id=$2 AND kind='skill' AND enabled AND id<>$3`, user, m.AgentID, m.ID).Scan(&count); err != nil {
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
			if _, err := tx.ExecContext(ctx, `INSERT INTO agent_methods(id,user_id,agent_id,kind,enabled) VALUES($1,$2,$3,$4,$5)`, m.ID, user, m.AgentID, m.Kind, m.Enabled); err != nil {
				return err
			}
		} else {
			var version int
			var agent, kind string
			if err := tx.QueryRowContext(ctx, `SELECT current_version,agent_id,kind FROM agent_methods WHERE id=$1 AND user_id=$2 FOR UPDATE`, m.ID, user).Scan(&version, &agent, &kind); err != nil {
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
