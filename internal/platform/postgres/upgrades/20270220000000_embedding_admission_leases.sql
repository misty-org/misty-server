-- +goose Up
-- Durable provider-attempt ownership prevents concurrent retrieval workers from
-- issuing duplicate embeddings. Raw usage settles through the billing outbox.
ALTER TABLE ai_retrieval_chunks ADD COLUMN embedding_attempt_id uuid;
ALTER TABLE ai_retrieval_chunks ADD COLUMN embedding_lease_until timestamptz;
CREATE INDEX ai_retrieval_embedding_due ON ai_retrieval_chunks(embedding_lease_until,updated_at) WHERE embedding IS NULL;
-- +goose Down
-- An outstanding admitted provider call must not lose its attempt identity.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ai_retrieval_chunks WHERE embedding_lease_until>now()) THEN
  RAISE EXCEPTION 'embedding attempts are still active';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX ai_retrieval_embedding_due;
ALTER TABLE ai_retrieval_chunks DROP COLUMN embedding_attempt_id,DROP COLUMN embedding_lease_until;
