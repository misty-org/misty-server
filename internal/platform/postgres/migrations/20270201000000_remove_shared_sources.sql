-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

-- Release only the snapshot system's storage charges, including recovery data.
WITH removed AS (
 DELETE FROM space_storage_contributions c USING space_source_items s
 WHERE c.space_id=s.space_id AND c.source_kind='import' AND c.source_id=s.id
 RETURNING c.space_id,c.logical_bytes,c.state
), totals AS (
 SELECT space_id,SUM(logical_bytes) AS bytes FROM removed
 WHERE state IN ('active','recovery') GROUP BY space_id
)
UPDATE space_storage_usage u
SET used_bytes=GREATEST(0,u.used_bytes-t.bytes),version=version+1,updated_at=NOW()
FROM totals t WHERE u.space_id=t.space_id;

-- Native notes produced by an explicit Copy action are independent user content.
-- Drop the copy receipts, never the notes or their document state.
DROP TABLE provider_source_copies;
DROP TABLE provider_source_watches;
DROP TABLE provider_source_subscriptions;
DROP TABLE provider_source_previews;
DROP TABLE space_source_items;

-- The older publication table also backs explicit app bindings. Preserve those
-- bindings and their content; purge only generic publications. Content deletion
-- uses the existing trigger to remove corresponding search/vector records.
DELETE FROM provider_shared_resources r
WHERE NOT EXISTS(SELECT 1 FROM github_code_workspaces w WHERE w.shared_resource_id=r.id)
  AND NOT EXISTS(SELECT 1 FROM figma_space_bindings b WHERE b.shared_resource_id=r.id)
  AND NOT EXISTS(SELECT 1 FROM space_slack_links l WHERE l.shared_resource_id=r.id);
DELETE FROM space_events WHERE event_type IN
 ('integration.resource_published','integration.resources_replaced','integration.resource_disabled');

-- Retire the feature's permissions without changing other app grants/accounts.
UPDATE user_app_installations
SET granted_scopes=granted_scopes-ARRAY['sources.read','sources.write'],updated_at=NOW()
WHERE granted_scopes ?| ARRAY['sources.read','sources.write'];
UPDATE app_runtime_sessions SET scopes=scopes-ARRAY['sources.read','sources.write']
WHERE scopes ?| ARRAY['sources.read','sources.write'];
-- +goose StatementEnd

-- +goose Down
-- Removed snapshots cannot be reconstructed. Applied migration history stays intact.
SELECT 1;
