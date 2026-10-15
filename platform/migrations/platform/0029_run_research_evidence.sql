-- +goose Up
ALTER TABLE analysis_runs ADD COLUMN research_intent JSONB NOT NULL DEFAULT '{}';
ALTER TABLE analysis_runs ADD COLUMN research_intent_version TEXT NOT NULL DEFAULT '';
ALTER TABLE analysis_runs ADD COLUMN research_intent_model TEXT NOT NULL DEFAULT '';
ALTER TABLE analysis_runs ADD COLUMN research_intent_created_at TIMESTAMPTZ;
ALTER TABLE analysis_runs ADD COLUMN retrieval_coverage JSONB;
ALTER TABLE analysis_runs ADD COLUMN insight_material_coverage JSONB;
ALTER TABLE analysis_runs ADD COLUMN report_material_coverage JSONB;
ALTER TABLE raw_documents ADD COLUMN observed_spans JSONB NOT NULL DEFAULT '[]';
ALTER TABLE raw_documents ADD COLUMN date_sources JSONB NOT NULL DEFAULT '{}';

ALTER TABLE analysis_runs ADD CONSTRAINT analysis_runs_evidence_identity UNIQUE(id,tenant_id,analysis_id);
CREATE TABLE analysis_retrieval_audits (
 id TEXT PRIMARY KEY,run_id TEXT NOT NULL REFERENCES analysis_runs(id),tenant_id TEXT NOT NULL,analysis_id TEXT NOT NULL,
 coverage JSONB NOT NULL,coverage_hash TEXT NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(run_id,coverage_hash),
 FOREIGN KEY(run_id,tenant_id,analysis_id) REFERENCES analysis_runs(id,tenant_id,analysis_id)
);
CREATE INDEX idx_analysis_retrieval_audits_run ON analysis_retrieval_audits(tenant_id,analysis_id,run_id,created_at);
CREATE TABLE analysis_candidate_materials (
 run_id TEXT NOT NULL REFERENCES analysis_runs(id),tenant_id TEXT NOT NULL,analysis_id TEXT NOT NULL,document_id TEXT NOT NULL,
 content TEXT NOT NULL,content_sha256 TEXT NOT NULL,metadata JSONB NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(run_id,document_id,content_sha256),
 FOREIGN KEY(run_id,tenant_id,analysis_id) REFERENCES analysis_runs(id,tenant_id,analysis_id)
);

-- +goose StatementBegin
CREATE FUNCTION analysis_research_intent_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.research_intent_version<>'' AND
    (NEW.research_intent IS DISTINCT FROM OLD.research_intent OR NEW.research_intent_version IS DISTINCT FROM OLD.research_intent_version OR NEW.research_intent_model IS DISTINCT FROM OLD.research_intent_model OR NEW.research_intent_created_at IS DISTINCT FROM OLD.research_intent_created_at) AND
    NOT EXISTS(SELECT 1 FROM analyses WHERE id=OLD.analysis_id AND tenant_id=OLD.tenant_id AND closure_purged_at IS NOT NULL)
 THEN RAISE EXCEPTION 'run research intent is immutable'; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER analysis_research_intent_immutable BEFORE UPDATE ON analysis_runs FOR EACH ROW EXECUTE FUNCTION analysis_research_intent_immutable();

-- Evidence follows the existing authoritative account retention decision.
-- Financial run identities and ledgers remain intact.
-- +goose StatementBegin
CREATE FUNCTION analysis_research_evidence_purge() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.closure_purged_at IS NOT NULL AND OLD.closure_purged_at IS NULL THEN
  DELETE FROM analysis_candidate_materials WHERE analysis_id=NEW.id AND tenant_id=NEW.tenant_id;
  DELETE FROM analysis_retrieval_audits WHERE analysis_id=NEW.id AND tenant_id=NEW.tenant_id;
  UPDATE analysis_runs SET research_intent='{}',research_intent_version='',research_intent_model='',research_intent_created_at=NULL,retrieval_coverage=NULL,insight_material_coverage=NULL,report_material_coverage=NULL WHERE analysis_id=NEW.id AND tenant_id=NEW.tenant_id;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER analysis_research_evidence_purge AFTER UPDATE OF closure_purged_at ON analyses FOR EACH ROW EXECUTE FUNCTION analysis_research_evidence_purge();

-- +goose Down
DROP TRIGGER analysis_research_evidence_purge ON analyses;
DROP FUNCTION analysis_research_evidence_purge();
DROP TRIGGER analysis_research_intent_immutable ON analysis_runs;
DROP FUNCTION analysis_research_intent_immutable();
DROP TABLE analysis_candidate_materials;
DROP TABLE analysis_retrieval_audits;
ALTER TABLE analysis_runs DROP CONSTRAINT analysis_runs_evidence_identity;
ALTER TABLE raw_documents DROP COLUMN date_sources;
ALTER TABLE raw_documents DROP COLUMN observed_spans;
ALTER TABLE analysis_runs DROP COLUMN report_material_coverage;
ALTER TABLE analysis_runs DROP COLUMN insight_material_coverage;
ALTER TABLE analysis_runs DROP COLUMN retrieval_coverage;
ALTER TABLE analysis_runs DROP COLUMN research_intent_created_at;
ALTER TABLE analysis_runs DROP COLUMN research_intent_model;
ALTER TABLE analysis_runs DROP COLUMN research_intent_version;
ALTER TABLE analysis_runs DROP COLUMN research_intent;
