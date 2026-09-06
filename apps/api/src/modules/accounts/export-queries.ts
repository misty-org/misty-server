// Explicit projections are the portable-data boundary. Never select credential,
// token, private object-key or billing columns into an unfiltered public object.
const member = (alias: string) => `EXISTS(SELECT 1 FROM space_members m JOIN spaces s ON s.id=m.space_id
  WHERE m.space_id=${alias}.space_id AND m.user_id=$1 AND s.lifecycle_state='active')`;
const audience = (alias: string) => `(${alias}.audience_kind='space' OR EXISTS(SELECT 1 FROM space_conversation_members cm
  JOIN space_conversations c ON c.id=cm.conversation_id WHERE cm.conversation_id=${alias}.audience_conversation_id
  AND c.space_id=${alias}.space_id AND cm.actor_kind='person' AND cm.user_id=$1))`;
export const exportQueries = {
  spaces: `SELECT s.id,s.name,m.role,m.joined_at FROM space_members m JOIN spaces s ON s.id=m.space_id
    WHERE m.user_id=$1 AND s.lifecycle_state='active' ORDER BY m.joined_at,s.id`,
  documents: `SELECT 'note' AS kind,id,space_id,title_projection AS title,acl_version,created_at,updated_at FROM space_notes n
    WHERE creator_user_id=$1 AND lifecycle_state='active' AND ${member("n")} AND ${audience("n")}
    UNION ALL SELECT 'drawing',id,space_id,title,acl_version,created_at,updated_at FROM space_drawings d
    WHERE creator_user_id=$1 AND lifecycle_state='active' AND ${member("d")} AND ${audience("d")} ORDER BY created_at,id`,
  messages: `SELECT id,space_id,content,created_at,COALESCE(edited_at,created_at) AS updated_at FROM space_messages message
    WHERE sender_user_id=$1 AND sender_kind='person' AND ${member("message")}
    AND (conversation_id IS NULL OR EXISTS(SELECT 1 FROM space_conversation_members cm JOIN space_conversations c ON c.id=cm.conversation_id
      WHERE cm.conversation_id=message.conversation_id AND c.space_id=message.space_id AND cm.actor_kind='person' AND cm.user_id=$1)) ORDER BY created_at,id`,
  agents: `SELECT id,name,role,description,icon,avatar,instructions,model_mode,model_id,reasoning_effort,default_run_mode,enabled,version,created_at,updated_at,deleted_at
    FROM personal_agents WHERE owner_user_id=$1 ORDER BY created_at,id`,
  versions: `SELECT id,version,name,role,description,icon,avatar,instructions,model_mode,model_id,reasoning_effort,default_run_mode,checksum_sha256,created_at
    FROM personal_agent_versions WHERE agent_id=$1 ORDER BY version,id`,
  connections: `SELECT provider,name,account_id,account_display,uses_custom_oauth_client,created_at,updated_at FROM cloud_connections
    WHERE user_id=$1 AND revoked_at IS NULL ORDER BY created_at,id`,
  assets: `SELECT 'note' AS kind,a.id,a.note_id AS parent_id,a.display_name AS filename,b.r2_object_key AS object_key,
      COALESCE(b.server_detected_mime_type,b.client_declared_mime_type) AS mime_type,b.byte_size,b.sha256,a.created_at
    FROM space_note_assets a JOIN space_notes n ON n.id=a.note_id JOIN library_files f ON f.id=a.file_id JOIN library_blobs b ON b.id=f.blob_id
    WHERE n.creator_user_id=$1 AND n.lifecycle_state='active' AND a.lifecycle_state='ready' AND f.lifecycle_state='ready' AND b.lifecycle_state='ready'
      AND ${member("n")} AND ${audience("n")}
    UNION ALL SELECT 'drawing',a.id,a.drawing_id,a.display_name,b.r2_object_key,COALESCE(b.server_detected_mime_type,b.client_declared_mime_type),b.byte_size,b.sha256,a.created_at
    FROM space_drawing_assets a JOIN space_drawings d ON d.id=a.drawing_id JOIN library_files f ON f.id=a.file_id JOIN library_blobs b ON b.id=f.blob_id
    WHERE d.creator_user_id=$1 AND d.lifecycle_state='active' AND a.lifecycle_state='ready' AND f.lifecycle_state='ready' AND b.lifecycle_state='ready'
      AND ${member("d")} AND ${audience("d")}
    UNION ALL SELECT 'library',i.id,i.space_id,i.display_name,b.r2_object_key,COALESCE(b.server_detected_mime_type,b.client_declared_mime_type),b.byte_size,b.sha256,i.added_at
    FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id
    WHERE f.uploader_user_id=$1 AND i.lifecycle_state='ready' AND f.lifecycle_state='ready' AND b.lifecycle_state='ready'
      AND ${member("i")} AND ${audience("i")}
    UNION ALL SELECT 'message_attachment',a.id,COALESCE(a.message_id,a.space_id),a.display_name,b.r2_object_key,
      COALESCE(b.server_detected_mime_type,b.client_declared_mime_type),b.byte_size,b.sha256,a.created_at
    FROM space_message_attachments a JOIN library_files f ON f.id=a.file_id JOIN library_blobs b ON b.id=f.blob_id
    WHERE a.uploader_user_id=$1 AND a.lifecycle_state='ready' AND f.lifecycle_state='ready' AND b.lifecycle_state='ready' AND ${member("a")}
      AND (a.message_id IS NULL OR EXISTS(SELECT 1 FROM space_messages message WHERE message.id=a.message_id AND message.space_id=a.space_id
        AND (message.conversation_id IS NULL OR EXISTS(SELECT 1 FROM space_conversation_members cm JOIN space_conversations c ON c.id=cm.conversation_id
          WHERE cm.conversation_id=message.conversation_id AND c.space_id=message.space_id AND cm.actor_kind='person' AND cm.user_id=$1)))) ORDER BY created_at,id`,
};

