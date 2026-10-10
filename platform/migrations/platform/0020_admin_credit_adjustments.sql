-- +goose Up
-- K9 administrator credit adjustments: an optimistic balance version,
-- durable operator intent and replay key. Existing ledgers remain intact.
ALTER TABLE report_credits ADD COLUMN version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE credit_transactions ADD COLUMN reason_detail TEXT NOT NULL DEFAULT '';
ALTER TABLE credit_transactions ADD COLUMN actor_id TEXT;
ALTER TABLE credit_transactions ADD COLUMN idempotency_key TEXT;
ALTER TABLE credit_transactions ADD COLUMN version BIGINT NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX credit_transactions_admin_key
  ON credit_transactions(tenant_id,idempotency_key)
  WHERE idempotency_key IS NOT NULL;

ALTER TABLE credit_transactions ADD COLUMN expected_version BIGINT;
ALTER TABLE report_credits ADD CONSTRAINT report_credit_version_nonnegative CHECK(version>=0);
ALTER TABLE credit_transactions ADD CONSTRAINT admin_adjustment_intent CHECK(reason <> 'admin_adjust' OR (delta<>0 AND length(btrim(reason_detail))>0 AND actor_id IS NOT NULL AND length(actor_id)>0 AND idempotency_key IS NOT NULL AND length(idempotency_key) BETWEEN 1 AND 128 AND expected_version IS NOT NULL AND expected_version>=0 AND version>0));
-- Every balance/plan writer, including the accepted K4 atomic run path,
-- advances the same CAS version. No K4 transaction or lock order is changed.
-- +goose StatementBegin
CREATE FUNCTION report_credit_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN NEW.version=OLD.version+1; RETURN NEW; END $$;
-- +goose StatementEnd
CREATE TRIGGER report_credit_version BEFORE UPDATE ON report_credits FOR EACH ROW EXECUTE FUNCTION report_credit_version();
CREATE TABLE account_notification_attempts(
 id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),actor_id TEXT NOT NULL REFERENCES users(id),
 actor_version BIGINT NOT NULL CHECK(actor_version>=0),purpose TEXT NOT NULL CHECK(purpose IN ('set_password','password_reset')),
 state TEXT NOT NULL CHECK(state IN ('pending','accepted','failed')),error_code TEXT NOT NULL DEFAULT '',created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX account_notification_user ON account_notification_attempts(user_id,created_at DESC);

-- +goose Down
-- Administrator ledger intent and dispatch history are forward-only.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'administrator credit and dispatch migration is forward-only'; END $$;
-- +goose StatementEnd
