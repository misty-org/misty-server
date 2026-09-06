-- +goose Up
-- +goose StatementBegin
ALTER TABLE account_deletion_provider_resources
    ADD COLUMN execution_ciphertext BYTEA,
    ADD COLUMN execution_nonce BYTEA,
    ADD COLUMN execution_key_version SMALLINT,
    ADD COLUMN execution_client_id_hash TEXT,
    ADD CONSTRAINT account_deletion_provider_execution_complete CHECK
      (state<>'completed' OR (execution_ciphertext IS NULL AND execution_nonce IS NULL)),
    ADD CONSTRAINT account_deletion_provider_execution_envelope CHECK
      ((execution_ciphertext IS NULL AND execution_nonce IS NULL AND execution_key_version IS NULL)
        OR (execution_ciphertext IS NOT NULL AND execution_nonce IS NOT NULL AND execution_key_version IS NOT NULL
          AND octet_length(execution_ciphertext) BETWEEN 16 AND 2097168 AND octet_length(execution_nonce)=12 AND execution_key_version=1 AND execution_client_id_hash IS NOT NULL)),
    ADD CONSTRAINT account_deletion_provider_execution_client CHECK
      (execution_client_id_hash IS NULL OR execution_client_id_hash ~ '^[0-9a-f]{64}$');
-- +goose StatementEnd
-- +goose Down
-- Preserve acknowledged refresh material until remote revocation is resolved.
SELECT 1;
