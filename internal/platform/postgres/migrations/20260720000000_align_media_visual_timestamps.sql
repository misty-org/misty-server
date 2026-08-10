-- +goose Up
-- +goose StatementBegin
-- Visual search used to expose the beginning of a +/-5 second relevance
-- window as the jump target. Preserve the interval end, but move the start to
-- the actual sampled frame so opening a result lands on the described scene.
UPDATE media_search_segments
SET start_ms = LEAST(end_ms - 1, start_ms + 5000),
    metadata = jsonb_set(
        COALESCE(metadata, '{}'::jsonb),
        '{frameTimestampMs}',
        to_jsonb(LEAST(end_ms - 1, start_ms + 5000)),
        true
    )
WHERE segment_kind = 'visual'
  AND NOT (COALESCE(metadata, '{}'::jsonb) ? 'frameTimestampMs');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE media_search_segments
SET start_ms = GREATEST(0, start_ms - 5000),
    metadata = COALESCE(metadata, '{}'::jsonb) - 'frameTimestampMs'
WHERE segment_kind = 'visual'
  AND COALESCE(metadata, '{}'::jsonb) ? 'frameTimestampMs';
-- +goose StatementEnd
