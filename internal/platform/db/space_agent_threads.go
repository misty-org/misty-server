package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxListedAgentThreads   = 200
	maxAgentThreadEntries   = 200
	maxAgentThreadEntryText = 20000
	maxAgentThreadTitle     = 200
	maxAgentThreadEmoji     = 32
)

var agentThreadStates = map[string]bool{"working": true, "needs-you": true, "landing": true, "resting": true}

var agentThreadKinds = map[string]bool{"ask": true, "reply": true, "step": true, "output": true}

type AgentThread struct {
	ID            string    `json:"id"`
	SpaceID       string    `json:"space_id"`
	AgentID       string    `json:"agent_id"`
	OwnerUserID   string    `json:"owner_user_id"`
	OwnerName     string    `json:"owner_name"`
	Title         string    `json:"title"`
	State         string    `json:"state"`
	ContinuedFrom *string   `json:"continued_from"`
	Entries       int       `json:"entries"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type AgentThreadReaction struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	Mine  bool   `json:"mine"`
}

type AgentThreadEntry struct {
	Key       string                `json:"key"`
	Seq       int64                 `json:"seq"`
	Kind      string                `json:"kind"`
	Body      string                `json:"body"`
	CreatedAt time.Time             `json:"created_at"`
	Reactions []AgentThreadReaction `json:"reactions"`
}

type AgentThreadInput struct {
	UserID        string
	SpaceID       string
	ThreadID      string
	AgentID       string
	Title         string
	State         string
	ContinuedFrom *string
}

type AgentThreadEntryInput struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
	Body string `json:"body"`
}

const agentThreadColumns = `t.id,t.space_id,t.agent_id,t.owner_user_id,COALESCE(u.username,''),t.title,t.state,
	t.continued_from,(SELECT count(*) FROM space_agent_thread_entries e WHERE e.thread_id=t.id),t.created_at,t.updated_at
	FROM space_agent_threads t LEFT JOIN users u ON u.id=t.owner_user_id`

func scanAgentThread(row interface{ Scan(...any) error }, out *AgentThread) error {
	var continued sql.NullString
	if err := row.Scan(&out.ID, &out.SpaceID, &out.AgentID, &out.OwnerUserID, &out.OwnerName, &out.Title, &out.State,
		&continued, &out.Entries, &out.CreatedAt, &out.UpdatedAt); err != nil {
		return err
	}
	if continued.Valid {
		value := continued.String
		out.ContinuedFrom = &value
	}
	return nil
}

func validAgentThreadID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == ':') {
			return false
		}
	}
	return true
}

func validAgentThreadKey(key string) bool {
	return key != "" && len(key) <= 160 && !strings.ContainsAny(key, "\x00\n\r")
}

func requireSharedSpaceTx(ctx context.Context, tx *sql.Tx, spaceID, userID string) (*memberAccess, error) {
	access, err := memberAccessTx(ctx, tx, spaceID, userID)
	if err != nil {
		return nil, err
	}
	if err := requireShareableSpaceTx(ctx, tx, spaceID); err != nil {
		if errors.Is(err, ErrSpaceNotShareable) {
			return nil, ErrSpaceForbidden
		}
		return nil, err
	}
	return access, nil
}

const agentVisibleSQL = `EXISTS(SELECT 1 FROM space_agents a WHERE a.space_id=t.space_id AND a.agent_id=t.agent_id
	AND (a.visibility='public' OR a.owner_user_id=$%d))`

func agentVisibleTx(ctx context.Context, tx *sql.Tx, userID, spaceID, agentID string) (bool, error) {
	var visible bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_agents WHERE space_id=$1 AND agent_id=$2
		AND (visibility='public' OR owner_user_id=$3))`, spaceID, agentID, userID).Scan(&visible)
	return visible, err
}

func visibleAgentThreadTx(ctx context.Context, tx *sql.Tx, userID, spaceID, threadID string) (*AgentThread, error) {
	thread, err := agentThreadTx(ctx, tx, spaceID, threadID)
	if err != nil || thread == nil {
		return nil, err
	}
	visible, err := agentVisibleTx(ctx, tx, userID, spaceID, thread.AgentID)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, nil
	}
	return thread, nil
}

func agentThreadTx(ctx context.Context, tx *sql.Tx, spaceID, threadID string) (*AgentThread, error) {
	var thread AgentThread
	err := scanAgentThread(tx.QueryRowContext(ctx, `SELECT `+agentThreadColumns+` WHERE t.id=$1 AND t.space_id=$2`,
		threadID, spaceID), &thread)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &thread, nil
}

func agentThreadEventPayload(thread *AgentThread) map[string]any {
	return map[string]any{
		"thread_id":     thread.ID,
		"agent_id":      thread.AgentID,
		"owner_user_id": thread.OwnerUserID,
		"state":         thread.State,
	}
}

