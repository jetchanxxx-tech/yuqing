package monitorplan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
)

var ErrNotFound = pkgerrors.ErrNotFound
var ErrConflict = pkgerrors.ErrConflict

type PGStore struct{ pool *pgxpool.Pool }

func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const planColumns = `id, tenant_id, owner_id, name, template_id, template_version, analysis_type,
 inputs_json, config_json, revision, state, created_at, updated_at`

func scanPlan(row pgx.Row) (*Plan, error) {
	var plan Plan
	var inputs, config []byte
	err := row.Scan(&plan.ID, &plan.TenantID, &plan.OwnerID, &plan.Name, &plan.TemplateID, &plan.TemplateVersion,
		&plan.AnalysisType, &inputs, &config, &plan.Revision, &plan.State, &plan.CreatedAt, &plan.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(inputs, &plan.Inputs); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(config, &plan.Config); err != nil {
		return nil, err
	}
	return &plan, nil
}

func (s *PGStore) Create(ctx context.Context, draft Plan) (*Plan, error) {
	if err := validateDraft(draft); err != nil {
		return nil, err
	}
	if draft.ID == "" {
		draft.ID = id.New()
	}
	inputs, err := json.Marshal(draft.Inputs)
	if err != nil {
		return nil, err
	}
	config, err := json.Marshal(draft.Config)
	if err != nil {
		return nil, err
	}
	return scanPlan(s.pool.QueryRow(ctx, `INSERT INTO monitor_plans
 (id, tenant_id, owner_id, name, template_id, template_version, analysis_type, inputs_json, config_json)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING `+planColumns,
		draft.ID, draft.TenantID, draft.OwnerID, draft.Name, draft.TemplateID, draft.TemplateVersion, draft.AnalysisType, inputs, config))
}

func (s *PGStore) Get(ctx context.Context, tenantID, planID string) (*Plan, error) {
	plan, err := scanPlan(s.pool.QueryRow(ctx, `SELECT `+planColumns+` FROM monitor_plans WHERE tenant_id=$1 AND id=$2`, tenantID, planID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return plan, err
}

func (s *PGStore) Update(ctx context.Context, tenantID string, draft Plan) (*Plan, error) {
	if tenantID == "" || tenantID != draft.TenantID {
		return nil, ErrNotFound
	}
	if draft.ID == "" || draft.Revision < 1 {
		return nil, fmt.Errorf("monitorplan: invalid update scope or revision")
	}
	if err := validateDraft(draft); err != nil {
		return nil, err
	}
	inputs, err := json.Marshal(draft.Inputs)
	if err != nil {
		return nil, err
	}
	config, err := json.Marshal(draft.Config)
	if err != nil {
		return nil, err
	}
	updated, err := scanPlan(s.pool.QueryRow(ctx, `UPDATE monitor_plans SET name=$4, analysis_type=$5,
 inputs_json=$6, config_json=$7, revision=revision+1, updated_at=now()
 WHERE tenant_id=$1 AND id=$2 AND revision=$3 AND owner_id=$8 AND template_id=$9 AND template_version=$10 AND state='draft'
 RETURNING `+planColumns, tenantID, draft.ID, draft.Revision, draft.Name, draft.AnalysisType, inputs, config,
		draft.OwnerID, draft.TemplateID, draft.TemplateVersion))
	if !errors.Is(err, pgx.ErrNoRows) {
		return updated, err
	}
	previous, err := s.Get(ctx, tenantID, draft.ID)
	if err != nil {
		return nil, err
	}
	if previous.OwnerID != draft.OwnerID || previous.TemplateID != draft.TemplateID || previous.TemplateVersion != draft.TemplateVersion {
		return nil, ErrNotFound
	}
	return nil, ErrConflict
}

func (s *PGStore) List(ctx context.Context, tenantID string, limit, offset int) ([]Plan, error) {
	if tenantID == "" || limit < 1 || limit > 100 || offset < 0 {
		return nil, fmt.Errorf("monitorplan: invalid list scope or pagination")
	}
	rows, err := s.pool.Query(ctx, `SELECT `+planColumns+` FROM monitor_plans WHERE tenant_id=$1 ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3`, tenantID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	plans := []Plan{}
	for rows.Next() {
		plan, scanErr := scanPlan(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		plans = append(plans, *plan)
	}
	return plans, rows.Err()
}

func (s *PGStore) Delete(ctx context.Context, tenantID, ownerID, planID string, revision int) error {
	if tenantID == "" || ownerID == "" || planID == "" || revision < 1 {
		return ErrNotFound
	}
	result, err := s.pool.Exec(ctx, `DELETE FROM monitor_plans WHERE tenant_id=$1 AND owner_id=$2 AND id=$3 AND revision=$4 AND state='draft'`, tenantID, ownerID, planID, revision)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 1 {
		return nil
	}
	plan, err := s.Get(ctx, tenantID, planID)
	if err != nil {
		return err
	}
	if plan.OwnerID != ownerID {
		return ErrNotFound
	}
	return ErrConflict
}

func validateDraft(draft Plan) error {
	if draft.TenantID == "" || draft.OwnerID == "" || strings.TrimSpace(draft.Name) == "" || len([]rune(draft.Name)) > 200 || draft.State != "" && draft.State != "draft" {
		return fmt.Errorf("monitorplan: invalid draft identity, name or state")
	}
	if _, ok := LookupTemplate(draft.TemplateID, draft.TemplateVersion); !ok {
		return fmt.Errorf("monitorplan: unknown template version")
	}
	if err := ValidateAnalysisType(draft.AnalysisType); err != nil {
		return err
	}
	for key, item := range draft.Config {
		if item.State != "proposed" && item.State != "unavailable" || item.Source == "" {
			return fmt.Errorf("monitorplan: invalid candidate state or source for %q", key)
		}
	}
	return nil
}
