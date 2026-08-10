-- +goose Up
-- +goose StatementBegin
SET LOCAL app.rls_mode = 'service';

-- Legacy device agents predate Spaces. Ensure every legacy owner has a
-- personal Space, then use it as the deterministic compatibility owner.
INSERT INTO security_domains(id,kind,owner_user_id)
SELECT 'sd_agent_' || md5(a.owner_user_id),'personal',a.owner_user_id
FROM (SELECT DISTINCT owner_user_id FROM agent_definitions) a
WHERE NOT EXISTS (
    SELECT 1 FROM security_domains d
    WHERE d.kind='personal' AND d.owner_user_id=a.owner_user_id
)
ON CONFLICT DO NOTHING;

INSERT INTO spaces(id,owner_user_id,name,is_personal,security_domain_id)
SELECT
    'space_agent_' || md5(a.owner_user_id),
    a.owner_user_id,
    LEFT(COALESCE(NULLIF(BTRIM(u.name),''),NULLIF(BTRIM(u.username),''),'My'),72) || '''s Space',
    TRUE,
    d.id
FROM (SELECT DISTINCT owner_user_id FROM agent_definitions) a
JOIN users u ON u.id=a.owner_user_id
JOIN security_domains d ON d.kind='personal' AND d.owner_user_id=a.owner_user_id
WHERE NOT EXISTS (
    SELECT 1 FROM spaces s
    WHERE s.owner_user_id=a.owner_user_id AND s.is_personal
)
ON CONFLICT DO NOTHING;

INSERT INTO space_members(space_id,user_id,role)
SELECT s.id,s.owner_user_id,'owner'
FROM spaces s
WHERE s.is_personal
  AND EXISTS (SELECT 1 FROM agent_definitions a WHERE a.owner_user_id=s.owner_user_id)
  AND NOT EXISTS (SELECT 1 FROM space_roles r WHERE r.space_id=s.id AND r.is_everyone)
ON CONFLICT DO NOTHING;

INSERT INTO space_roles(id,space_id,name,is_everyone,permissions)
SELECT
    'role_agent_' || md5(s.id),s.id,'@everyone',TRUE,
    '["space.view","messages.read","library.view","library.download","storage.view_own_usage","studio.view","studio.manage","agents.run"]'::jsonb
FROM spaces s
WHERE s.is_personal
  AND EXISTS (SELECT 1 FROM agent_definitions a WHERE a.owner_user_id=s.owner_user_id)
ON CONFLICT DO NOTHING;

ALTER TABLE agent_definitions ADD COLUMN space_id TEXT REFERENCES spaces(id) ON DELETE CASCADE;
UPDATE agent_definitions a
SET space_id=s.id
FROM spaces s
WHERE s.owner_user_id=a.owner_user_id AND s.is_personal;
ALTER TABLE agent_definitions ALTER COLUMN space_id SET NOT NULL;

ALTER TABLE agent_definitions DROP CONSTRAINT IF EXISTS agent_definitions_owner_user_id_device_id_scope_id_name_key;
ALTER TABLE agent_definitions ADD CONSTRAINT agent_definitions_space_device_scope_name_key
    UNIQUE(space_id,device_id,scope_id,name);
CREATE INDEX agent_definitions_space_idx ON agent_definitions(space_id,updated_at DESC) WHERE deleted_at IS NULL;

-- Access is now inherited from Space membership. Legacy per-agent grants are
-- intentionally retired rather than creating hidden secondary permissions.
DELETE FROM agent_members;

UPDATE space_roles
SET permissions = permissions
        || CASE WHEN permissions ? 'studio.view' THEN '[]'::jsonb ELSE '["studio.view"]'::jsonb END
        || CASE WHEN permissions ? 'studio.manage' THEN '[]'::jsonb ELSE '["studio.manage"]'::jsonb END
        || CASE WHEN permissions ? 'agents.run' THEN '[]'::jsonb ELSE '["agents.run"]'::jsonb END,
    version = version + 1,
    updated_at = NOW()
WHERE is_everyone
  AND NOT (permissions ? 'studio.view' AND permissions ? 'studio.manage' AND permissions ? 'agents.run');

DROP POLICY IF EXISTS agent_definitions_select_policy ON agent_definitions;
DROP POLICY IF EXISTS agent_definitions_owner_write_policy ON agent_definitions;
CREATE POLICY agent_definitions_space_select_policy ON agent_definitions FOR SELECT
    USING (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY agent_definitions_space_write_policy ON agent_definitions FOR ALL
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

DROP POLICY IF EXISTS agent_members_select_policy ON agent_members;
DROP POLICY IF EXISTS agent_members_owner_write_policy ON agent_members;
CREATE POLICY agent_members_space_policy ON agent_members FOR ALL
    USING (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM agent_definitions a
        WHERE a.id=agent_members.agent_id AND misty_is_space_member(a.space_id)
    ))
    WITH CHECK (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM agent_definitions a
        WHERE a.id=agent_members.agent_id AND misty_is_space_member(a.space_id)
    ));

DROP POLICY IF EXISTS agent_triggers_policy ON agent_triggers;
CREATE POLICY agent_triggers_space_policy ON agent_triggers FOR ALL
    USING (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM agent_definitions a
        WHERE a.id=agent_triggers.agent_id AND misty_is_space_member(a.space_id)
    ))
    WITH CHECK (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM agent_definitions a
        WHERE a.id=agent_triggers.agent_id AND misty_is_space_member(a.space_id)
    ));

DROP POLICY IF EXISTS agent_jobs_policy ON agent_jobs;
CREATE POLICY agent_jobs_space_policy ON agent_jobs FOR ALL
    USING (misty_rls_is_service() OR requester_user_id=misty_rls_user_id() OR EXISTS (
        SELECT 1 FROM agent_definitions a
        WHERE a.id=agent_jobs.agent_id AND misty_is_space_member(a.space_id)
    ))
    WITH CHECK (misty_rls_is_service() OR requester_user_id=misty_rls_user_id() OR EXISTS (
        SELECT 1 FROM agent_definitions a
        WHERE a.id=agent_jobs.agent_id AND misty_is_space_member(a.space_id)
    ));

DROP POLICY IF EXISTS agent_job_events_policy ON agent_job_events;
CREATE POLICY agent_job_events_space_policy ON agent_job_events FOR ALL
    USING (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM agent_jobs j JOIN agent_definitions a ON a.id=j.agent_id
        WHERE j.id=agent_job_events.job_id AND misty_is_space_member(a.space_id)
    ))
    WITH CHECK (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM agent_jobs j JOIN agent_definitions a ON a.id=j.agent_id
        WHERE j.id=agent_job_events.job_id AND misty_is_space_member(a.space_id)
    ));

DROP POLICY IF EXISTS agent_approvals_policy ON agent_approvals;
CREATE POLICY agent_approvals_space_policy ON agent_approvals FOR ALL
    USING (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM agent_jobs j JOIN agent_definitions a ON a.id=j.agent_id
        WHERE j.id=agent_approvals.job_id AND misty_is_space_member(a.space_id)
    ))
    WITH CHECK (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM agent_jobs j JOIN agent_definitions a ON a.id=j.agent_id
        WHERE j.id=agent_approvals.job_id AND misty_is_space_member(a.space_id)
    ));

DROP POLICY IF EXISTS agent_attachments_policy ON agent_attachments;
CREATE POLICY agent_attachments_space_policy ON agent_attachments FOR ALL
    USING (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM agent_jobs j JOIN agent_definitions a ON a.id=j.agent_id
        WHERE j.id=agent_attachments.job_id AND misty_is_space_member(a.space_id)
    ))
    WITH CHECK (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM agent_jobs j JOIN agent_definitions a ON a.id=j.agent_id
        WHERE j.id=agent_attachments.job_id AND misty_is_space_member(a.space_id)
    ));

DROP POLICY IF EXISTS agent_artifacts_policy ON agent_artifacts;
CREATE POLICY agent_artifacts_space_policy ON agent_artifacts FOR ALL
    USING (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM agent_jobs j JOIN agent_definitions a ON a.id=j.agent_id
        WHERE j.id=agent_artifacts.job_id AND misty_is_space_member(a.space_id)
    ))
    WITH CHECK (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM agent_jobs j JOIN agent_definitions a ON a.id=j.agent_id
        WHERE j.id=agent_artifacts.job_id AND misty_is_space_member(a.space_id)
    ));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL app.rls_mode = 'service';

DROP POLICY IF EXISTS agent_artifacts_space_policy ON agent_artifacts;
DROP POLICY IF EXISTS agent_attachments_space_policy ON agent_attachments;
DROP POLICY IF EXISTS agent_approvals_space_policy ON agent_approvals;
DROP POLICY IF EXISTS agent_job_events_space_policy ON agent_job_events;
DROP POLICY IF EXISTS agent_jobs_space_policy ON agent_jobs;
DROP POLICY IF EXISTS agent_triggers_space_policy ON agent_triggers;
DROP POLICY IF EXISTS agent_members_space_policy ON agent_members;
DROP POLICY IF EXISTS agent_definitions_space_write_policy ON agent_definitions;
DROP POLICY IF EXISTS agent_definitions_space_select_policy ON agent_definitions;

CREATE POLICY agent_definitions_select_policy ON agent_definitions FOR SELECT USING (misty_rls_is_service() OR owner_user_id=misty_rls_user_id());
CREATE POLICY agent_definitions_owner_write_policy ON agent_definitions FOR ALL USING (misty_rls_is_service() OR owner_user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR owner_user_id=misty_rls_user_id());
CREATE POLICY agent_members_select_policy ON agent_members FOR SELECT USING (misty_rls_is_service() OR owner_user_id=misty_rls_user_id() OR user_id=misty_rls_user_id());
CREATE POLICY agent_members_owner_write_policy ON agent_members FOR ALL USING (misty_rls_is_service() OR owner_user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR owner_user_id=misty_rls_user_id());
CREATE POLICY agent_triggers_policy ON agent_triggers FOR ALL USING (misty_rls_is_service() OR owner_user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR owner_user_id=misty_rls_user_id());
CREATE POLICY agent_jobs_policy ON agent_jobs FOR ALL USING (misty_rls_is_service() OR owner_user_id=misty_rls_user_id() OR requester_user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR owner_user_id=misty_rls_user_id() OR requester_user_id=misty_rls_user_id());
CREATE POLICY agent_job_events_policy ON agent_job_events FOR ALL USING (misty_rls_is_service() OR owner_user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR owner_user_id=misty_rls_user_id());
CREATE POLICY agent_approvals_policy ON agent_approvals FOR ALL USING (misty_rls_is_service() OR owner_user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR owner_user_id=misty_rls_user_id());
CREATE POLICY agent_attachments_policy ON agent_attachments FOR ALL USING (misty_rls_is_service() OR owner_user_id=misty_rls_user_id() OR requester_user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR owner_user_id=misty_rls_user_id() OR requester_user_id=misty_rls_user_id());
CREATE POLICY agent_artifacts_policy ON agent_artifacts FOR ALL USING (misty_rls_is_service() OR owner_user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR owner_user_id=misty_rls_user_id());

DROP INDEX IF EXISTS agent_definitions_space_idx;
ALTER TABLE agent_definitions DROP CONSTRAINT IF EXISTS agent_definitions_space_device_scope_name_key;
ALTER TABLE agent_definitions ADD CONSTRAINT agent_definitions_owner_user_id_device_id_scope_id_name_key UNIQUE(owner_user_id,device_id,scope_id,name);
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS space_id;

UPDATE space_roles
SET permissions = permissions - 'studio.view' - 'studio.manage' - 'agents.run',
    version = version + 1,
    updated_at = NOW()
WHERE is_everyone;
-- +goose StatementEnd