func (db *Database) PutAgentThread(ctx context.Context, in AgentThreadInput) (*AgentThread, error) {
	title := strings.TrimSpace(in.Title)
	if !validAgentThreadID(in.ThreadID) || in.AgentID == "" || len(in.AgentID) > maxSpaceAgentIDLength ||
		utf8.RuneCountInString(title) > maxAgentThreadTitle || !agentThreadStates[in.State] ||
		(in.ContinuedFrom != nil && !validAgentThreadID(*in.ContinuedFrom)) {
		return nil, ErrSpaceInvalid
	}
	var out *AgentThread
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSharedSpaceTx(ctx, tx, in.SpaceID, in.UserID); err != nil {
			return err
		}
		visible, err := agentVisibleTx(ctx, tx, in.UserID, in.SpaceID, in.AgentID)
		if err != nil {
			return err
		}
		if !visible {
			return ErrSpaceForbidden
		}
		var owner, spaceID string
		err = tx.QueryRowContext(ctx, `SELECT owner_user_id,space_id FROM space_agent_threads WHERE id=$1 FOR UPDATE`,
			in.ThreadID).Scan(&owner, &spaceID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_agent_threads(id,space_id,agent_id,owner_user_id,title,state,continued_from)
				VALUES($1,$2,$3,$4,$5,$6,$7)`, in.ThreadID, in.SpaceID, in.AgentID, in.UserID, title, in.State,
				nullableString(in.ContinuedFrom)); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			if owner != in.UserID || spaceID != in.SpaceID {
				return ErrSpaceForbidden
			}
			result, err := tx.ExecContext(ctx, `UPDATE space_agent_threads SET title=$2,state=$3,updated_at=now()
				WHERE id=$1 AND (title<>$2 OR state<>$3)`, in.ThreadID, title, in.State)
			if err != nil {
				return err
			}
			if changed, _ := result.RowsAffected(); changed == 0 {
				thread, err := agentThreadTx(ctx, tx, in.SpaceID, in.ThreadID)
				if err != nil {
					return err
				}
				out = thread
				return nil
			}
		}
		thread, err := agentThreadTx(ctx, tx, in.SpaceID, in.ThreadID)
		if err != nil {
			return err
		}
		if thread == nil {
			return ErrSpaceNotFound
		}
		out = thread
		_, err = recordSpaceEventTx(ctx, tx, in.SpaceID, in.UserID, "agent_thread.updated", in.ThreadID,
			agentThreadEventPayload(thread))
		return err
	})
	return out, err
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func (db *Database) AgentThreads(ctx context.Context, userID, spaceID, agentID string) ([]AgentThread, error) {
	items := []AgentThread{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSharedSpaceTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+agentThreadColumns+`
			WHERE t.space_id=$1 AND ($2='' OR t.agent_id=$2) AND `+fmt.Sprintf(agentVisibleSQL, 4)+`
			ORDER BY t.updated_at DESC,t.id LIMIT $3`,
			spaceID, agentID, maxListedAgentThreads, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentThread
			if err := scanAgentThread(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) PutAgentThreadEntries(ctx context.Context, userID, spaceID, threadID string, entries []AgentThreadEntryInput) (*AgentThread, error) {
	if !validAgentThreadID(threadID) || len(entries) == 0 || len(entries) > maxAgentThreadEntries {
		return nil, ErrSpaceInvalid
	}
	for _, entry := range entries {
		if !validAgentThreadKey(entry.Key) || !agentThreadKinds[entry.Kind] || strings.TrimSpace(entry.Body) == "" ||
			utf8.RuneCountInString(entry.Body) > maxAgentThreadEntryText {
			return nil, ErrSpaceInvalid
		}
	}
	var out *AgentThread
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSharedSpaceTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		thread, err := agentThreadTx(ctx, tx, spaceID, threadID)
		if err != nil {
			return err
		}
		if thread == nil {
			return ErrSpaceNotFound
		}
		if thread.OwnerUserID != userID {
			return ErrSpaceForbidden
		}
		keys := make([]string, 0, len(entries))
		for _, entry := range entries {
			result, err := tx.ExecContext(ctx, `INSERT INTO space_agent_thread_entries(thread_id,key,kind,body)
				VALUES($1,$2,$3,$4) ON CONFLICT (thread_id,key) DO UPDATE SET kind=EXCLUDED.kind,body=EXCLUDED.body,seq=DEFAULT
				WHERE space_agent_thread_entries.kind<>EXCLUDED.kind OR space_agent_thread_entries.body<>EXCLUDED.body`,
				threadID, entry.Key, entry.Kind, entry.Body)
			if err != nil {
				return err
			}
			if changed, _ := result.RowsAffected(); changed > 0 {
				keys = append(keys, entry.Key)
			}
		}
		if len(keys) == 0 {
			out = thread
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_agent_threads SET updated_at=now() WHERE id=$1`, threadID); err != nil {
			return err
		}
		if thread, err = agentThreadTx(ctx, tx, spaceID, threadID); err != nil {
			return err
		}
		out = thread
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "agent_thread.entries", threadID, map[string]any{
			"thread_id": threadID,
			"agent_id":  thread.AgentID,
			"keys":      keys,
		})
		return err
	})
	return out, err
}

