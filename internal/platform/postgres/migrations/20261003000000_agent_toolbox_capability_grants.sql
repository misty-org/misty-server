-- +goose Up
-- +goose StatementBegin
-- Give Agent Toolbox capability_grants an exact, risk-bound shape. Before this
-- migration there was no API or enforcement for this column, so every empty
-- array is legacy state rather than an intentional deny-all selection.

ALTER TABLE space_agent_instances
    ALTER COLUMN capability_grants SET DEFAULT '[
        {"capability":"calendar.query","risk":"read"},
        {"capability":"library.search","risk":"read"},
        {"capability":"messages.search","risk":"read"},
        {"capability":"provider.discord.query","risk":"read"},
        {"capability":"provider.discord.write","risk":"write"},
        {"capability":"provider.google.query","risk":"read"},
        {"capability":"provider.notion.query","risk":"read"},
        {"capability":"provider.slack.query","risk":"read"},
        {"capability":"provider.slack.write","risk":"write"},
        {"capability":"tasks.create","risk":"write"},
        {"capability":"tasks.query","risk":"read"},
        {"capability":"tasks.update","risk":"write"}
    ]'::jsonb;

UPDATE space_agent_instances
SET capability_grants = DEFAULT,
    updated_at = NOW()
WHERE capability_grants = '[]'::jsonb;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE space_agent_instances
    ALTER COLUMN capability_grants SET DEFAULT '[]'::jsonb;
