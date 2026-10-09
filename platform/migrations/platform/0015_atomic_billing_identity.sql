-- +goose Up
-- K4 development migration: freeze the complete schema before shared installation.
CREATE TABLE billing_exempt_principals (
 policy_key TEXT PRIMARY KEY CHECK(policy_key='fixed_admin_v1'),
 user_id TEXT UNIQUE NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 policy_version BIGINT NOT NULL DEFAULT 1 CHECK(policy_version=1),
 bound_at TIMESTAMPTZ NOT NULL DEFAULT now(), bound_by TEXT NOT NULL
);
-- +goose StatementBegin
CREATE FUNCTION billing_binding_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'billing binding is immutable'; END;
$$;
-- +goose StatementEnd
CREATE TRIGGER billing_binding_immutable BEFORE UPDATE OR DELETE ON billing_exempt_principals FOR EACH ROW EXECUTE FUNCTION billing_binding_immutable();
ALTER TABLE api_keys ADD COLUMN creator_user_id TEXT REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_api_keys_creator ON api_keys(creator_user_id);
-- Historical NULL is deliberately not inferred from tenant membership.
-- +goose StatementBegin
CREATE FUNCTION api_key_creator_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.creator_user_id IS DISTINCT FROM OLD.creator_user_id THEN RAISE EXCEPTION 'api key creator is immutable'; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER api_key_creator_immutable BEFORE UPDATE ON api_keys FOR EACH ROW EXECUTE FUNCTION api_key_creator_immutable();

CREATE TABLE analysis_runs (
 id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL,
 analysis_id TEXT NOT NULL REFERENCES analyses(id) DEFERRABLE INITIALLY DEFERRED,
 run_no INTEGER NOT NULL CHECK(run_no>0),
 actor_user_id TEXT REFERENCES users(id) ON DELETE RESTRICT,
 actor_api_key_id TEXT REFERENCES api_keys(id) ON DELETE RESTRICT,
 plan_code TEXT NOT NULL, catalog_revision TEXT NOT NULL,
 charge_mode TEXT NOT NULL CHECK(charge_mode IN ('normal','exempt','legacy_unbilled')),
 exempt_policy_key TEXT REFERENCES billing_exempt_principals(policy_key), exempt_policy_version BIGINT,
 consume_tx_id TEXT REFERENCES credit_transactions(id) DEFERRABLE INITIALLY DEFERRED,
 state TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), finished_at TIMESTAMPTZ,
 UNIQUE(tenant_id,analysis_id,run_no),
 CHECK(charge_mode='legacy_unbilled' OR actor_user_id IS NOT NULL),
 CHECK((charge_mode='exempt' AND exempt_policy_key='fixed_admin_v1' AND exempt_policy_version=1 AND consume_tx_id IS NULL)
    OR (charge_mode IN ('normal','legacy_unbilled') AND exempt_policy_key IS NULL AND exempt_policy_version IS NULL)),
 CHECK(charge_mode<>'legacy_unbilled' OR consume_tx_id IS NULL)
);
ALTER TABLE analyses ADD COLUMN current_run_id TEXT REFERENCES analysis_runs(id) DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX idx_analyses_current_run ON analyses(current_run_id);
ALTER TABLE credit_transactions ADD COLUMN run_id TEXT REFERENCES analysis_runs(id) DEFERRABLE INITIALLY DEFERRED,
 ADD COLUMN actor_user_id TEXT REFERENCES users(id), ADD COLUMN actor_api_key_id TEXT REFERENCES api_keys(id), ADD COLUMN plan_code_snapshot TEXT;
CREATE UNIQUE INDEX idx_credit_run_consume ON credit_transactions(run_id) WHERE reason='consume' AND run_id IS NOT NULL;
-- Old ledger facts do not establish an unambiguous current consumption. Retain
-- them unchanged for reconciliation; never refund them by a guessed run link.
INSERT INTO analysis_runs(id,tenant_id,analysis_id,run_no,actor_user_id,plan_code,catalog_revision,charge_mode,state,created_at,finished_at)
 SELECT 'legacy:'||a.id,a.tenant_id,a.id,1,a.created_by,COALESCE(c.plan_code,'unknown'),'legacy','legacy_unbilled',a.state,a.created_at,a.finished_at
 FROM analyses a LEFT JOIN report_credits c ON c.tenant_id=a.tenant_id;
UPDATE analyses SET current_run_id='legacy:'||id;
INSERT INTO audit_logs(action,resource,details_json)
 SELECT 'billing.legacy.reconciliation',id,jsonb_build_object('analysis_id',id,'actor_known',created_by IS NOT NULL,'charge_evidence','unlinked') FROM analyses;
