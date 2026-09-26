-- +goose Up
-- An admission must survive a crash between the remote reservation and the
-- local receipt. Expired attempts can be replayed and released without running
-- their provider work. No charging policy is stored here.
CREATE TABLE billing_adapter_intents (
 account_id text NOT NULL,
 key text NOT NULL,
 admission jsonb NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','admitted','abandoned','recovered')),
 expires_at timestamptz NOT NULL DEFAULT now()+interval '2 minutes',
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(account_id,key)
);
CREATE INDEX billing_adapter_intents_expired ON billing_adapter_intents(expires_at) WHERE state IN ('pending','abandoned');
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM billing_adapter_intents WHERE state IN ('pending','abandoned')) THEN
 RAISE EXCEPTION 'Recover pending billing admissions before reverting';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE billing_adapter_intents;
