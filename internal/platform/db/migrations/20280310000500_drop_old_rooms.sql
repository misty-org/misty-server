-- +goose Up
SELECT set_config('app.rls_mode', 'service', true);

DROP TABLE IF EXISTS space_code_session_control_outbox;
DROP TABLE IF EXISTS space_code_sessions;
DROP TABLE IF EXISTS space_collab_room_control_outbox;
DROP TABLE IF EXISTS space_collab_room_users;
DROP TABLE IF EXISTS space_collab_rooms;
DROP TABLE IF EXISTS space_note_control_outbox;

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);
