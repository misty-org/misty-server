-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);
SET CONSTRAINTS ALL DEFERRED;

-- Personal Spaces were pre-beta scaffolding. Files remains the private user
-- environment; collaborative Spaces are now created intentionally.
DELETE FROM space_agent_instance_workflows aiw
USING space_workflow_versions wv
JOIN spaces s ON s.id=wv.space_id
WHERE aiw.workflow_version_id=wv.id AND s.is_personal;
DELETE FROM space_agent_version_workflows avw
USING space_workflow_versions wv
JOIN spaces s ON s.id=wv.space_id
WHERE avw.workflow_version_id=wv.id AND s.is_personal;
UPDATE space_agents a
SET active_workflow_version_id=NULL
FROM spaces s
WHERE s.id=a.space_id AND s.is_personal;
DELETE FROM spaces WHERE is_personal;
SET CONSTRAINTS ALL IMMEDIATE;
ALTER TABLE spaces DROP COLUMN is_personal;

-- Space creation setup is deliberately lightweight and resumable. It records
-- intent only; provider credentials and selected resources remain in their
-- existing purpose-built tables.
CREATE TABLE space_setup_integrations (
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('google','discord','notion')),
    status TEXT NOT NULL DEFAULT 'selected'
        CHECK (status IN ('selected','authorized','configured','skipped')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(space_id,provider)
);
ALTER TABLE space_setup_integrations ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_setup_integrations FORCE ROW LEVEL SECURITY;
CREATE POLICY space_setup_integrations_read ON space_setup_integrations
    FOR SELECT USING (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_setup_integrations_owner_write ON space_setup_integrations
    FOR ALL USING (misty_rls_is_service() OR misty_is_space_owner(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_owner(space_id));

CREATE TABLE space_creation_requests (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL,
    request_fingerprint TEXT NOT NULL,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id,idempotency_key)
);
ALTER TABLE space_creation_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_creation_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY space_creation_requests_owner ON space_creation_requests
    FOR ALL USING (misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());

DO $grant$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE
            ON space_setup_integrations,space_creation_requests TO misty_app;
    END IF;
END $grant$;

-- Provider-backed conversations remain ordinary Chat conversations, but carry
-- stable provenance so the desktop can group them without title heuristics.
ALTER TABLE space_conversations
    ADD COLUMN origin TEXT NOT NULL DEFAULT 'misty' CHECK (origin IN ('misty','discord')),
    ADD COLUMN integration_id TEXT REFERENCES space_integrations(id) ON DELETE SET NULL,
    ADD COLUMN external_resource_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN external_display_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN integration_status TEXT NOT NULL DEFAULT 'active'
        CHECK (integration_status IN ('active','disconnected')),
    ADD COLUMN visible_to_space BOOLEAN NOT NULL DEFAULT FALSE;
CREATE UNIQUE INDEX space_conversations_discord_resource_idx
    ON space_conversations(space_id,external_resource_id)
    WHERE origin='discord' AND external_resource_id<>'';
CREATE POLICY space_conversations_disconnected_discord_owner_delete
    ON space_conversations FOR DELETE
    USING (
        misty_rls_is_service() OR (
            origin='discord' AND integration_status='disconnected'
            AND misty_is_space_owner(space_id)
        )
    );

-- A provider conversation is visible to every current Space member. Selected
-- Misty direct/group conversations retain their existing member list.
CREATE OR REPLACE FUNCTION misty_is_space_conversation_member(candidate_conversation_id TEXT)
RETURNS BOOLEAN
LANGUAGE SQL
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
    SELECT EXISTS (
        SELECT 1
        FROM space_conversations c
        JOIN space_members sm ON sm.space_id=c.space_id
        WHERE c.id=candidate_conversation_id
          AND sm.user_id=misty_rls_user_id()
          AND (
              c.visible_to_space OR EXISTS (
                  SELECT 1 FROM space_conversation_members cm
                  WHERE cm.conversation_id=c.id AND cm.user_id=misty_rls_user_id()
              )
          )
    )
$$;

-- Invitations may target an email before an account exists. Tokens are stored
-- hashed and are independently revocable and single-use.
ALTER TABLE space_invitations
    DROP CONSTRAINT IF EXISTS space_invitations_space_id_invited_user_id_key,
    ALTER COLUMN invited_user_id DROP NOT NULL,
    ADD COLUMN invited_email TEXT,
    ADD COLUMN token_hash TEXT,
    ADD COLUMN delivery_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (delivery_status IN ('pending','sent','failed')),
    ADD COLUMN revoked_at TIMESTAMPTZ,
    ADD COLUMN consumed_at TIMESTAMPTZ,
    ADD COLUMN last_sent_at TIMESTAMPTZ;
UPDATE space_invitations i
SET invited_email=lower(u.email)
FROM users u
WHERE u.id=i.invited_user_id;
UPDATE space_invitations SET token_hash=md5(id || ':' || invited_email) WHERE token_hash IS NULL;
ALTER TABLE space_invitations
    ALTER COLUMN invited_email SET NOT NULL,
    ALTER COLUMN token_hash SET NOT NULL;
CREATE UNIQUE INDEX space_invitations_token_hash_idx ON space_invitations(token_hash);
CREATE UNIQUE INDEX space_invitations_active_email_idx
    ON space_invitations(space_id,lower(invited_email))
    WHERE revoked_at IS NULL AND consumed_at IS NULL;
CREATE INDEX space_invitations_active_user_idx
    ON space_invitations(invited_user_id,expires_at)
    WHERE revoked_at IS NULL AND consumed_at IS NULL;

-- Credentials stay encrypted and server-side. The installer, current owner,
-- and members using an explicitly shared resource may invoke them.
DROP POLICY IF EXISTS space_provider_credentials_owner ON space_provider_credentials;
CREATE POLICY space_provider_credentials_owner ON space_provider_credentials FOR ALL
    USING (
        misty_rls_is_service()
        OR user_id=misty_rls_user_id()
        OR EXISTS (
            SELECT 1 FROM spaces s
            WHERE s.id=space_provider_credentials.space_id
              AND s.owner_user_id=misty_rls_user_id()
        )
        OR EXISTS (
            SELECT 1 FROM provider_shared_resources r
            WHERE r.integration_id=space_provider_credentials.integration_id
              AND r.space_id=space_provider_credentials.space_id
              AND r.status='active'
              AND misty_is_space_member(r.space_id)
        )
    )
    WITH CHECK (
        misty_rls_is_service()
        OR user_id=misty_rls_user_id()
        OR EXISTS (
            SELECT 1 FROM spaces s
            WHERE s.id=space_provider_credentials.space_id
              AND s.owner_user_id=misty_rls_user_id()
        )
        OR EXISTS (
            SELECT 1 FROM provider_shared_resources r
            WHERE r.integration_id=space_provider_credentials.integration_id
              AND r.space_id=space_provider_credentials.space_id
              AND r.status='active'
              AND misty_is_space_member(r.space_id)
        )
    );

-- Native notes are collaborative Space documents. Membership grants edit
-- access; creator/owner retain destructive control.
ALTER TABLE space_notes
    DROP CONSTRAINT IF EXISTS space_notes_lifecycle_state_check,
    DROP CONSTRAINT IF EXISTS space_notes_check;
ALTER TABLE space_notes
    ADD CONSTRAINT space_notes_lifecycle_state_check
        CHECK (lifecycle_state IN ('active','archived','archived_creator_left','deleting')),
    ADD CONSTRAINT space_notes_archive_timestamp_check
        CHECK (
            (lifecycle_state='archived' AND archived_at IS NOT NULL AND purge_after IS NULL)
            OR lifecycle_state<>'archived'
        ),
    ADD CONSTRAINT space_notes_creator_left_timestamp_check
        CHECK (
            lifecycle_state<>'archived_creator_left'
            OR (archived_at IS NOT NULL AND purge_after IS NOT NULL)
        );
DROP POLICY IF EXISTS space_notes_access_policy ON space_notes;
CREATE POLICY space_notes_access_policy ON space_notes FOR ALL
    USING (misty_rls_is_service() OR (
        misty_is_space_member(space_id)
        AND (
            lifecycle_state='active'
            OR creator_user_id=misty_rls_user_id()
            OR EXISTS (
                SELECT 1 FROM spaces s
                WHERE s.id=space_notes.space_id AND s.owner_user_id=misty_rls_user_id()
            )
        )
    ))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
ALTER TABLE space_note_control_outbox
    DROP CONSTRAINT space_note_control_outbox_command_check,
    ADD CONSTRAINT space_note_control_outbox_command_check
        CHECK (command IN ('acl','disconnect','purge','bootstrap'));

-- Fixed member defaults: collaboration is on, administration is owner-only.
DELETE FROM space_member_permission_overrides;
DELETE FROM space_member_roles;
UPDATE space_roles
SET permissions='[
    "space.view",
    "messages.read",
    "messages.write",
    "attachments.upload",
    "library.view",
    "library.upload",
    "library.add",
    "library.edit",
    "library.download",
    "library.import",
    "storage.view_own_usage",
    "tasks.view",
    "tasks.manage"
]'::jsonb
WHERE is_everyone;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM space_invitations WHERE invited_user_id IS NULL;
DROP POLICY IF EXISTS space_provider_credentials_owner ON space_provider_credentials;
CREATE POLICY space_provider_credentials_owner ON space_provider_credentials FOR ALL
    USING (misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());
