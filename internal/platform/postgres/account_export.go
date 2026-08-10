package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type AccountExportSpace struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

type AccountExportProfile struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Username      string    `json:"username"`
	Email         string    `json:"email"`
	AvatarVersion int64     `json:"avatar_version"`
	CreatedAt     time.Time `json:"created_at"`
}

type AccountExportJournal struct {
	Kind       string    `json:"kind"`
	ID         string    `json:"id"`
	SpaceID    string    `json:"space_id"`
	Title      string    `json:"title"`
	ACLVersion int64     `json:"acl_version"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type AccountExportAsset struct {
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
	ParentID  string    `json:"parent_id"`
	Filename  string    `json:"filename"`
	ObjectKey string    `json:"-"`
	MIMEType  string    `json:"mime_type"`
	ByteSize  int64     `json:"byte_size"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
}

type AccountExportMessage struct {
	ID        string          `json:"id"`
	SpaceID   string          `json:"space_id"`
	Content   json.RawMessage `json:"content"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type AccountPortableExport struct {
	FormatVersion int                    `json:"format_version"`
	ExportedAt    time.Time              `json:"exported_at"`
	Account       AccountExportProfile   `json:"account"`
	Settings      UserSettings           `json:"settings"`
	Spaces        []AccountExportSpace   `json:"spaces"`
	Journal       []AccountExportJournal `json:"journal"`
	Assets        []AccountExportAsset   `json:"assets"`
	Messages      []AccountExportMessage `json:"authored_messages"`
	Agents        []AccountExportAgent   `json:"agents"`
	Connections   []map[string]any       `json:"cloud_connections"`
}

// AccountPortableExport returns user-supplied and account-owned data without
// password hashes, sessions, OAuth secrets, internal object keys, or billing
// credentials. Journal CRDT and asset URLs are attached by the API after fresh
// authorization checks.
func (db *Database) AccountPortableExport(
	ctx context.Context, userID string,
) (*AccountPortableExport, error) {
	out := &AccountPortableExport{
		FormatVersion: 2,
		ExportedAt:    time.Now().UTC(),
		Spaces:        []AccountExportSpace{},
		Journal:       []AccountExportJournal{},
		Assets:        []AccountExportAsset{},
		Messages:      []AccountExportMessage{},
		Agents:        []AccountExportAgent{},
		Connections:   []map[string]any{},
	}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `
			SELECT id,name,username,email,avatar_version,created_at
			FROM users WHERE id=$1 AND lifecycle_state='active'`, userID,
		).Scan(
			&out.Account.ID, &out.Account.Name,
			&out.Account.Username, &out.Account.Email,
			&out.Account.AvatarVersion, &out.Account.CreatedAt,
		); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `
			SELECT email_updates_enabled,analytics_enabled,error_reporting_enabled
			FROM users WHERE id=$1`, userID,
		).Scan(
			&out.Settings.EmailUpdatesEnabled,
			&out.Settings.AnalyticsEnabled,
			&out.Settings.ErrorReportingEnabled,
		); err != nil {
			return err
		}
		if err := scanExportQuery(ctx, tx, `
			SELECT s.id,s.name,m.role,m.joined_at
			FROM space_members m JOIN spaces s ON s.id=m.space_id
			WHERE m.user_id=$1
			ORDER BY m.joined_at`, userID, func(rows *sql.Rows) error {
			var item AccountExportSpace
			if err := rows.Scan(&item.ID, &item.Name, &item.Role, &item.JoinedAt); err != nil {
				return err
			}
			out.Spaces = append(out.Spaces, item)
			return nil
		}); err != nil {
			return err
		}
		if err := scanExportQuery(ctx, tx, `
			SELECT 'note',id,space_id,title_projection,acl_version,created_at,updated_at
			FROM space_notes n
			WHERE creator_user_id=$1 AND lifecycle_state='active'
			  AND EXISTS(
			      SELECT 1 FROM space_members m
			      WHERE m.space_id=n.space_id AND m.user_id=$1
			  )
			  AND (n.audience_kind='space' OR EXISTS(
			      SELECT 1 FROM space_conversation_members cm
			      WHERE cm.conversation_id=n.audience_conversation_id
			        AND cm.actor_kind='person' AND cm.user_id=$1
			  ))
			UNION ALL
			SELECT 'drawing',id,space_id,title,acl_version,created_at,updated_at
			FROM space_drawings d
			WHERE creator_user_id=$1 AND lifecycle_state='active'
			  AND EXISTS(
			      SELECT 1 FROM space_members m
			      WHERE m.space_id=d.space_id AND m.user_id=$1
			  )
			  AND (d.audience_kind='space' OR EXISTS(
			      SELECT 1 FROM space_conversation_members cm
			      WHERE cm.conversation_id=d.audience_conversation_id
			        AND cm.actor_kind='person' AND cm.user_id=$1
			  ))
			ORDER BY created_at`, userID, func(rows *sql.Rows) error {
			var item AccountExportJournal
			if err := rows.Scan(
				&item.Kind, &item.ID, &item.SpaceID, &item.Title,
				&item.ACLVersion, &item.CreatedAt, &item.UpdatedAt,
			); err != nil {
				return err
			}
			out.Journal = append(out.Journal, item)
			return nil
		}); err != nil {
			return err
		}
		if err := scanExportQuery(ctx, tx, `
			SELECT 'note',a.id,a.note_id,a.display_name,b.r2_object_key,
			       COALESCE(b.server_detected_mime_type,b.client_declared_mime_type),
			       b.byte_size,b.sha256,a.created_at
			FROM space_note_assets a
			JOIN space_notes n ON n.id=a.note_id
			JOIN library_files f ON f.id=a.file_id
			JOIN library_blobs b ON b.id=f.blob_id
			WHERE n.creator_user_id=$1 AND a.lifecycle_state='ready'
			  AND f.lifecycle_state='ready' AND b.lifecycle_state='ready'
			  AND (n.audience_kind='space' OR EXISTS(
			      SELECT 1 FROM space_conversation_members cm
			      WHERE cm.conversation_id=n.audience_conversation_id
			        AND cm.actor_kind='person' AND cm.user_id=$1
			  ))
			UNION ALL
			SELECT 'drawing',a.id,a.drawing_id,a.display_name,b.r2_object_key,
			       COALESCE(b.server_detected_mime_type,b.client_declared_mime_type),
			       b.byte_size,b.sha256,a.created_at
			FROM space_drawing_assets a
			JOIN space_drawings d ON d.id=a.drawing_id
			JOIN library_files f ON f.id=a.file_id
			JOIN library_blobs b ON b.id=f.blob_id
			WHERE d.creator_user_id=$1 AND a.lifecycle_state='ready'
			  AND f.lifecycle_state='ready' AND b.lifecycle_state='ready'
			  AND (d.audience_kind='space' OR EXISTS(
			      SELECT 1 FROM space_conversation_members cm
			      WHERE cm.conversation_id=d.audience_conversation_id
			        AND cm.actor_kind='person' AND cm.user_id=$1
			  ))
			UNION ALL
			SELECT 'library',i.id,i.space_id,i.display_name,b.r2_object_key,
			       COALESCE(b.server_detected_mime_type,b.client_declared_mime_type),
			       b.byte_size,b.sha256,i.added_at
			FROM space_library_items i
			JOIN library_files f ON f.id=i.file_id
			JOIN library_blobs b ON b.id=f.blob_id
			WHERE f.uploader_user_id=$1 AND i.lifecycle_state='ready'
			  AND f.lifecycle_state='ready' AND b.lifecycle_state='ready'
			  AND EXISTS(
			      SELECT 1 FROM space_members m
			      WHERE m.space_id=i.space_id AND m.user_id=$1
			  )
			  AND (i.audience_kind='space' OR EXISTS(
			      SELECT 1 FROM space_conversation_members cm
			      WHERE cm.conversation_id=i.audience_conversation_id
			        AND cm.actor_kind='person' AND cm.user_id=$1
			  ))
			UNION ALL
			SELECT 'message_attachment',a.id,COALESCE(a.message_id,a.space_id),
			       a.display_name,b.r2_object_key,
			       COALESCE(b.server_detected_mime_type,b.client_declared_mime_type),
			       b.byte_size,b.sha256,a.created_at
			FROM space_message_attachments a
			JOIN library_files f ON f.id=a.file_id
			JOIN library_blobs b ON b.id=f.blob_id
			WHERE a.uploader_user_id=$1 AND a.lifecycle_state='ready'
			  AND f.lifecycle_state='ready' AND b.lifecycle_state='ready'
			  AND EXISTS(
			      SELECT 1 FROM space_members m
			      WHERE m.space_id=a.space_id AND m.user_id=$1
			  )
			  AND (a.message_id IS NULL OR EXISTS(
			      SELECT 1 FROM space_messages message
			      WHERE message.id=a.message_id AND (message.conversation_id IS NULL OR EXISTS(
			          SELECT 1 FROM space_conversation_members cm
			          WHERE cm.conversation_id=message.conversation_id
			            AND cm.actor_kind='person' AND cm.user_id=$1
			      ))
			  ))
			ORDER BY created_at`, userID, func(rows *sql.Rows) error {
			var item AccountExportAsset
			if err := rows.Scan(
				&item.Kind, &item.ID, &item.ParentID, &item.Filename,
				&item.ObjectKey, &item.MIMEType, &item.ByteSize,
				&item.SHA256, &item.CreatedAt,
			); err != nil {
				return err
			}
			out.Assets = append(out.Assets, item)
			return nil
		}); err != nil {
			return err
		}
		if err := scanExportQuery(ctx, tx, `
			SELECT id,space_id,content,created_at,COALESCE(edited_at,created_at)
			FROM space_messages
			WHERE sender_user_id=$1 AND sender_kind='person'
			  AND (conversation_id IS NULL OR EXISTS(
			      SELECT 1 FROM space_conversation_members cm
			      WHERE cm.conversation_id=space_messages.conversation_id
			        AND cm.actor_kind='person' AND cm.user_id=$1
			  ))
			ORDER BY created_at`, userID, func(rows *sql.Rows) error {
			var item AccountExportMessage
			if err := rows.Scan(
				&item.ID, &item.SpaceID, &item.Content,
				&item.CreatedAt, &item.UpdatedAt,
			); err != nil {
				return err
			}
			out.Messages = append(out.Messages, item)
			return nil
		}); err != nil {
			return err
		}
		if err := appendAccountAgentExport(ctx, tx, userID, out); err != nil {
			return err
		}
		return scanExportQuery(ctx, tx, `
			SELECT provider,name,account_id,account_display,
			       uses_custom_oauth_client,created_at,updated_at
			FROM cloud_connections
			WHERE user_id=$1 AND revoked_at IS NULL
			ORDER BY created_at`, userID, func(rows *sql.Rows) error {
			var provider, name, accountID, display string
			var custom bool
			var createdAt, updatedAt time.Time
			if err := rows.Scan(
				&provider, &name, &accountID, &display, &custom,
				&createdAt, &updatedAt,
			); err != nil {
				return err
			}
			out.Connections = append(out.Connections, map[string]any{
				"provider": provider, "name": name, "account_id": accountID,
				"account_display": display, "uses_custom_oauth_client": custom,
				"created_at": createdAt, "updated_at": updatedAt,
			})
			return nil
		})
	})
	return out, err
}

func scanExportQuery(
	ctx context.Context,
	tx *sql.Tx,
	query string,
	userID string,
	scan func(*sql.Rows) error,
) error {
	rows, err := tx.QueryContext(ctx, query, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}
