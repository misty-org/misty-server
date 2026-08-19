-- +goose Up
ALTER TABLE space_note_control_outbox
    DROP CONSTRAINT IF EXISTS space_note_control_outbox_command_check;
ALTER TABLE space_note_control_outbox
    ADD CONSTRAINT space_note_control_outbox_command_check
    CHECK (command IN ('acl','disconnect','purge','bootstrap','replace_markdown'));

-- +goose Down
ALTER TABLE space_note_control_outbox
    DROP CONSTRAINT IF EXISTS space_note_control_outbox_command_check;
ALTER TABLE space_note_control_outbox
    ADD CONSTRAINT space_note_control_outbox_command_check
    CHECK (command IN ('acl','disconnect','purge','bootstrap'));