ALTER TABLE space_invitations
    DROP COLUMN IF EXISTS last_sent_at,
    DROP COLUMN IF EXISTS consumed_at,
    DROP COLUMN IF EXISTS revoked_at,
    DROP COLUMN IF EXISTS delivery_status,
    DROP COLUMN IF EXISTS token_hash,
    DROP COLUMN IF EXISTS invited_email;
ALTER TABLE space_invitations
    ALTER COLUMN invited_user_id SET NOT NULL,
    ADD CONSTRAINT space_invitations_space_id_invited_user_id_key
        UNIQUE (space_id,invited_user_id);

DROP INDEX IF EXISTS space_conversations_discord_resource_idx;
DROP POLICY IF EXISTS space_conversations_disconnected_discord_owner_delete ON space_conversations;
ALTER TABLE space_conversations
    DROP COLUMN IF EXISTS visible_to_space,
    DROP COLUMN IF EXISTS integration_status,
    DROP COLUMN IF EXISTS external_display_name,
    DROP COLUMN IF EXISTS external_resource_id,
    DROP COLUMN IF EXISTS integration_id,
    DROP COLUMN IF EXISTS origin;
DROP TABLE IF EXISTS space_setup_integrations;
DROP TABLE IF EXISTS space_creation_requests;

ALTER TABLE spaces ADD COLUMN is_personal BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE space_notes
    DROP CONSTRAINT IF EXISTS space_notes_archive_timestamp_check,
    DROP CONSTRAINT IF EXISTS space_notes_creator_left_timestamp_check,
    DROP CONSTRAINT IF EXISTS space_notes_lifecycle_state_check;
