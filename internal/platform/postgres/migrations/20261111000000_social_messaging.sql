-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';

-- Social is the provider-neutral messaging layer. Existing Misty, Discord,
-- and Slack rows remain readable while new Instagram conversations can be
-- introduced without rewriting historical provenance.
ALTER TABLE space_conversations DROP CONSTRAINT IF EXISTS space_conversations_origin_check;
ALTER TABLE space_conversations ADD CONSTRAINT space_conversations_origin_check
    CHECK (origin IN ('misty','discord','instagram','slack'));
CREATE UNIQUE INDEX space_conversations_instagram_resource_idx
    ON space_conversations(space_id,external_resource_id)
    WHERE origin='instagram' AND external_resource_id<>'';

CREATE TABLE social_bindings (
    id TEXT PRIMARY KEY CHECK (id ~ '^social_binding_[0-9a-f-]{36}$'),
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    connection_id TEXT NOT NULL REFERENCES connected_accounts(id) ON DELETE CASCADE,
    connected_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    conversation_id TEXT REFERENCES space_conversations(id) ON DELETE SET NULL,
    provider TEXT NOT NULL CHECK (provider IN ('discord','instagram')),
    external_resource_id TEXT NOT NULL CHECK (char_length(external_resource_id) BETWEEN 1 AND 320),
    external_parent_id TEXT NOT NULL DEFAULT '' CHECK (char_length(external_parent_id)<=320),
    display_name TEXT NOT NULL DEFAULT '' CHECK (char_length(display_name)<=320),
    direction TEXT NOT NULL DEFAULT 'two_way' CHECK (direction IN ('two_way','inbound','outbound')),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','syncing','active','needs_attention','disabled')),
    capabilities JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(capabilities)='object'),
    sync_cursor TEXT NOT NULL DEFAULT '' CHECK (char_length(sync_cursor)<=1000),
    last_synced_at TIMESTAMPTZ,
    last_error_code TEXT NOT NULL DEFAULT '' CHECK (char_length(last_error_code)<=120),
    disabled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(space_id,provider,external_resource_id)
);
CREATE INDEX social_bindings_connection_idx ON social_bindings(connection_id,status);
CREATE INDEX social_bindings_resource_idx ON social_bindings(provider,external_resource_id)
    WHERE disabled_at IS NULL;

