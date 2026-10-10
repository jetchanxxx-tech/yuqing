-- A consumed email-change credential is also the durable notification event.
-- Intent is written in the same transaction as identity/version/consumption.
-- Payloads, tokens and supplier errors are never stored in this outbox.
ALTER TABLE verification_tokens
  ADD COLUMN notice_target TEXT NOT NULL DEFAULT '',
  ADD COLUMN notice_state TEXT NOT NULL DEFAULT '' CHECK (notice_state IN ('','pending','processing','failed','accepted')),
  ADD COLUMN notice_attempts INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN notice_next_attempt TIMESTAMPTZ,
  ADD COLUMN notice_receipt JSONB NOT NULL DEFAULT '{}'::jsonb;
CREATE INDEX verification_email_notices_due ON verification_tokens(notice_next_attempt)
  WHERE notice_state IN ('pending','processing','failed');
