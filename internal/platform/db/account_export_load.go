package db

import (
	"context"
	"database/sql"
	"time"
)

// loadExportAccountProfile fills the account identity block for an active user.
func loadExportAccountProfile(
	ctx context.Context, tx *sql.Tx, userID string, out *AccountPortableExport,
) error {
	if err := tx.QueryRowContext(ctx, `
			SELECT id,username,email,avatar_version,created_at
			FROM users WHERE id=$1 AND lifecycle_state='active'`, userID,
	).Scan(
		&out.Account.ID,
		&out.Account.Username, &out.Account.Email,
		&out.Account.AvatarVersion, &out.Account.CreatedAt,
	); err != nil {
		return err
	}
	return nil
}

// loadExportSettings copies the user's account-level preference flags.
func loadExportSettings(
	ctx context.Context, tx *sql.Tx, userID string, out *AccountPortableExport,
) error {
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
	return nil
}

// loadExportSpaces lists the spaces the user is a member of.
func loadExportSpaces(
	ctx context.Context, tx *sql.Tx, userID string, out *AccountPortableExport,
) error {
	return scanExportQuery(ctx, tx, `
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
	})
}

// loadExportJournal lists the notes and drawings the user created that they can still see.
func loadExportJournal(
	ctx context.Context, tx *sql.Tx, userID string, out *AccountPortableExport,
) error {
	return scanExportQuery(ctx, tx, `
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
	})
}

// loadExportAssets lists the ready files the user uploaded or authored and can still see.
func loadExportAssets(
	ctx context.Context, tx *sql.Tx, userID string, out *AccountPortableExport,
) error {
	return scanExportQuery(ctx, tx, `
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
	})
}

// loadExportMessages lists the messages the user authored in spaces and conversations they can see.
func loadExportMessages(
	ctx context.Context, tx *sql.Tx, userID string, out *AccountPortableExport,
) error {
	return scanExportQuery(ctx, tx, `
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
	})
}

// loadExportConnections lists the user's active cloud connections without credentials.
func loadExportConnections(
	ctx context.Context, tx *sql.Tx, userID string, out *AccountPortableExport,
) error {
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
}
