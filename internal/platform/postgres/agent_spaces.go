package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// Agents are members of Spaces. These helpers cover replying in a Space's
// chat and saving what an agent makes into a Space it is in.

// CreateAgentMemberRequestMessage posts the target agent's words about a
// request that a person made by @mentioning it, under the mentioning message.
// The agent's owner is the sender of record, as for every agent message.
func (db *Database) CreateAgentMemberRequestMessage(ctx context.Context, request *AgentMemberRequest, kind, text string) (*SpaceMessage, error) {
	if request == nil || request.SourceMessageID == "" {
		return nil, ErrSpaceInvalid
	}
	origin, _ := json.Marshal(map[string]any{"kind": kind, "agent_request_id": request.ID, "agent_run_id": request.ChildRunID})
	return db.createSpaceAgentMessageWithProvenance(ctx, request.TargetOwnerUserID, request.SpaceID, request.SourceConversationID, request.TargetAgentID,
		[]MessageSpan{{Type: "text", Text: text}}, true, request.SourceMessageID, origin)
}

// AgentArtifactKinds are what an agent can save to a Space.
var AgentArtifactKinds = map[string]bool{"download": true, "file": true, "image": true, "code": true, "document": true}

// AgentArtifactProvenance says which agent made an upload, in which of its
// owner's conversations and runs. It travels with the upload until the
// Library item exists.
type AgentArtifactProvenance struct {
	AgentID        string `json:"agent_id"`
	ConversationID string `json:"conversation_id,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	Kind           string `json:"kind"`
}

func (p *AgentArtifactProvenance) normalize() error {
	p.AgentID, p.ConversationID, p.RunID, p.Kind = strings.TrimSpace(p.AgentID), strings.TrimSpace(p.ConversationID), strings.TrimSpace(p.RunID), strings.TrimSpace(p.Kind)
	if p.Kind == "" {
		p.Kind = "file"
	}
	if p.AgentID == "" || !AgentArtifactKinds[p.Kind] || len(p.ConversationID) > 200 || len(p.RunID) > 200 {
		return ErrLibraryInvalid
	}
	return nil
}

// requireAgentArtifactUploadTx checks that the uploader owns the agent and
// that the upload goes to a Space the agent is in.
func requireAgentArtifactUploadTx(ctx context.Context, tx *sql.Tx, userID, spaceID, purpose string, provenance *AgentArtifactProvenance) error {
	if err := provenance.normalize(); err != nil {
		return err
	}
	if purpose != "library" {
		return ErrLibraryInvalid
	}
	in, err := agentInSpaceTx(ctx, tx, userID, spaceID, provenance.AgentID)
	if err != nil {
		return err
	}
	if !in {
		return ErrLibraryForbidden
	}
	return nil
}

// recordAgentArtifactTx turns a finished agent upload into a Space item with
// provenance. Uploads without an agent are left alone.
func recordAgentArtifactTx(ctx context.Context, tx *sql.Tx, uploadID, itemID, spaceID, userID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO space_agent_artifacts(item_id,space_id,agent_id,agent_name,owner_user_id,conversation_id,run_id,kind)
		SELECT $2,$3,u.agent_id,a.name,$4,COALESCE(u.agent_conversation_id,''),COALESCE(u.agent_run_id,''),COALESCE(u.agent_artifact_kind,'file')
		FROM space_library_uploads u JOIN misty_ask_identities a ON a.id=u.agent_id AND a.owner_user_id=$4
		WHERE u.id=$1 AND u.agent_id IS NOT NULL
		ON CONFLICT (item_id) DO NOTHING`, uploadID, itemID, spaceID, userID)
	return err
}

// SpaceAgentArtifact is a Library item an agent saved to a Space.
type SpaceAgentArtifact struct {
	ItemID      string    `json:"item_id"`
	SpaceID     string    `json:"space_id"`
	DisplayName string    `json:"display_name"`
	MIMEType    string    `json:"mime_type"`
	ByteSize    int64     `json:"byte_size"`
	AgentID     string    `json:"agent_id,omitempty"`
	AgentName   string    `json:"agent_name"`
	OwnerUserID string    `json:"owner_user_id"`
	OwnerName   string    `json:"owner_name"`
	Kind        string    `json:"kind"`
	CreatedAt   time.Time `json:"created_at"`
	// Only the agent's owner sees which of their private conversations and
	// runs made the item.
	ConversationID string `json:"conversation_id,omitempty"`
	RunID          string `json:"run_id,omitempty"`
}

// SpaceAgentArtifacts lists what agents saved to a Space, newest first, to
// anyone who can see its Library. agentID narrows it to one agent.
func (db *Database) SpaceAgentArtifacts(ctx context.Context, userID, spaceID, agentID string, limit int) ([]SpaceAgentArtifact, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	items := []SpaceAgentArtifact{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT x.item_id,x.space_id,i.display_name,COALESCE(b.server_detected_mime_type,''),COALESCE(b.byte_size,0),
			COALESCE(x.agent_id,''),COALESCE(a.name,x.agent_name),x.owner_user_id,COALESCE(u.username,''),x.kind,x.created_at,
			CASE WHEN x.owner_user_id=$1 THEN x.conversation_id ELSE '' END,CASE WHEN x.owner_user_id=$1 THEN x.run_id ELSE '' END
			FROM space_agent_artifacts x
			JOIN space_library_items i ON i.id=x.item_id AND i.lifecycle_state='ready' AND i.hidden=FALSE AND i.audience_kind='space'
			JOIN library_files f ON f.id=i.file_id LEFT JOIN library_blobs b ON b.id=f.blob_id
			LEFT JOIN misty_ask_identities a ON a.id=x.agent_id
			LEFT JOIN users u ON u.id=x.owner_user_id
			WHERE x.space_id=$2 AND ($3='' OR x.agent_id=$3)
			ORDER BY x.created_at DESC,x.item_id LIMIT $4`, userID, spaceID, agentID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceAgentArtifact
			if err := rows.Scan(&item.ItemID, &item.SpaceID, &item.DisplayName, &item.MIMEType, &item.ByteSize, &item.AgentID, &item.AgentName,
				&item.OwnerUserID, &item.OwnerName, &item.Kind, &item.CreatedAt, &item.ConversationID, &item.RunID); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}
