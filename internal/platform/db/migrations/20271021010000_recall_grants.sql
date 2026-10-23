-- +goose Up
-- Agents may be allowed to search the person's Memory (recall.read). Grants
-- for it are scoped to Memory itself; forgetting is never remembered.
ALTER TABLE agent_grants DROP CONSTRAINT agent_grants_action_check;
ALTER TABLE agent_grants ADD CONSTRAINT agent_grants_action_check CHECK (action IN ('app.action', 'browser.send', 'browser.delete',
    'browser.publish', 'payment', 'space.admin', 'item.delete', 'schedule.remove', 'project.edit', 'project.install', 'git.push',
    'recall.read'));
ALTER TABLE agent_grants DROP CONSTRAINT agent_grants_scope_kind_check;
ALTER TABLE agent_grants ADD CONSTRAINT agent_grants_scope_kind_check CHECK (scope_kind IN ('connected_account', 'origin', 'merchant',
    'space', 'project', 'repo', 'schedule', 'recall'));

-- +goose Down
DELETE FROM agent_grants WHERE action = 'recall.read' OR scope_kind = 'recall';
ALTER TABLE agent_grants DROP CONSTRAINT agent_grants_action_check;
ALTER TABLE agent_grants ADD CONSTRAINT agent_grants_action_check CHECK (action IN ('app.action', 'browser.send', 'browser.delete',
    'browser.publish', 'payment', 'space.admin', 'item.delete', 'schedule.remove', 'project.edit', 'project.install', 'git.push'));
ALTER TABLE agent_grants DROP CONSTRAINT agent_grants_scope_kind_check;
ALTER TABLE agent_grants ADD CONSTRAINT agent_grants_scope_kind_check CHECK (scope_kind IN ('connected_account', 'origin', 'merchant',
    'space', 'project', 'repo', 'schedule'));
