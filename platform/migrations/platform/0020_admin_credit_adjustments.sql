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

-- +goose Down
DROP INDEX IF EXISTS credit_transactions_admin_key;
ALTER TABLE credit_transactions DROP COLUMN IF EXISTS version;
ALTER TABLE credit_transactions DROP COLUMN IF EXISTS idempotency_key;
ALTER TABLE credit_transactions DROP COLUMN IF EXISTS actor_id;
ALTER TABLE credit_transactions DROP COLUMN IF EXISTS reason_detail;
ALTER TABLE report_credits DROP COLUMN IF EXISTS version;
