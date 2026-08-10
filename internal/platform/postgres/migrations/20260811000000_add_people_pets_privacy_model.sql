-- +goose Up
-- +goose StatementBegin
CREATE TABLE space_library_intelligence_policies (
    space_id TEXT PRIMARY KEY REFERENCES spaces(id) ON DELETE CASCADE,
    faces_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    pets_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    enabled_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE space_people
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'person' CHECK (kind IN ('person','pet')),
    ADD COLUMN created_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN merged_into_id TEXT REFERENCES space_people(id) ON DELETE SET NULL;

ALTER TABLE space_person_observations DROP CONSTRAINT space_person_observations_pkey;
ALTER TABLE space_person_observations ALTER COLUMN derivative_id DROP NOT NULL;
ALTER TABLE space_person_observations
    ADD COLUMN id TEXT,
    ADD COLUMN source TEXT NOT NULL DEFAULT 'automatic' CHECK (source IN ('automatic','manual'));
UPDATE space_person_observations SET id='observation_'||replace(gen_random_uuid()::text,'-','') WHERE id IS NULL;
ALTER TABLE space_person_observations ALTER COLUMN id SET NOT NULL;
ALTER TABLE space_person_observations ADD PRIMARY KEY(id);
CREATE UNIQUE INDEX space_person_observations_identity_idx
    ON space_person_observations(person_id,space_library_item_id,derivative_id) NULLS NOT DISTINCT;
CREATE INDEX space_person_observations_item_idx ON space_person_observations(space_library_item_id);

ALTER TABLE space_library_intelligence_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_library_intelligence_policies FORCE ROW LEVEL SECURITY;
CREATE POLICY intelligence_policies_read ON space_library_intelligence_policies FOR SELECT
    USING (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY intelligence_policies_write ON space_library_intelligence_policies FOR ALL
    USING (misty_rls_is_service() OR misty_is_space_owner(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_owner(space_id));

DO $grant$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_library_intelligence_policies TO misty_app;
    END IF;
END $grant$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS space_library_intelligence_policies;
DROP INDEX IF EXISTS space_person_observations_item_idx;
DROP INDEX IF EXISTS space_person_observations_identity_idx;
ALTER TABLE space_person_observations DROP CONSTRAINT space_person_observations_pkey;
DELETE FROM space_person_observations WHERE derivative_id IS NULL;
ALTER TABLE space_person_observations ALTER COLUMN derivative_id SET NOT NULL;
ALTER TABLE space_person_observations DROP COLUMN source,DROP COLUMN id;
ALTER TABLE space_person_observations ADD PRIMARY KEY(person_id,space_library_item_id,derivative_id);
ALTER TABLE space_people DROP COLUMN merged_into_id,DROP COLUMN created_by_user_id,DROP COLUMN kind;
-- +goose StatementEnd
