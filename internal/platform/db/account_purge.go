package db

import (
	"context"
	"database/sql"
)

// The full purge of a deleted account, as Privacy §10 promises: after the
// retention window "we delete your personal information or permanently
// anonymize it". The users row stays, renamed and without credentials, as
// the "deleted user" that content in Spaces other people still use is
// attributed to. Everything else the account held goes:
//
//   - Spaces it owns alone, the personal Space included, are marked for the
//     Space purge (space_purge.go), which removes their notes, drawings,
//     chats, Library and R2 objects;
//   - every row in accountPurgeTables: sync vaults, voice transcripts, devices,
//     sessions, connections and settings.
//
// What stays, by design, is listed in accountPurgeKept. The purge test
// (test/contract/postgres/account_purge_test.go) checks every table that
// references users, so a new table must be added to one of the two lists.

// accountPurgeTables are deleted by their user column, in this order.
var accountPurgeTables = []struct{ Table, Column string }{
	{"sessions", "user_id"},
	{"auth_handoff_tokens", "user_id"},
	{"email_verification_tokens", "user_id"},
	{"password_reset_tokens", "user_id"},
	{"google_reauthentication_tokens", "user_id"},
	{"google_sign_in_flows", "user_id"},
	{"google_sign_in_flows", "reauthenticate_user_id"},
	{"realtime_tickets", "user_id"},
	{"space_resolve_tickets", "user_id"},
	{"device_channel_tickets", "user_id"},
	{"account_setup_keys", "user_id"},
	{"email_outbox", "user_id"},
	{"account_controls", "user_id"},
	// Encrypted sync: every vault, with its records, blobs and devices.
	{"sync_vaults", "user_id"},
	{"voice_live_sessions", "owner_user_id"},
	{"voice_transcripts", "owner_user_id"},
	// Connections.
	{"cloud_connections", "user_id"},
	{"space_provider_credentials", "user_id"},
	// Devices.
	{"device_policies", "user_id"},
	{"device_revoked_keys", "user_id"},
	{"trusted_device_request_nonces", "owner_user_id"},
	{"trusted_devices", "user_id"},
	// The account's private layer in Spaces it belonged to.
	{"space_personal_item_state", "user_id"},
	{"space_library_item_views", "user_id"},
	{"space_conversation_reads", "user_id"},
	{"space_conversation_members", "user_id"},
	{"space_invitations", "invited_user_id"},
	{"space_invitations", "invited_by_user_id"},
	{"space_creation_requests", "user_id"},
	{"owner_storage_usage", "owner_user_id"},
}

// accountPurgeKept are the user columns that keep pointing at the deleted
// user after the purge, each because it is attribution on content other
// people still use, or the anonymized account record itself.
var accountPurgeKept = map[string]string{
	"licenses.user_id":                                    "the identity the anonymized users row requires",
	"account_deletion_requests.user_id":                   "the record that the deletion ran",
	"space_messages.sender_user_id":                       "messages in Spaces other people still use",
	"space_message_reactions.user_id":                     "reactions on those messages",
	"space_message_attachments.uploader_user_id":          "files attached to those messages",
	"space_message_library_references.created_by_user_id": "references in those messages",
	"space_conversations.created_by_user_id":              "chats in those Spaces",
	"space_notes.creator_user_id":                         "notes in those Spaces",
	"space_note_assets.uploader_user_id":                  "files in those notes",
	"space_drawings.creator_user_id":                      "drawings in those Spaces",
	"space_drawing_assets.uploader_user_id":               "files in those drawings",
	"space_nodes.uploader_user_id":                        "files in those Spaces",
	"space_item_aliases.created_by_user_id":               "links in those Spaces",
	"space_library_items.added_by_user_id":                "Library items in those Spaces",
	"space_library_items.contributing_user_id":            "Library items in those Spaces",
	"space_library_direct_references.created_by_user_id":  "Library links in those Spaces",
	"space_library_uploads.user_id":                       "uploads that became those items",
	"space_storage_contributions.user_id":                 "the storage those items count against",
	"space_upload_reservations.user_id":                   "reservations in those Spaces",
	"space_rendition_reservations.user_id":                "reservations in those Spaces",
	"library_files.uploader_user_id":                      "files of those items",
	"library_item_versions.created_by_user_id":            "edits of those items",
	"library_exports.requested_by_user_id":                "exports of those Spaces",
	"library_legal_holds.created_by_user_id":              "holds on those Spaces",
	"space_events.actor_user_id":                          "activity in those Spaces, kept seven days",
	"space_library_audit_events.actor_user_id":            "the Library audit trail of those Spaces",
	"space_agents.owner_user_id":                          "the agent's name in those Spaces",
	"space_agent_artifacts.owner_user_id":                 "agent results shared in those Spaces",
	"space_integrations.connected_by_user_id":             "Space apps, marked as needing a new connection",
}

// AccountPurgeKept names the user columns the purge leaves, with why. The
// purge test reads it.
func AccountPurgeKept() map[string]string { return accountPurgeKept }

// purgeAccountDataTx runs inside CompleteAccountDeletion.
func purgeAccountDataTx(ctx context.Context, tx *sql.Tx, userID string) error {
	if err := handOverSharedSpacesTx(ctx, tx, userID); err != nil {
		return err
	}
	// Owned Spaces now have no other member; the Space purge removes them.
	if _, err := tx.ExecContext(ctx, `UPDATE spaces SET lifecycle_state='pending_deletion',
		deletion_requested_at=COALESCE(deletion_requested_at,NOW()),permanent_delete_after=NOW(),updated_at=NOW()
		WHERE owner_user_id=$1`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM space_members m USING spaces s
		WHERE m.space_id=s.id AND m.user_id=$1 AND s.owner_user_id<>$1`, userID); err != nil {
		return err
	}
	for _, target := range accountPurgeTables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+target.Table+` WHERE `+target.Column+`=$1`, userID); err != nil {
			return err
		}
	}
	return nil
}
