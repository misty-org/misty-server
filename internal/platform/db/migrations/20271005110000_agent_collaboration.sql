-- +goose Up
-- Collaboration state on a Misty conversation: whether it plans or acts, the
-- questions an agent asked, the plans it proposed and the goal it pursues.
-- Every row belongs to the conversation's owner.

-- Plan mode is read-only; Act mode does the work. One row per conversation.
CREATE TABLE ai_conversation_modes (
    conversation_id text PRIMARY KEY,
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mode text NOT NULL CHECK (mode IN ('act', 'plan')),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Structured questions an agent asked inside one run. A run has at most one
-- open set; answers are validated against the stored options and accepted once.
CREATE TABLE agent_question_sets (
    id text PRIMARY KEY CHECK (id ~ '^question_[0-9a-f-]{36}$'),
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    run_id text NOT NULL,
    call_id text NOT NULL CHECK (length(call_id) BETWEEN 1 AND 200),
    conversation_id text NOT NULL,
    questions jsonb NOT NULL CHECK (jsonb_typeof(questions) = 'array'),
    answers jsonb CHECK (answers IS NULL OR jsonb_typeof(answers) = 'array'),
    state text NOT NULL CHECK (state IN ('pending', 'answered', 'superseded', 'canceled', 'expired')),
    -- The asking run stopped waiting; a later answer continues the conversation once.
    handed_off boolean NOT NULL DEFAULT false,
    continued boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    answered_at timestamptz,
    expires_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX agent_question_sets_call ON agent_question_sets(owner_user_id, run_id, call_id);
CREATE UNIQUE INDEX agent_question_sets_open_run ON agent_question_sets(owner_user_id, run_id) WHERE state = 'pending';
CREATE INDEX agent_question_sets_conversation ON agent_question_sets(owner_user_id, conversation_id, created_at DESC);

-- Proposed plans. A revision is a new version that supersedes the previous one;
-- step progress lives with the version that was approved.
CREATE TABLE agent_plans (
    id text PRIMARY KEY CHECK (id ~ '^plan_[0-9a-f-]{36}$'),
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    conversation_id text NOT NULL,
    version int NOT NULL CHECK (version >= 1),
    run_id text NOT NULL DEFAULT '',
    author text NOT NULL CHECK (author IN ('agent', 'user')),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    progress jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(progress) = 'object'),
    state text NOT NULL CHECK (state IN ('proposed', 'approved', 'superseded', 'rejected', 'completed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    approved_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX agent_plans_version ON agent_plans(owner_user_id, conversation_id, version);
CREATE UNIQUE INDEX agent_plans_current ON agent_plans(owner_user_id, conversation_id) WHERE state IN ('proposed', 'approved');

-- A persistent objective. At most one goal is pursued or paused per conversation.
CREATE TABLE agent_goals (
    id text PRIMARY KEY CHECK (id ~ '^goal_[0-9a-f-]{36}$'),
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    conversation_id text NOT NULL,
    objective text NOT NULL CHECK (length(objective) BETWEEN 1 AND 2000),
    success_criteria jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(success_criteria) = 'array'),
    status text NOT NULL CHECK (status IN ('pursuing', 'paused', 'achieved', 'unmet', 'budget_limited', 'cleared')),
    -- Model tokens (input plus output) across the goal's runs; the account's AI
    -- limit still applies to every run on top of this budget.
    budget_tokens bigint NOT NULL CHECK (budget_tokens BETWEEN 10000 AND 50000000),
    used_tokens bigint NOT NULL DEFAULT 0 CHECK (used_tokens >= 0),
    continuation_count int NOT NULL DEFAULT 0 CHECK (continuation_count >= 0),
    max_continuations int NOT NULL DEFAULT 20 CHECK (max_continuations BETWEEN 1 AND 50),
    last_report jsonb,
    last_invocation_id text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX agent_goals_one_active ON agent_goals(owner_user_id, conversation_id) WHERE status IN ('pursuing', 'paused');
CREATE INDEX agent_goals_conversation ON agent_goals(owner_user_id, conversation_id, created_at DESC);

ALTER TABLE ai_conversation_modes ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_conversation_modes FORCE ROW LEVEL SECURITY;
CREATE POLICY ai_conversation_modes_owner ON ai_conversation_modes
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
ALTER TABLE agent_question_sets ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_question_sets FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_question_sets_owner ON agent_question_sets
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
ALTER TABLE agent_plans ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_plans FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_plans_owner ON agent_plans
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
ALTER TABLE agent_goals ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_goals FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_goals_owner ON agent_goals
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON ai_conversation_modes, agent_question_sets, agent_plans, agent_goals TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS agent_goals;
DROP TABLE IF EXISTS agent_plans;
DROP TABLE IF EXISTS agent_question_sets;
DROP TABLE IF EXISTS ai_conversation_modes;
