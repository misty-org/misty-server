-- +goose Up
-- Search in Spaces is Postgres full-text search on the words people wrote, not
-- meaning. Notes already have their
-- index, space_notes_search_idx; messages get theirs here. Only the text and
-- mention labels of a message's spans count: its span types and ids are not
-- words anyone wrote, and "text" would otherwise match every message.
ALTER TABLE space_messages ADD COLUMN search_tsv tsvector GENERATED ALWAYS AS (
    to_tsvector('simple'::regconfig,
        jsonb_path_query_array(content, '$[*].text') || jsonb_path_query_array(content, '$[*].label'))
) STORED;
CREATE INDEX space_messages_search_idx ON space_messages USING gin (search_tsv);

-- +goose Down
DROP INDEX space_messages_search_idx;
ALTER TABLE space_messages DROP COLUMN search_tsv;
