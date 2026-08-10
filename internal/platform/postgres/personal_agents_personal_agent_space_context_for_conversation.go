package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PersonalAgentSpaceContextForConversation builds the same bounded Space
// context while limiting chat history to the current shared conversation.
// An empty conversation ID means the Space-wide chat, never every private
// selected-member conversation in the Space.
func (db *Database) PersonalAgentSpaceContextForConversation(ctx context.Context, userID, spaceID, conversationID string, permissions json.RawMessage) (string, error) {
	var allowed map[string]bool
	_ = json.Unmarshal(permissions, &allowed)
	// "notes" here has always meant the free-text notes column on a task, not
	// the Notes surface, which is device-local and unreadable by the server. The
	// key is now "task_notes"; the old name is still honoured because this value
	// is a stored user setting in personal_agents.context_permissions and a hard
	// break would silently flip someone's toggle.
	if allowed["notes"] && !allowed["task_notes"] {
		allowed["task_notes"] = true
	}
	parts := []string{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		// Membership is the entry gate, not messages.read. This used to require
		// messages.read for the whole function, so a member with Library and
		// Tasks access but no chat access received zero Space context -- not even
		// the Library and Tasks they can see. Each section below still checks its
		// own permission, and the "may this member use agents here" gate is
		// agents.run, enforced upstream by validateAgentSpaceAccessTx.
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		var name string
		if err := tx.QueryRowContext(ctx, `SELECT name FROM spaces WHERE id=$1`, spaceID).Scan(&name); err != nil {
			return err
		}
		parts = append(parts, "Space: "+name)
		if allowed["members"] {
			rows, err := tx.QueryContext(ctx, `SELECT u.name,m.role FROM space_members m JOIN users u ON u.id=m.user_id WHERE m.space_id=$1 ORDER BY lower(u.name) LIMIT 50`, spaceID)
			if err != nil {
				return err
			}
			names := []string{}
			for rows.Next() {
				var value, role string
				if err := rows.Scan(&value, &role); err != nil {
					rows.Close()
					return err
				}
				names = append(names, fmt.Sprintf("%s (%s)", value, role))
			}
			if err := rows.Close(); err != nil {
				return err
			}
			if len(names) > 0 {
				parts = append(parts, "Members: "+strings.Join(names, ", "))
			}
		}
		// messages.read is checked here rather than as the entry gate, so a member
		// without chat access loses only this section instead of the whole context.
		canReadChat := false
		if allowed["space_chat"] {
			readable, permissionErr := hasSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead)
			if permissionErr != nil {
				return permissionErr
			}
			canReadChat = readable
		}
		if canReadChat {
			if conversationID != "" {
				if err := requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, conversationID); err != nil {
					return err
				}
			}
			rows, err := tx.QueryContext(ctx, `SELECT content FROM space_messages WHERE space_id=$1 AND COALESCE(conversation_id,'')=$2 ORDER BY seq DESC LIMIT 30`, spaceID, conversationID)
			if err != nil {
				return err
			}
			messages := []string{}
			for rows.Next() {
				var raw []byte
				if err := rows.Scan(&raw); err != nil {
					rows.Close()
					return err
				}
				var spans []MessageSpan
				_ = json.Unmarshal(raw, &spans)
				if value := messagePreview(spans); value != "" {
					messages = append(messages, value)
				}
			}
			if err := rows.Close(); err != nil {
				return err
			}
			for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
				messages[left], messages[right] = messages[right], messages[left]
			}
			if len(messages) > 0 {
				parts = append(parts, "Recent chat:\n- "+strings.Join(messages, "\n- "))
			}
		}
		if allowed["tasks"] || allowed["task_notes"] {
			canViewTasks, permissionErr := hasSpacePermissionTx(ctx, tx, userID, spaceID, PermissionTasksView)
			if permissionErr != nil {
				return permissionErr
			}
			if canViewTasks && allowed["tasks"] {
				rows, queryErr := tx.QueryContext(ctx, `SELECT title,status FROM space_tasks WHERE space_id=$1 AND archived_at IS NULL ORDER BY updated_at DESC LIMIT 30`, spaceID)
				if queryErr != nil {
					return queryErr
				}
				tasks := []string{}
				for rows.Next() {
					var title, status string
					if err := rows.Scan(&title, &status); err != nil {
						rows.Close()
						return err
					}
					tasks = append(tasks, fmt.Sprintf("%s (%s)", title, status))
				}
				if err := rows.Close(); err != nil {
					return err
				}
				if len(tasks) > 0 {
					parts = append(parts, "Tasks:\n- "+strings.Join(tasks, "\n- "))
				}
			}
			if canViewTasks && allowed["task_notes"] {
				rows, queryErr := tx.QueryContext(ctx, `SELECT title,notes FROM space_tasks WHERE space_id=$1 AND archived_at IS NULL AND btrim(notes)<>'' ORDER BY updated_at DESC LIMIT 20`, spaceID)
				if queryErr != nil {
					return queryErr
				}
				notes := []string{}
				for rows.Next() {
					var title, note string
					if err := rows.Scan(&title, &note); err != nil {
						rows.Close()
						return err
					}
					notes = append(notes, strings.TrimSpace(title+": "+note))
				}
				if err := rows.Close(); err != nil {
					return err
				}
				if len(notes) > 0 {
					parts = append(parts, "Task notes:\n- "+strings.Join(notes, "\n- "))
				}
			}
		}
		if allowed["library"] {
			ok, permissionErr := hasSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView)
			if permissionErr != nil {
				return permissionErr
			}
			if ok {
				rows, queryErr := tx.QueryContext(ctx, `SELECT display_name,caption,tags FROM space_library_items WHERE space_id=$1 AND lifecycle_state='ready' AND hidden=FALSE ORDER BY updated_at DESC LIMIT 40`, spaceID)
				if queryErr == nil {
					items := []string{}
					for rows.Next() {
						var display, caption string
						var tagsRaw []byte
						if rows.Scan(&display, &caption, &tagsRaw) == nil {
							var tags []string
							_ = json.Unmarshal(tagsRaw, &tags)
							items = append(items, strings.TrimSpace(strings.Join([]string{display, caption, strings.Join(tags, ", ")}, " — ")))
						}
					}
					_ = rows.Close()
					if len(items) > 0 {
						parts = append(parts, "Library:\n- "+strings.Join(items, "\n- "))
					}
				}
			}
		}
		return nil
	})
	return strings.Join(parts, "\n\n"), err
}