// Space/account locks precede content locks. Lock every asset row before file
// rows, and every file before blobs, matching storage retention's lock order.
export const exportContentLocks = [
  ...(["note", "drawing"] as const).map(kind => {
    const table = kind === "note" ? "space_notes" : "space_drawings";
    return `SELECT d.id FROM ${table} d WHERE d.creator_user_id=$1 AND ${member("d")} ORDER BY d.id FOR SHARE OF d`;
  }),
  `SELECT message.id FROM space_messages message WHERE ${member("message")} AND (sender_user_id=$1 OR EXISTS(
    SELECT 1 FROM space_message_attachments a WHERE a.message_id=message.id AND a.uploader_user_id=$1)) ORDER BY message.id FOR SHARE OF message`,
  ...(["note", "drawing"] as const).map(kind => {
    const table = kind === "note" ? "space_notes" : "space_drawings";
    return `SELECT a.id FROM space_${kind}_assets a JOIN ${table} d ON d.id=a.${kind}_id WHERE d.creator_user_id=$1
      AND d.lifecycle_state='active' AND ${member("d")} AND ${audience("d")} ORDER BY a.id FOR SHARE OF a`;
  }),
  `SELECT i.id FROM space_library_items i JOIN library_files f ON f.id=i.file_id WHERE f.uploader_user_id=$1 AND ${member("i")}
    AND ${audience("i")} ORDER BY i.id FOR SHARE OF i`,
  `SELECT a.id FROM space_message_attachments a WHERE a.uploader_user_id=$1 AND ${member("a")} ORDER BY a.id FOR SHARE OF a`,
  `SELECT f.id FROM library_files f JOIN library_blobs b ON b.id=f.blob_id WHERE b.r2_object_key IN (SELECT object_key FROM (${exportQueries.assets}) a)
    ORDER BY f.id FOR SHARE OF f`,
  `SELECT b.id FROM library_blobs b WHERE b.r2_object_key IN (SELECT object_key FROM (${exportQueries.assets}) a) ORDER BY b.id FOR SHARE OF b`,
];
