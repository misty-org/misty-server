package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// The live checklist: the concrete steps one run is working through, in every
// chat. A delegated run's checklist nests under its parent's.

const (
	MaxChecklistItems      = 20
	maxChecklistTitleRunes = 160
	maxChecklistNoteRunes  = 500
)

var checklistStatuses = map[string]bool{"pending": true, "in_progress": true, "done": true, "skipped": true, "blocked": true}

// ChecklistItem is one step. Delegated steps are handed to another agent or a
// coding tool; that run's own checklist nests under this step.
type ChecklistItem struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Note      string `json:"note,omitempty"`
	Delegated bool   `json:"delegated,omitempty"`
}

// AgentChecklist is one run's checklist.
type AgentChecklist struct {
	InvocationID       string          `json:"invocationId"`
	ConversationID     string          `json:"conversationId"`
	ParentInvocationID string          `json:"parentInvocationId,omitempty"`
	Items              []ChecklistItem `json:"items"`
	Version            int             `json:"version"`
	UpdatedAt          time.Time       `json:"updatedAt"`
}

const agentChecklistColumns = `invocation_id,conversation_id,COALESCE(parent_invocation_id,''),items,version,updated_at`

func scanAgentChecklist(row scanner, out *AgentChecklist) error {
	var items []byte
	if err := row.Scan(&out.InvocationID, &out.ConversationID, &out.ParentInvocationID, &items, &out.Version, &out.UpdatedAt); err != nil {
		return err
	}
	return json.Unmarshal(items, &out.Items)
}

// mergeChecklist applies an update: replace swaps the whole list; otherwise
// known ids change in place and new ids are appended in order.
func mergeChecklist(current, updates []ChecklistItem, replace bool) ([]ChecklistItem, error) {
	next := []ChecklistItem{}
	if !replace {
		next = append(next, current...)
	}
	index := map[string]int{}
	for position, item := range next {
		index[item.ID] = position
	}
	for _, update := range updates {
		update.ID, update.Title, update.Note = strings.TrimSpace(update.ID), strings.TrimSpace(update.Title), strings.TrimSpace(update.Note)
		if update.ID == "" || len(update.ID) > 40 || !checklistStatuses[update.Status] ||
			len([]rune(update.Title)) > maxChecklistTitleRunes || len([]rune(update.Note)) > maxChecklistNoteRunes {
			return nil, ErrSpaceInvalid
		}
		if position, ok := index[update.ID]; ok {
			if update.Title == "" {
				update.Title = next[position].Title
			}
			update.Delegated = update.Delegated || next[position].Delegated
			next[position] = update
			continue
		}
		if update.Title == "" {
			return nil, ErrSpaceInvalid
		}
		index[update.ID] = len(next)
		next = append(next, update)
	}
	if len(next) == 0 || len(next) > MaxChecklistItems {
		return nil, ErrSpaceInvalid
	}
	return next, nil
}

// SaveAgentChecklist applies an update to a run's checklist, seeding it from
// seed (an approved plan's steps) when the run has none yet. Every save bumps
// the version so clients drop stale copies.
func (db *Database) SaveAgentChecklist(ctx context.Context, user, invocationID, conversationID, parentID string, seed, updates []ChecklistItem, replace bool) (*AgentChecklist, error) {
	if user == "" || invocationID == "" || conversationID == "" {
		return nil, ErrSpaceInvalid
	}
	out := &AgentChecklist{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		current := &AgentChecklist{}
		err := scanAgentChecklist(tx.QueryRowContext(ctx, `SELECT `+agentChecklistColumns+` FROM agent_checklists
			WHERE invocation_id=$1 AND owner_user_id=$2 FOR UPDATE`, invocationID, user), current)
		if errors.Is(err, sql.ErrNoRows) {
			current.Items = seed
		} else if err != nil {
			return err
		}
		items, err := mergeChecklist(current.Items, updates, replace)
		if err != nil {
			return err
		}
		encoded, _ := json.Marshal(items)
		return scanAgentChecklist(tx.QueryRowContext(ctx, `INSERT INTO agent_checklists(invocation_id,owner_user_id,conversation_id,parent_invocation_id,items)
			VALUES($1,$2,$3,NULLIF($4,''),$5)
			ON CONFLICT (invocation_id) DO UPDATE SET items=EXCLUDED.items,version=agent_checklists.version+1,updated_at=now()
			WHERE agent_checklists.owner_user_id=EXCLUDED.owner_user_id
			RETURNING `+agentChecklistColumns, invocationID, user, conversationID, parentID, encoded), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

// ConversationChecklists is the conversation's latest top-level checklist and
// the checklists of the runs it delegated to, parent first.
func (db *Database) ConversationChecklists(ctx context.Context, user, conversationID string) ([]AgentChecklist, error) {
	items := []AgentChecklist{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `WITH latest AS (
				SELECT invocation_id FROM agent_checklists WHERE owner_user_id=$1 AND conversation_id=$2 AND parent_invocation_id IS NULL
				ORDER BY updated_at DESC LIMIT 1)
			SELECT `+agentChecklistColumns+` FROM agent_checklists
			WHERE owner_user_id=$1 AND (invocation_id IN (SELECT invocation_id FROM latest) OR parent_invocation_id IN (SELECT invocation_id FROM latest))
			ORDER BY parent_invocation_id NULLS FIRST, updated_at`, user, conversationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentChecklist
			if err := scanAgentChecklist(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}