func (db *Database) AppendPersonalAgentMemory(ctx context.Context, userID, spaceID, agentID, prompt, response string) error {
	prompt, response = strings.TrimSpace(prompt), strings.TrimSpace(response)
	if prompt == "" || response == "" {
		return nil
	}
	event, _ := json.Marshal(map[string]any{"prompt": prompt, "response": response, "created_at": time.Now().UTC()})
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := personalAgentAllowedTx(ctx, tx, userID, spaceID, agentID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO personal_agent_instances(id,agent_id,invoker_user_id,space_id,scope_key,memory)
			VALUES($1,$2,$3,$4,$4,jsonb_build_array($5::jsonb))
			ON CONFLICT(agent_id,invoker_user_id,scope_key) DO UPDATE SET memory=(CASE WHEN jsonb_array_length(personal_agent_instances.memory)>=20 THEN personal_agent_instances.memory-(0) ELSE personal_agent_instances.memory END)||$5::jsonb,updated_at=NOW()`,
			"agentinstance_"+uuid.NewString(), agentID, userID, spaceID, event)
		return err
	})
}

// PersonalAgentMemoryContext returns only the memory isolated to one invoker,
// agent, and Space. Authorization is checked on every read so revoking a grant
// or removing a member immediately prevents future use of that memory.
func (db *Database) PersonalAgentMemoryContext(ctx context.Context, userID, spaceID, agentID string) (string, error) {
	type memoryEvent struct {
		Prompt   string    `json:"prompt"`
		Response string    `json:"response"`
		Created  time.Time `json:"created_at"`
	}
	events := []memoryEvent{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := personalAgentAllowedTx(ctx, tx, userID, spaceID, agentID); err != nil {
			return err
		}
		var raw []byte
		err := tx.QueryRowContext(ctx, `SELECT memory FROM personal_agent_instances
			WHERE agent_id=$1 AND invoker_user_id=$2 AND scope_key=$3`, agentID, userID, spaceID).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, &events)
	})
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(events))
	for _, event := range events {
		prompt, response := strings.TrimSpace(event.Prompt), strings.TrimSpace(event.Response)
		if prompt != "" && response != "" {
			parts = append(parts, "User: "+prompt+"\nAgent: "+response)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}