CREATE TABLE social_identities (
    id TEXT PRIMARY KEY CHECK (id ~ '^social_identity_[0-9a-f-]{36}$'),
    binding_id TEXT NOT NULL REFERENCES social_bindings(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('discord','instagram')),
    external_user_id TEXT NOT NULL CHECK (char_length(external_user_id) BETWEEN 1 AND 320),
    display_name TEXT NOT NULL DEFAULT '' CHECK (char_length(display_name)<=320),
    handle TEXT NOT NULL DEFAULT '' CHECK (char_length(handle)<=320),
    avatar_url TEXT NOT NULL DEFAULT '' CHECK (char_length(avatar_url)<=2048),
    kind TEXT NOT NULL DEFAULT 'person' CHECK (kind IN ('person','business','bot')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(binding_id,external_user_id)
);

ALTER TABLE space_messages
    ADD COLUMN social_provider TEXT CHECK (social_provider IS NULL OR social_provider IN ('misty','discord','instagram')),
    ADD COLUMN social_external_id TEXT NOT NULL DEFAULT '' CHECK (char_length(social_external_id)<=320),
    ADD COLUMN social_external_conversation_id TEXT NOT NULL DEFAULT '' CHECK (char_length(social_external_conversation_id)<=320),
    ADD COLUMN social_identity_id TEXT REFERENCES social_identities(id) ON DELETE SET NULL,
    ADD COLUMN social_direction TEXT CHECK (social_direction IS NULL OR social_direction IN ('inbound','outbound')),
    ADD COLUMN social_delivery_state TEXT CHECK (social_delivery_state IS NULL OR social_delivery_state IN ('queued','sending','sent','delivered','read','failed','cancelled'));
CREATE UNIQUE INDEX space_messages_social_external_idx
    ON space_messages(space_id,social_provider,social_external_id)
    WHERE social_provider IS NOT NULL AND social_external_id<>'';

CREATE TABLE social_send_authorities (
    id TEXT PRIMARY KEY CHECK (id ~ '^social_authority_[0-9a-f-]{36}$'),
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    connection_id TEXT NOT NULL REFERENCES connected_accounts(id) ON DELETE CASCADE,
    binding_id TEXT REFERENCES social_bindings(id) ON DELETE CASCADE,
    allow_manual BOOLEAN NOT NULL DEFAULT TRUE,
    allow_scheduled BOOLEAN NOT NULL DEFAULT FALSE,
    allow_automation BOOLEAN NOT NULL DEFAULT FALSE,
    hourly_limit INTEGER NOT NULL DEFAULT 5 CHECK (hourly_limit BETWEEN 1 AND 100),
    daily_limit INTEGER NOT NULL DEFAULT 25 CHECK (daily_limit BETWEEN 1 AND 1000),
    quiet_hours JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(quiet_hours)='object'),
    timezone TEXT NOT NULL DEFAULT 'UTC' CHECK (char_length(timezone)<=80),
    approved_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX social_send_authorities_active_idx
    ON social_send_authorities(user_id,connection_id,COALESCE(binding_id,'')) WHERE revoked_at IS NULL;

CREATE TABLE social_automation_rules (
    id TEXT PRIMARY KEY CHECK (id ~ '^social_rule_[0-9a-f-]{36}$'),
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    binding_id TEXT NOT NULL REFERENCES social_bindings(id) ON DELETE CASCADE,
    conversation_id TEXT REFERENCES space_conversations(id) ON DELETE CASCADE,
    authority_id TEXT NOT NULL REFERENCES social_send_authorities(id) ON DELETE RESTRICT,
    created_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    instructions TEXT NOT NULL CHECK (char_length(instructions) BETWEEN 1 AND 10000),
    tone TEXT NOT NULL DEFAULT '' CHECK (char_length(tone)<=500),
    confidence_threshold NUMERIC(4,3) NOT NULL DEFAULT 0.800 CHECK (confidence_threshold BETWEEN 0 AND 1),
    max_replies_per_hour INTEGER NOT NULL DEFAULT 5 CHECK (max_replies_per_hour BETWEEN 1 AND 100),
    max_replies_per_day INTEGER NOT NULL DEFAULT 25 CHECK (max_replies_per_day BETWEEN 1 AND 1000),
    cooldown_seconds INTEGER NOT NULL DEFAULT 120 CHECK (cooldown_seconds BETWEEN 30 AND 86400),
    max_unanswered_replies INTEGER NOT NULL DEFAULT 2 CHECK (max_unanswered_replies BETWEEN 1 AND 10),
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    paused_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE social_scheduled_messages (
    id TEXT PRIMARY KEY CHECK (id ~ '^social_scheduled_[0-9a-f-]{36}$'),
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    binding_id TEXT NOT NULL REFERENCES social_bindings(id) ON DELETE CASCADE,
    conversation_id TEXT NOT NULL REFERENCES space_conversations(id) ON DELETE CASCADE,
    authority_id TEXT NOT NULL REFERENCES social_send_authorities(id) ON DELETE RESTRICT,
    created_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content JSONB NOT NULL CHECK (jsonb_typeof(content)='array'),
    scheduled_at TIMESTAMPTZ NOT NULL,
    timezone TEXT NOT NULL DEFAULT 'UTC' CHECK (char_length(timezone)<=80),
    status TEXT NOT NULL DEFAULT 'scheduled' CHECK (status IN ('scheduled','queued','sent','failed','cancelled')),
    outbound_command_id TEXT,
    last_error_code TEXT NOT NULL DEFAULT '' CHECK (char_length(last_error_code)<=120),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX social_scheduled_messages_due_idx ON social_scheduled_messages(scheduled_at)
    WHERE status='scheduled';

CREATE TABLE social_outbound_commands (
    id TEXT PRIMARY KEY CHECK (id ~ '^social_command_[0-9a-f-]{36}$'),
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    binding_id TEXT NOT NULL REFERENCES social_bindings(id) ON DELETE CASCADE,
    conversation_id TEXT NOT NULL REFERENCES space_conversations(id) ON DELETE CASCADE,
    authority_id TEXT REFERENCES social_send_authorities(id) ON DELETE SET NULL,
    requested_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('manual','scheduled','automation')),
    content JSONB NOT NULL CHECK (jsonb_typeof(content)='array'),
    idempotency_key TEXT NOT NULL CHECK (char_length(idempotency_key) BETWEEN 8 AND 200),
    state TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','sending','sent','failed','cancelled')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 20),
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_expires_at TIMESTAMPTZ,
    provider_receipt JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(provider_receipt)='object'),
    last_error_code TEXT NOT NULL DEFAULT '' CHECK (char_length(last_error_code)<=120),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(idempotency_key)
);
CREATE INDEX social_outbound_commands_ready_idx ON social_outbound_commands(available_at,created_at)
    WHERE state='queued';

ALTER TABLE social_scheduled_messages ADD CONSTRAINT social_scheduled_messages_command_fk
    FOREIGN KEY (outbound_command_id) REFERENCES social_outbound_commands(id) ON DELETE SET NULL;

CREATE TABLE social_automation_runs (
    id TEXT PRIMARY KEY CHECK (id ~ '^social_run_[0-9a-f-]{36}$'),
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    rule_id TEXT NOT NULL REFERENCES social_automation_rules(id) ON DELETE CASCADE,
    trigger_message_id TEXT REFERENCES space_messages(id) ON DELETE SET NULL,
    outbound_command_id TEXT REFERENCES social_outbound_commands(id) ON DELETE SET NULL,
    decision TEXT NOT NULL CHECK (decision IN ('reply','draft','skip','blocked')),
    reason_code TEXT NOT NULL DEFAULT '' CHECK (char_length(reason_code)<=120),
    confidence NUMERIC(4,3),
    draft_content JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(draft_content)='array'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE social_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE social_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE social_identities ENABLE ROW LEVEL SECURITY;
ALTER TABLE social_identities FORCE ROW LEVEL SECURITY;
ALTER TABLE social_send_authorities ENABLE ROW LEVEL SECURITY;
ALTER TABLE social_send_authorities FORCE ROW LEVEL SECURITY;
ALTER TABLE social_automation_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE social_automation_rules FORCE ROW LEVEL SECURITY;
ALTER TABLE social_scheduled_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE social_scheduled_messages FORCE ROW LEVEL SECURITY;
ALTER TABLE social_outbound_commands ENABLE ROW LEVEL SECURITY;
ALTER TABLE social_outbound_commands FORCE ROW LEVEL SECURITY;
ALTER TABLE social_automation_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE social_automation_runs FORCE ROW LEVEL SECURITY;

CREATE POLICY social_bindings_member ON social_bindings FOR ALL
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY social_identities_member ON social_identities FOR ALL
    USING (misty_rls_is_service() OR EXISTS(SELECT 1 FROM social_bindings b WHERE b.id=binding_id AND misty_is_space_member(b.space_id)))
    WITH CHECK (misty_rls_is_service() OR EXISTS(SELECT 1 FROM social_bindings b WHERE b.id=binding_id AND misty_is_space_member(b.space_id)));
CREATE POLICY social_send_authorities_owner ON social_send_authorities FOR ALL
    USING (misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());
CREATE POLICY social_automation_rules_member ON social_automation_rules FOR ALL
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY social_scheduled_messages_member ON social_scheduled_messages FOR ALL
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY social_outbound_commands_member ON social_outbound_commands FOR ALL
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY social_automation_runs_member ON social_automation_runs FOR SELECT
    USING (misty_rls_is_service() OR misty_is_space_member(space_id));

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON social_bindings,social_identities,
            social_send_authorities,social_automation_rules,social_scheduled_messages,
            social_outbound_commands,social_automation_runs TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: social consent, delivery, and automation records are audit data.
SELECT 1;