-- Validate final transaction state, including the circular run/consume links.
-- +goose StatementBegin
CREATE FUNCTION billing_run_integrity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r analysis_runs; a analyses; c credit_transactions; target_analysis TEXT;
BEGIN
 IF TG_TABLE_NAME='analyses' THEN target_analysis:=NEW.id; ELSE target_analysis:=NEW.analysis_id; END IF;
 FOR r IN SELECT * FROM analysis_runs WHERE analysis_id=target_analysis LOOP
  SELECT * INTO a FROM analyses WHERE id=r.analysis_id;
  IF a.tenant_id IS DISTINCT FROM r.tenant_id THEN RAISE EXCEPTION 'billing run tenant mismatch'; END IF;
  IF r.charge_mode='normal' THEN
   SELECT * INTO c FROM credit_transactions WHERE id=r.consume_tx_id;
   IF NOT FOUND OR c.reason<>'consume' OR c.delta<>-1 OR c.run_id IS DISTINCT FROM r.id OR c.tenant_id IS DISTINCT FROM r.tenant_id OR c.analysis_id IS DISTINCT FROM r.analysis_id OR c.actor_user_id IS DISTINCT FROM r.actor_user_id THEN
    RAISE EXCEPTION 'normal run requires its exact consume';
   END IF;
  ELSIF r.consume_tx_id IS NOT NULL THEN RAISE EXCEPTION 'uncharged run cannot consume';
  END IF;
  IF r.charge_mode='exempt' AND NOT EXISTS(SELECT 1 FROM billing_exempt_principals p WHERE p.user_id=r.actor_user_id AND p.policy_key=r.exempt_policy_key AND p.policy_version=r.exempt_policy_version) THEN RAISE EXCEPTION 'exempt run policy mismatch'; END IF;
 END LOOP;
 IF TG_TABLE_NAME='analyses' THEN
 IF NEW.current_run_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM analysis_runs WHERE id=NEW.current_run_id AND analysis_id=NEW.id AND tenant_id=NEW.tenant_id) THEN RAISE EXCEPTION 'current run mismatch'; END IF;
 END IF;
 RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER billing_run_integrity AFTER INSERT OR UPDATE ON analysis_runs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION billing_run_integrity();
CREATE CONSTRAINT TRIGGER billing_analysis_integrity AFTER INSERT OR UPDATE ON analyses DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION billing_run_integrity();
-- +goose StatementBegin
CREATE FUNCTION billing_run_identity_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.id,NEW.tenant_id,NEW.analysis_id,NEW.run_no,NEW.actor_user_id,NEW.actor_api_key_id,NEW.plan_code,NEW.catalog_revision,NEW.charge_mode,NEW.exempt_policy_key,NEW.exempt_policy_version)
 IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.analysis_id,OLD.run_no,OLD.actor_user_id,OLD.actor_api_key_id,OLD.plan_code,OLD.catalog_revision,OLD.charge_mode,OLD.exempt_policy_key,OLD.exempt_policy_version)
 OR (OLD.consume_tx_id IS NOT NULL AND NEW.consume_tx_id IS DISTINCT FROM OLD.consume_tx_id) THEN RAISE EXCEPTION 'run identity and billing snapshot are immutable'; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER billing_run_identity_immutable BEFORE UPDATE ON analysis_runs FOR EACH ROW EXECUTE FUNCTION billing_run_identity_immutable();
CREATE TABLE llm_call_authorizations (
 call_id TEXT PRIMARY KEY, run_id TEXT NOT NULL REFERENCES analysis_runs(id), engine TEXT NOT NULL, phase TEXT NOT NULL,
 attempt INTEGER NOT NULL CHECK(attempt>0), requested_model TEXT NOT NULL,
 provider_price_version TEXT, price_snapshot JSONB NOT NULL DEFAULT '{}',
 authorized_at TIMESTAMPTZ NOT NULL DEFAULT now(), expires_at TIMESTAMPTZ NOT NULL,
 permit_hash TEXT NOT NULL, request_hash TEXT NOT NULL
);
ALTER TABLE usage_events ADD COLUMN event_id TEXT, ADD COLUMN call_id TEXT REFERENCES llm_call_authorizations(call_id),
 ADD COLUMN run_id TEXT REFERENCES analysis_runs(id), ADD COLUMN event_version INTEGER, ADD COLUMN attempt INTEGER,
 ADD COLUMN billing_exempt BOOLEAN NOT NULL DEFAULT false, ADD COLUMN quota_tokens BIGINT CHECK(quota_tokens>=0),
 ADD COLUMN usage_status TEXT CHECK(usage_status IN ('reported','unknown')), ADD COLUMN cost_status TEXT CHECK(cost_status IN ('known','pending')),
 ADD COLUMN provider_price_version TEXT, ADD COLUMN provider_request_id TEXT, ADD COLUMN event_hash TEXT, ADD COLUMN outcome TEXT;
ALTER TABLE usage_events ALTER COLUMN cost_micro_cny DROP NOT NULL, ALTER COLUMN cost_micro_cny DROP DEFAULT;
-- Historical values remain facts with unknown verification status; token totals
-- retain the previous accounting convention until individual reconciliation.
UPDATE usage_events SET quota_tokens=GREATEST(0,COALESCE(prompt_tokens,0)+COALESCE(completion_tokens,0)+COALESCE(cache_tokens,0)),usage_status='unknown',cost_status='pending';
CREATE UNIQUE INDEX idx_usage_event_id ON usage_events(event_id) WHERE event_id IS NOT NULL;
CREATE UNIQUE INDEX idx_usage_call_id ON usage_events(call_id) WHERE call_id IS NOT NULL;

-- +goose Down
-- Financial history is irreversible; use a reviewed forward migration.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION '0015 contains financial history; rollback requires a reviewed forward migration'; END $$;
-- +goose StatementEnd
