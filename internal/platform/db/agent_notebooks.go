package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// AgentNotebook is a run's plan and findings (docs/agents/harness.md, Sub-agents and the notebook).
// A conversation and every sub-agent it starts share one notebook.
type AgentNotebook struct {
	Key       string    `json:"key"`
	NoteID    string    `json:"note_id,omitempty"`
	Plan      string    `json:"plan"`
	Findings  string    `json:"findings"`
	UpdatedAt time.Time `json:"updated_at"`
}

const (
	MaxNotebookPlan     = 20_000
	MaxNotebookFindings = 100_000
)

var ErrNotebookFull = errors.New("the notebook section is full; replace it with a shorter version")

func (db *Database) AgentNotebook(ctx context.Context, user, key string) (*AgentNotebook, error) {
	var notebook AgentNotebook
	var note sql.NullString
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT notebook_key,note_id,plan,findings,updated_at FROM agent_notebooks WHERE owner_user_id=$1 AND notebook_key=$2`, user, key).
			Scan(&notebook.Key, &note, &notebook.Plan, &notebook.Findings, &notebook.UpdatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return &AgentNotebook{Key: key}, nil
	}
	if err != nil {
		return nil, err
	}
	notebook.NoteID = note.String
	return &notebook, nil
}

// WriteAgentNotebook replaces or appends to one section, atomically.
func (db *Database) WriteAgentNotebook(ctx context.Context, user, key, section, content string, appendText bool) (*AgentNotebook, error) {
	if user == "" || key == "" || len(key) > 200 || (section != "plan" && section != "findings") {
		return nil, ErrSpaceInvalid
	}
	limit := MaxNotebookFindings
	if section == "plan" {
		limit = MaxNotebookPlan
	}
	var notebook AgentNotebook
	var note sql.NullString
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_notebooks(owner_user_id,notebook_key) VALUES($1,$2) ON CONFLICT DO NOTHING`, user, key); err != nil {
			return err
		}
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT `+section+` FROM agent_notebooks WHERE owner_user_id=$1 AND notebook_key=$2 FOR UPDATE`, user, key).Scan(&current); err != nil {
			return err
		}
		next := strings.TrimSpace(content)
		if appendText && strings.TrimSpace(current) != "" {
			next = strings.TrimRight(current, "\n") + "\n" + next
		}
		if len(next) > limit {
			return ErrNotebookFull
		}
		return tx.QueryRowContext(ctx, `UPDATE agent_notebooks SET `+section+`=$3,updated_at=now() WHERE owner_user_id=$1 AND notebook_key=$2
 RETURNING notebook_key,note_id,plan,findings,updated_at`, user, key, next).Scan(&notebook.Key, &note, &notebook.Plan, &notebook.Findings, &notebook.UpdatedAt)
	})
	if err != nil {
		return nil, err
	}
	notebook.NoteID = note.String
	return &notebook, nil
}

func (db *Database) SetAgentNotebookNote(ctx context.Context, user, key, noteID string) error {
	return db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE agent_notebooks SET note_id=$3 WHERE owner_user_id=$1 AND notebook_key=$2`, user, key, noteID)
		return err
	})
}

// DelegationChild is one sub-agent task and its invocation's progress.
type DelegationChild struct {
	CallID            string          `json:"-"`
	TaskIndex         int             `json:"index"`
	Goal              string          `json:"goal"`
	OutputSchema      json.RawMessage `json:"-"`
	ChildInvocationID string          `json:"invocation_id"`
	State             string          `json:"state"`
	Answer            string          `json:"answer,omitempty"`
	Error             string          `json:"error,omitempty"`
}

func (db *Database) InsertDelegationChild(ctx context.Context, user, parent string, child DelegationChild) error {
	var schema any
	if len(child.OutputSchema) > 0 {
		schema = []byte(child.OutputSchema)
	}
	return db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO agent_delegation_children(owner_user_id,parent_invocation_id,call_id,task_index,goal,output_schema,child_invocation_id)
 VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, user, parent, child.CallID, child.TaskIndex, child.Goal, schema, child.ChildInvocationID)
		return err
	})
}

// DelegationChildren lists a parent's sub-agents, optionally for one call,
// with each child's state, final answer or error.
func (db *Database) DelegationChildren(ctx context.Context, user, parent, callID string) ([]DelegationChild, error) {
	out := []DelegationChild{}
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT c.call_id,c.task_index,c.goal,c.output_schema,c.child_invocation_id,COALESCE(i.state,'missing'),COALESCE(i.error_code,''),
 COALESCE((SELECT e.payload->>'text' FROM ai_invocation_events e WHERE e.invocation_id=c.child_invocation_id AND e.event_type='assistant.message' ORDER BY e.sequence DESC LIMIT 1),'')
 FROM agent_delegation_children c LEFT JOIN ai_invocations i ON i.id=c.child_invocation_id AND i.user_id=c.owner_user_id
 WHERE c.owner_user_id=$1 AND c.parent_invocation_id=$2 AND ($3='' OR c.call_id=$3) ORDER BY c.created_at,c.task_index`, user, parent, callID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var child DelegationChild
			var schema []byte
			if err := rows.Scan(&child.CallID, &child.TaskIndex, &child.Goal, &schema, &child.ChildInvocationID, &child.State, &child.Error, &child.Answer); err != nil {
				return err
			}
			child.OutputSchema = schema
			out = append(out, child)
		}
		return rows.Err()
	})
	return out, err
}

// DelegationParent returns the invocation that started a sub-agent, or "".
func (db *Database) DelegationParent(ctx context.Context, user, child string) (string, error) {
	var parent string
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT parent_invocation_id FROM agent_delegation_children WHERE owner_user_id=$1 AND child_invocation_id=$2`, user, child).Scan(&parent)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return parent, err
}

// ConversationTitle is a conversation's title, or "" when it has none.
func (db *Database) ConversationTitle(ctx context.Context, user, conversationID string) string {
	var title sql.NullString
	_ = db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT title FROM misty_ask_conversations WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, conversationID, user).Scan(&title)
	})
	return title.String
}
