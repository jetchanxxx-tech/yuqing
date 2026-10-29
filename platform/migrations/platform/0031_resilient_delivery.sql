-- +goose Up
ALTER TABLE analyses ADD COLUMN seed_urls JSONB NOT NULL DEFAULT '[]';
ALTER TABLE analyses ADD COLUMN delivery_policy TEXT NOT NULL DEFAULT '' CHECK(delivery_policy IN ('','best_available','full_only'));
ALTER TABLE analyses ADD COLUMN delivery_level TEXT NOT NULL DEFAULT '' CHECK(delivery_level IN ('','full','limited','brief','empty'));
ALTER TABLE analyses ADD COLUMN delivery_reasons JSONB NOT NULL DEFAULT '[]';
ALTER TABLE analyses ADD COLUMN resume_from_run_id TEXT NOT NULL DEFAULT '';
ALTER TABLE analyses ADD COLUMN model_fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE analysis_runs ADD COLUMN delivery_policy TEXT NOT NULL DEFAULT '' CHECK(delivery_policy IN ('','best_available','full_only'));
ALTER TABLE analysis_runs ADD COLUMN delivery_level TEXT NOT NULL DEFAULT '' CHECK(delivery_level IN ('','full','limited','brief','empty'));
ALTER TABLE analysis_runs ADD COLUMN delivery_reasons JSONB NOT NULL DEFAULT '[]';
ALTER TABLE analysis_runs ADD COLUMN resume_from_run_id TEXT NOT NULL DEFAULT '';
ALTER TABLE analysis_runs ADD COLUMN model_fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE analysis_runs ADD COLUMN inputs_snapshot JSONB NOT NULL DEFAULT '{}';
ALTER TABLE analysis_runs ADD COLUMN materials_snapshot JSONB NOT NULL DEFAULT '[]';
ALTER TABLE analysis_runs ADD COLUMN insight_snapshot JSONB NOT NULL DEFAULT '{}';
ALTER TABLE analysis_runs ADD COLUMN report_content_snapshot TEXT NOT NULL DEFAULT '';
ALTER TABLE reports ADD COLUMN run_id TEXT REFERENCES analysis_runs(id);
ALTER TABLE reports ADD COLUMN delivery_level TEXT NOT NULL DEFAULT '';
ALTER TABLE reports ADD COLUMN delivery_reasons JSONB NOT NULL DEFAULT '[]';
ALTER TABLE reports ADD COLUMN resume_from_run_id TEXT NOT NULL DEFAULT '';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION billing_ledger_integrity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r analysis_runs;
BEGIN
 IF NEW.run_id IS NULL THEN RETURN NULL; END IF;
 SELECT * INTO r FROM analysis_runs WHERE id=NEW.run_id;
 IF NOT FOUND OR r.charge_mode<>'normal' OR NEW.tenant_id IS DISTINCT FROM r.tenant_id OR NEW.analysis_id IS DISTINCT FROM r.analysis_id
  OR NEW.actor_user_id IS DISTINCT FROM r.actor_user_id OR NEW.actor_api_key_id IS DISTINCT FROM r.actor_api_key_id OR NEW.plan_code_snapshot IS DISTINCT FROM r.plan_code
 THEN RAISE EXCEPTION 'ledger run snapshot mismatch'; END IF;
 IF NEW.reason='consume' THEN
  IF NEW.id IS DISTINCT FROM r.consume_tx_id OR NEW.delta<>-1 THEN RAISE EXCEPTION 'consume reverse pairing mismatch'; END IF;
 ELSIF NEW.reason='refund' THEN
  IF NEW.consume_tx_id IS DISTINCT FROM r.consume_tx_id OR NEW.delta<>1 OR NOT
   (r.state IN ('failed','canceled') OR (r.state='completed' AND r.delivery_level IN ('limited','brief','empty')))
  THEN RAISE EXCEPTION 'refund must settle this failed, canceled, or explicitly non-full run'; END IF;
 ELSE RAISE EXCEPTION 'unsupported run-linked ledger reason'; END IF;
 RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION preserve_run_delivery_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.delivery_level<>'' AND OLD.state IN ('completed','failed','canceled') AND
 (NEW.delivery_level,NEW.delivery_reasons,NEW.report_content_snapshot,NEW.insight_snapshot,NEW.materials_snapshot,NEW.inputs_snapshot,NEW.model_fingerprint,NEW.resume_from_run_id,NEW.delivery_policy)
 IS DISTINCT FROM
 (OLD.delivery_level,OLD.delivery_reasons,OLD.report_content_snapshot,OLD.insight_snapshot,OLD.materials_snapshot,OLD.inputs_snapshot,OLD.model_fingerprint,OLD.resume_from_run_id,OLD.delivery_policy)
 AND NOT EXISTS(SELECT 1 FROM analyses WHERE id=OLD.analysis_id AND tenant_id=OLD.tenant_id AND closure_purged_at IS NOT NULL)
 THEN RAISE EXCEPTION 'run delivery snapshot is immutable'; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER preserve_run_delivery_snapshot BEFORE UPDATE ON analysis_runs FOR EACH ROW EXECUTE FUNCTION preserve_run_delivery_snapshot();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION capture_report_content_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r analysis_runs; a analyses;
