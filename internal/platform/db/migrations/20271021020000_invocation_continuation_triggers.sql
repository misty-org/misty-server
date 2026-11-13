-- +goose Up
-- A turn Misty sends to carry on after the person answers a card (connect, approve, share a
-- screen) is stored as "continuation", and an automatic goal turn as "goal_continuation".
-- The check only allowed the original six kinds, so both were rejected with an internal error
-- (goal turns are already counted by trigger_kind in agent_goals.go).
ALTER TABLE ai_invocations DROP CONSTRAINT ai_invocations_trigger_kind_check;
ALTER TABLE ai_invocations ADD CONSTRAINT ai_invocations_trigger_kind_check
    CHECK (trigger_kind IN ('message', 'selection', 'object', 'schedule', 'event', 'handoff', 'continuation', 'goal_continuation'));

-- +goose Down
UPDATE ai_invocations SET trigger_kind = 'message' WHERE trigger_kind IN ('continuation', 'goal_continuation');
ALTER TABLE ai_invocations DROP CONSTRAINT ai_invocations_trigger_kind_check;
ALTER TABLE ai_invocations ADD CONSTRAINT ai_invocations_trigger_kind_check
    CHECK (trigger_kind IN ('message', 'selection', 'object', 'schedule', 'event', 'handoff'));
