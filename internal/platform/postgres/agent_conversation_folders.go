package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrConversationFolderNotFound covers a missing folder, one owned by another
// account, and filing a conversation into a folder of a different agent.
var ErrConversationFolderNotFound = errors.New("conversation folder not found")

// AgentConversationFolder groups one agent's conversations in its sidebar.
type AgentConversationFolder struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agentId"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

const conversationFolderColumns = `id, agent_id, name, created_at, updated_at`

func scanConversationFolder(row interface{ Scan(...any) error }) (AgentConversationFolder, error) {
	var folder AgentConversationFolder
	err := row.Scan(&folder.ID, &folder.AgentID, &folder.Name, &folder.CreatedAt, &folder.UpdatedAt)
	return folder, err
}

// ListAgentConversationFolders returns every folder the account made, oldest first.
func (db *Database) ListAgentConversationFolders(ctx context.Context, userID string) ([]AgentConversationFolder, error) {
	folders := []AgentConversationFolder{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT `+conversationFolderColumns+`
			FROM agent_conversation_folders
			WHERE owner_user_id = $1
			ORDER BY created_at, id
		`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			folder, err := scanConversationFolder(rows)
			if err != nil {
				return err
			}
			folders = append(folders, folder)
		}
		return rows.Err()
	})
	return folders, err
}

// CreateAgentConversationFolder adds a folder to one of the account's agents.
func (db *Database) CreateAgentConversationFolder(ctx context.Context, userID, agentID, name string) (AgentConversationFolder, error) {
	var folder AgentConversationFolder
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var err error
		folder, err = scanConversationFolder(tx.QueryRowContext(ctx, `
			INSERT INTO agent_conversation_folders(id, owner_user_id, agent_id, name)
			SELECT $1, $2, identity.id, $4
			FROM misty_ask_identities identity
			WHERE identity.id = $3 AND identity.owner_user_id = $2
			RETURNING `+conversationFolderColumns,
			"folder_"+uuid.NewString(), userID, agentID, strings.TrimSpace(name)))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConversationFolderNotFound
		}
		return err
	})
	return folder, err
}

// RenameAgentConversationFolder changes a folder's name.
func (db *Database) RenameAgentConversationFolder(ctx context.Context, userID, folderID, name string) (AgentConversationFolder, error) {
	var folder AgentConversationFolder
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var err error
		folder, err = scanConversationFolder(tx.QueryRowContext(ctx, `
			UPDATE agent_conversation_folders SET name = $1, updated_at = now()
			WHERE id = $2 AND owner_user_id = $3
			RETURNING `+conversationFolderColumns,
			strings.TrimSpace(name), folderID, userID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConversationFolderNotFound
		}
		return err
	})
	return folder, err
}

// DeleteAgentConversationFolder removes a folder; its conversations return to Recents.
func (db *Database) DeleteAgentConversationFolder(ctx context.Context, userID, folderID string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			DELETE FROM agent_conversation_folders WHERE id = $1 AND owner_user_id = $2
		`, folderID, userID)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil {
			return err
		} else if rows == 0 {
			return ErrConversationFolderNotFound
		}
		return nil
	})
}

// FileAgentConversation moves a conversation into one of its own agent's folders,
// or back to Recents when folderID is empty.
func (db *Database) FileAgentConversation(ctx context.Context, userID, conversationID, folderID string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE misty_ask_conversations conversation
			SET folder_id = NULLIF($1, '')
			WHERE conversation.id = $2 AND conversation.user_id = $3 AND conversation.deleted_at IS NULL
			  AND ($1 = '' OR EXISTS (
				SELECT 1 FROM agent_conversation_folders folder
				WHERE folder.id = $1 AND folder.owner_user_id = $3
				  AND folder.agent_id = conversation.agent_id
			  ))
		`, folderID, conversationID, userID)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil {
			return err
		} else if rows == 0 {
			return ErrConversationFolderNotFound
		}
		return nil
	})
}