BEGIN
 IF NEW.run_id IS NOT NULL THEN
  SELECT * INTO r FROM analysis_runs WHERE id=NEW.run_id AND tenant_id=NEW.tenant_id AND analysis_id=NEW.analysis_id FOR SHARE;
  IF NOT FOUND OR r.state NOT IN ('completed','failed') THEN RAISE EXCEPTION 'report requires terminal run snapshot'; END IF;
  NEW.content_html=r.report_content_snapshot;
  NEW.delivery_level=r.delivery_level; NEW.delivery_reasons=r.delivery_reasons; NEW.resume_from_run_id=r.resume_from_run_id;
  -- Strict historical runs predate the immutable per-run snapshot.
  IF NEW.content_html='' AND r.delivery_level='' THEN
   SELECT * INTO a FROM analyses WHERE id=NEW.analysis_id AND tenant_id=NEW.tenant_id AND current_run_id=r.id AND state='completed' FOR SHARE;
   NEW.content_html=COALESCE(a.report_content,'');
  END IF;
 ELSE
  SELECT * INTO a FROM analyses WHERE id=NEW.analysis_id AND tenant_id=NEW.tenant_id AND state='completed' FOR SHARE;
  NEW.content_html=COALESCE(a.report_content,''); NEW.delivery_level=COALESCE(a.delivery_level,'');
  NEW.delivery_reasons=COALESCE(a.delivery_reasons,'[]'); NEW.resume_from_run_id=COALESCE(a.resume_from_run_id,'');
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_report_content_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.content_html,NEW.run_id,NEW.delivery_level,NEW.delivery_reasons,NEW.resume_from_run_id)
 IS DISTINCT FROM (OLD.content_html,OLD.run_id,OLD.delivery_level,OLD.delivery_reasons,OLD.resume_from_run_id)
 THEN RAISE EXCEPTION 'published report snapshot is immutable'; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER report_content_snapshot_immutable ON reports;
CREATE TRIGGER report_content_snapshot_immutable BEFORE UPDATE ON reports FOR EACH ROW EXECUTE FUNCTION preserve_report_content_snapshot();

CREATE TABLE tool_call_authorizations (
 call_id TEXT PRIMARY KEY,run_id TEXT NOT NULL REFERENCES analysis_runs(id),provider TEXT NOT NULL CHECK(provider='deepseek'),
 operation TEXT NOT NULL CHECK(operation IN ('search','fetch')),attempt INTEGER NOT NULL CHECK(attempt>0),request_hash TEXT NOT NULL,
 authorization_hash TEXT NOT NULL,permit_hash TEXT NOT NULL,price_version TEXT NOT NULL DEFAULT '',
 authorized_at TIMESTAMPTZ NOT NULL DEFAULT now(),expires_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE tool_usage_events (
 event_id TEXT PRIMARY KEY,call_id TEXT UNIQUE NOT NULL REFERENCES tool_call_authorizations(call_id),run_id TEXT NOT NULL REFERENCES analysis_runs(id),
 tenant_id TEXT NOT NULL,analysis_id TEXT NOT NULL,actor_user_id TEXT NOT NULL,provider TEXT NOT NULL CHECK(provider='deepseek'),
 operation TEXT NOT NULL CHECK(operation IN ('search','fetch')),event_version INTEGER NOT NULL CHECK(event_version=1),attempt INTEGER NOT NULL CHECK(attempt>0),
 outcome TEXT NOT NULL,http_status INTEGER NOT NULL CHECK(http_status BETWEEN 0 AND 599),result_count INTEGER NOT NULL CHECK(result_count>=0),
 usage_status TEXT NOT NULL CHECK(usage_status IN ('reported','unknown')),cost_micro_cny BIGINT CHECK(cost_micro_cny>=0),
 cost_status TEXT NOT NULL CHECK(cost_status IN ('known','pending')),provider_price_version TEXT NOT NULL DEFAULT '',provider_request_id TEXT NOT NULL DEFAULT '',
 reconciliation_status TEXT NOT NULL DEFAULT 'pending' CHECK(reconciliation_status IN ('pending','reconciled')),
 event_hash TEXT NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(run_id,tenant_id,analysis_id) REFERENCES analysis_runs(id,tenant_id,analysis_id),
 CHECK((cost_status='pending' AND cost_micro_cny IS NULL) OR (cost_status='known' AND cost_micro_cny IS NOT NULL))
);
CREATE INDEX tool_usage_tenant_created ON tool_usage_events(tenant_id,created_at);

-- +goose StatementBegin
CREATE FUNCTION immutable_tool_billing_fact() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'tool billing facts are immutable'; END;
$$;
-- +goose StatementEnd
CREATE TRIGGER immutable_tool_usage BEFORE UPDATE OR DELETE ON tool_usage_events FOR EACH ROW EXECUTE FUNCTION immutable_tool_billing_fact();
CREATE TRIGGER immutable_tool_authorization BEFORE UPDATE OR DELETE ON tool_call_authorizations FOR EACH ROW EXECUTE FUNCTION immutable_tool_billing_fact();

-- Respect the existing retention boundary for duplicated material snapshots.
-- +goose StatementBegin
CREATE FUNCTION purge_run_delivery_materials() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.closure_purged_at IS NOT NULL AND OLD.closure_purged_at IS NULL THEN
  UPDATE analyses SET seed_urls='[]',delivery_reasons='[]' WHERE id=NEW.id AND tenant_id=NEW.tenant_id;
  UPDATE analysis_runs SET inputs_snapshot='{}',materials_snapshot='[]',insight_snapshot='{}',report_content_snapshot='',delivery_reasons='[]' WHERE analysis_id=NEW.id AND tenant_id=NEW.tenant_id;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER purge_run_delivery_materials AFTER UPDATE OF closure_purged_at ON analyses FOR EACH ROW EXECUTE FUNCTION purge_run_delivery_materials();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'resilient delivery and billing snapshots require a reviewed forward migration'; END $$;
-- +goose StatementEnd