func (db *Database) AgentThreadEntries(ctx context.Context, userID, spaceID, threadID string, after int64) ([]AgentThreadEntry, error) {
	items := []AgentThreadEntry{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSharedSpaceTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		thread, err := visibleAgentThreadTx(ctx, tx, userID, spaceID, threadID)
		if err != nil {
			return err
		}
		if thread == nil {
			return ErrSpaceNotFound
		}
		rows, err := tx.QueryContext(ctx, `SELECT key,seq,kind,body,created_at FROM space_agent_thread_entries
			WHERE thread_id=$1 AND seq>$2 ORDER BY seq LIMIT $3`, threadID, after, maxAgentThreadEntries)
		if err != nil {
			return err
		}
		index := map[string]int{}
		for rows.Next() {
			var entry AgentThreadEntry
			if err := rows.Scan(&entry.Key, &entry.Seq, &entry.Kind, &entry.Body, &entry.CreatedAt); err != nil {
				rows.Close()
				return err
			}
			entry.Reactions = []AgentThreadReaction{}
			index[entry.Key] = len(items)
			items = append(items, entry)
		}
		iterated := rows.Err()
		if err := rows.Close(); err != nil {
			return err
		}
		if iterated != nil {
			return iterated
		}
		if len(items) == 0 {
			return nil
		}
		reactions, err := tx.QueryContext(ctx, `SELECT r.key,r.emoji,count(*),bool_or(r.user_id=$2)
			FROM space_agent_thread_reactions r JOIN space_agent_thread_entries e ON e.thread_id=r.thread_id AND e.key=r.key
			WHERE r.thread_id=$1 AND e.seq BETWEEN $3 AND $4 GROUP BY r.key,r.emoji ORDER BY r.key,min(r.created_at)`,
			threadID, userID, items[0].Seq, items[len(items)-1].Seq)
		if err != nil {
			return err
		}
		defer reactions.Close()
		for reactions.Next() {
			var key string
			var reaction AgentThreadReaction
			if err := reactions.Scan(&key, &reaction.Emoji, &reaction.Count, &reaction.Mine); err != nil {
				return err
			}
			if at, ok := index[key]; ok {
				items[at].Reactions = append(items[at].Reactions, reaction)
			}
		}
		return reactions.Err()
	})
	return items, err
}

func (db *Database) SetAgentThreadReaction(ctx context.Context, userID, spaceID, threadID, key, emoji string, on bool) error {
	emoji = strings.TrimSpace(emoji)
	if !validAgentThreadID(threadID) || !validAgentThreadKey(key) || emoji == "" || utf8.RuneCountInString(emoji) > maxAgentThreadEmoji {
		return ErrSpaceInvalid
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSharedSpaceTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		thread, err := visibleAgentThreadTx(ctx, tx, userID, spaceID, threadID)
		if err != nil {
			return err
		}
		if thread == nil {
			return ErrSpaceNotFound
		}
		if on {
			result, err := tx.ExecContext(ctx, `INSERT INTO space_agent_thread_reactions(thread_id,key,user_id,emoji)
				SELECT $1,$2,$3,$4 WHERE EXISTS (SELECT 1 FROM space_agent_thread_entries WHERE thread_id=$1 AND key=$2)
				ON CONFLICT DO NOTHING`, threadID, key, userID, emoji)
			if err != nil {
				return err
			}
			if changed, _ := result.RowsAffected(); changed == 0 {
				var exists bool
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM space_agent_thread_entries WHERE thread_id=$1 AND key=$2)`,
					threadID, key).Scan(&exists); err != nil {
					return err
				}
				if !exists {
					return ErrSpaceNotFound
				}
			}
		} else if _, err := tx.ExecContext(ctx, `DELETE FROM space_agent_thread_reactions
			WHERE thread_id=$1 AND key=$2 AND user_id=$3 AND emoji=$4`, threadID, key, userID, emoji); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "agent_thread.reactions", threadID, map[string]any{
			"thread_id": threadID,
			"agent_id":  thread.AgentID,
			"key":       key,
		})
		return err
	})
}

func (db *Database) DeleteAgentThread(ctx context.Context, userID, spaceID, threadID string) error {
	if !validAgentThreadID(threadID) {
		return ErrSpaceInvalid
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := requireSharedSpaceTx(ctx, tx, spaceID, userID)
		if err != nil {
			return err
		}
		thread, err := agentThreadTx(ctx, tx, spaceID, threadID)
		if err != nil {
			return err
		}
		if thread == nil {
			return ErrSpaceNotFound
		}
		if !canEditSpaceAgent(access, thread.OwnerUserID, userID) {
			return ErrSpaceForbidden
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_agent_threads WHERE id=$1`, threadID); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "agent_thread.removed", threadID, map[string]any{
			"thread_id": threadID,
			"agent_id":  thread.AgentID,
		})
		return err
	})
}