UPDATE space_notes SET lifecycle_state='archived_creator_left',
    purge_after=COALESCE(purge_after,NOW()+INTERVAL '30 days')
WHERE lifecycle_state='archived';
ALTER TABLE space_notes
    ADD CONSTRAINT space_notes_lifecycle_state_check
        CHECK (lifecycle_state IN ('active','archived_creator_left','deleting')),
    ADD CONSTRAINT space_notes_check
        CHECK (
            lifecycle_state<>'archived_creator_left'
            OR (archived_at IS NOT NULL AND purge_after IS NOT NULL)
        );
DROP POLICY IF EXISTS space_notes_access_policy ON space_notes;
CREATE POLICY space_notes_access_policy ON space_notes FOR ALL
    USING (misty_rls_is_service() OR (
        lifecycle_state='active' AND (
            creator_user_id=misty_rls_user_id()
            OR EXISTS (
                SELECT 1 FROM space_note_permissions p
                JOIN space_members m
                  ON m.space_id=space_notes.space_id AND m.user_id=p.user_id
                WHERE p.note_id=space_notes.id AND p.user_id=misty_rls_user_id()
            )
        )
    ))
    WITH CHECK (misty_rls_is_service() OR creator_user_id=misty_rls_user_id());
ALTER TABLE space_note_control_outbox
    DROP CONSTRAINT IF EXISTS space_note_control_outbox_command_check,
    ADD CONSTRAINT space_note_control_outbox_command_check
        CHECK (command IN ('acl','disconnect','purge'));
-- Personal Spaces and their deleted content are intentionally not recreated.
-- +goose StatementEnd
