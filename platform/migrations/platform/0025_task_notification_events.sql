-- +goose Up
-- An account-wide notification generation cancels unsent old work atomically
-- at closure request without materializing an unbounded event history.
ALTER TABLE users ADD COLUMN notification_epoch BIGINT NOT NULL DEFAULT 0 CHECK(notification_epoch>=0);
CREATE TABLE account_notification_events (
 id TEXT PRIMARY KEY,
 user_id TEXT NOT NULL REFERENCES users(id),
 tenant_id TEXT NOT NULL REFERENCES tenants(id),
 analysis_id TEXT NOT NULL REFERENCES analyses(id),
 run_id TEXT NOT NULL REFERENCES analysis_runs(id),
 category TEXT NOT NULL CHECK(category='task_completed'),
 issued_epoch BIGINT NOT NULL CHECK(issued_epoch>=0),
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','processing','failed','accepted','cancelled')),
 attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts>=0),
 next_attempt_at TIMESTAMPTZ DEFAULT now(),
 receipt JSONB NOT NULL DEFAULT '{}'::jsonb,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(run_id,category)
);
CREATE INDEX account_notification_events_due ON account_notification_events(next_attempt_at,id)
 WHERE state IN ('pending','failed','processing');
CREATE INDEX account_notification_events_owner ON account_notification_events(user_id,state);
-- +goose StatementBegin
CREATE FUNCTION enqueue_task_notification() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.state='completed' AND OLD.state IS DISTINCT FROM 'completed' AND NEW.current_run_id IS NOT NULL THEN
  INSERT INTO account_notification_events(id,user_id,tenant_id,analysis_id,run_id,category,issued_epoch)
  SELECT 'task:'||r.id,r.actor_user_id,r.tenant_id,r.analysis_id,r.id,'task_completed',u.notification_epoch
  FROM analysis_runs r JOIN users u ON u.id=r.actor_user_id
  WHERE r.id=NEW.current_run_id AND r.analysis_id=NEW.id AND r.tenant_id=NEW.tenant_id AND u.status='active'
  ON CONFLICT(run_id,category) DO NOTHING;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER enqueue_task_notification AFTER UPDATE OF state ON analyses
 FOR EACH ROW EXECUTE FUNCTION enqueue_task_notification();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'notification intent and receipt migration is forward-only'; END $$;
-- +goose StatementEnd
