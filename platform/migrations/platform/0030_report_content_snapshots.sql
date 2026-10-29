-- +goose Up
ALTER TABLE reports ADD COLUMN content_html TEXT NOT NULL DEFAULT '';

-- Only the latest completed report created after the current analysis finished
-- can inherit its content. Older/undated versions remain explicitly unavailable.
WITH latest AS (
 SELECT DISTINCT ON (tenant_id,analysis_id) id
 FROM reports WHERE format='html' AND status='completed'
 ORDER BY tenant_id,analysis_id,report_version DESC,created_at DESC,id DESC
)
UPDATE reports r SET content_html=a.report_content
FROM analyses a,latest l
WHERE r.id=l.id AND r.analysis_id=a.id AND r.tenant_id=a.tenant_id
 AND a.state='completed' AND a.finished_at IS NOT NULL
 AND r.created_at>=a.finished_at AND a.report_content<>'';

-- +goose StatementBegin
CREATE FUNCTION capture_report_content_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE snapshot TEXT;
BEGIN
 SELECT report_content INTO snapshot FROM analyses
 WHERE id=NEW.analysis_id AND tenant_id=NEW.tenant_id AND state='completed'
 FOR SHARE;
 NEW.content_html=COALESCE(snapshot,'');
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER report_content_snapshot_insert BEFORE INSERT ON reports
FOR EACH ROW EXECUTE FUNCTION capture_report_content_snapshot();

-- +goose StatementBegin
CREATE FUNCTION preserve_report_content_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.content_html IS DISTINCT FROM OLD.content_html THEN
  RAISE EXCEPTION 'published report content is immutable';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER report_content_snapshot_immutable BEFORE UPDATE OF content_html ON reports
FOR EACH ROW EXECUTE FUNCTION preserve_report_content_snapshot();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'report snapshots must be retained; use a reviewed forward migration'; END $$;
-- +goose StatementEnd
