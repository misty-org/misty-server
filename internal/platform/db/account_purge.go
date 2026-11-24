package db

import (
	"context"
	"database/sql"
)

var accountPurgeTables = []struct{ Table, Column string }{
	{"sessions", "user_id"},
	{"device_sign_in_requests", "approved_user_id"},
	{"email_verification_tokens", "user_id"},
	{"password_reset_tokens", "user_id"},
	{"google_reauthentication_tokens", "user_id"},
	{"google_sign_in_flows", "user_id"},
	{"google_sign_in_flows", "reauthenticate_user_id"},
	{"realtime_tickets", "user_id"},
	{"space_resolve_tickets", "user_id"},
	{"account_setup_keys", "user_id"},
	{"email_outbox", "user_id"},
	{"account_controls", "user_id"},
	{"voice_live_sessions", "owner_user_id"},
	{"voice_transcripts", "owner_user_id"},
	{"cloud_connections", "user_id"},
	{"space_provider_credentials", "user_id"},
	{"space_personal_item_state", "user_id"},
	{"space_conversation_reads", "user_id"},
	{"space_conversation_members", "user_id"},
	{"space_conversation_agents", "owner_user_id"},
	{"space_thread_reads", "user_id"},
	{"space_drive_in_use", "user_id"},
	{"space_invitations", "invited_user_id"},
	{"space_invitations", "invited_by_user_id"},
	{"space_creation_requests", "user_id"},
	{"owner_storage_usage", "owner_user_id"},
	{"space_agent_thread_reactions", "user_id"},
	{"space_agent_threads", "owner_user_id"},
}

var accountPurgeKept = map[string]string{
	"licenses.user_id":                           "the identity the anonymized users row requires",
	"account_deletion_requests.user_id":          "the record that the deletion ran",
	"space_messages.sender_user_id":              "messages in Spaces other people still use",
	"space_message_reactions.user_id":            "reactions on those messages",
	"space_message_attachments.uploader_user_id": "files attached to those messages",
	"space_conversations.created_by_user_id":     "chats in those Spaces",
	"space_notes.creator_user_id":                "notes in those Spaces",
	"space_note_assets.uploader_user_id":         "files in those notes",
	"space_apps.installed_by":                    "apps installed in those Spaces",
	"space_nodes.uploader_user_id":               "files in those Spaces",
	"space_item_aliases.created_by_user_id":      "links in those Spaces",
	"space_library_uploads.user_id":              "uploads that became those files",
	"space_drive_items.modified_by":              "files in those Spaces' drives",
	"space_drive_items.trashed_by":               "files in those Spaces' trash",
	"space_storage_contributions.user_id":        "the storage those items count against",
	"space_upload_reservations.user_id":          "reservations in those Spaces",
	"library_files.uploader_user_id":             "those attachments' and notes' files",
	"library_exports.requested_by_user_id":       "exports of those Spaces",
	"library_legal_holds.created_by_user_id":     "holds on those Spaces",
	"space_events.actor_user_id":                 "activity in those Spaces, kept seven days",
	"space_library_audit_events.actor_user_id":   "the Library audit trail of those Spaces",
	"space_agents.owner_user_id":                 "the agent's name in those Spaces",
	"space_integrations.created_by":              "integrations made in those Spaces",
}

func AccountPurgeKept() map[string]string { return accountPurgeKept }

func purgeAccountDataTx(ctx context.Context, tx *sql.Tx, userID string) error {
	if err := handOverSharedSpacesTx(ctx, tx, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE spaces SET lifecycle_state='pending_deletion',
		deletion_requested_at=COALESCE(deletion_requested_at,NOW()),permanent_delete_after=NOW(),updated_at=NOW()
		WHERE owner_user_id=$1`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM space_members m USING spaces s
		WHERE m.space_id=s.id AND m.user_id=$1 AND s.owner_user_id<>$1`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE space_agents SET updated_by_user_id=NULL WHERE updated_by_user_id=$1`, userID); err != nil {
		return err
	}
	for _, target := range accountPurgeTables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+target.Table+` WHERE `+target.Column+`=$1`, userID); err != nil {
			return err
		}
	}
	return nil
}
