package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/internal/pkg/queue"
	"github.com/yuqing/platform/internal/platform/billingpolicy"
	"github.com/yuqing/platform/internal/platform/credit"
)

type Run struct {
	ID, TenantID, AnalysisID       string
	RunNo                          int
	Actor                          billingpolicy.Actor
	Decision                       billingpolicy.Decision
	ChargeMode, ConsumeTxID, State string
	CreatedAt                      time.Time
	FinishedAt                     *time.Time
}

func (s *Service) admitPG(ctx context.Context, tenantID string, a *AnalysisResult, analysisID string, actor billingpolicy.Actor) error {
	pg := s.store.(*pgStore)
	credits, ok := s.credits.(*credit.Service)
	if !ok || !credits.UsesPool(pg.pool) {
		return fmt.Errorf("analysis: credits must use analysis pool")
	}
	q, ok := s.queue.(*queue.PGQueue)
	if !ok || !q.UsesPool(pg.pool) {
		return fmt.Errorf("analysis: queue must use analysis pool")
	}
	tx, err := pg.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = billingpolicy.CheckActorTx(ctx, tx, tenantID, actor); err != nil {
		return err
	}
	rerun := a == nil
	runNo := 1
	if rerun {
		a, err = scanAnalysis(tx.QueryRow(ctx, `SELECT `+analysisColumns+` FROM analyses WHERE id=$1 AND tenant_id=$2 FOR UPDATE`, analysisID, tenantID))
		if err != nil {
			return notFoundOrInternal(err, "analysis not found")
		}
		if !IsTerminal(string(a.State)) {
			return pkgerrors.ErrConflict
		}
		if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(run_no),0)+1 FROM analysis_runs WHERE analysis_id=$1 AND tenant_id=$2`, a.ID, tenantID).Scan(&runNo); err != nil {
			return err
		}
	}
	// Locks are tenant → actor → analysis → credits; the policy snapshot is read
	// after locking the real paid plan row so purchase cannot interleave its update.
	if _, err = tx.Exec(ctx, `INSERT INTO report_credits(tenant_id,balance,plan_code) VALUES($1,0,'free') ON CONFLICT DO NOTHING`, tenantID); err != nil {
		return err
	}
	var plan string
	if err = tx.QueryRow(ctx, `SELECT plan_code FROM report_credits WHERE tenant_id=$1 FOR UPDATE`, tenantID).Scan(&plan); err != nil {
		return err
	}
	decision, err := billingpolicy.NewService(pg.pool).ResolveTx(ctx, tx, tenantID, actor)
	if err != nil {
		return err
	}
	runID := id.New()
	chargeMode := "normal"
	if decision.Exempt {
		chargeMode = "exempt"
	}
	_, err = tx.Exec(ctx, `INSERT INTO analysis_runs(id,tenant_id,analysis_id,run_no,actor_user_id,actor_api_key_id,plan_code,catalog_revision,charge_mode,exempt_policy_key,exempt_policy_version,state) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8,$9,NULLIF($10,''),NULLIF($11,0),'queued')`, runID, tenantID, a.ID, runNo, actor.UserID, actor.APIKeyID, decision.PlanCode, decision.CatalogRevision, chargeMode, decision.PolicyKey, decision.PolicyVersion)
	if err != nil {
		return err
	}
	if !decision.Exempt {
		consumed, err := credits.TryConsumeTx(ctx, tx, credit.Reservation{TenantID: tenantID, AnalysisID: a.ID, RunID: runID, ActorUserID: actor.UserID, ActorAPIKeyID: actor.APIKeyID, PlanCode: decision.PlanCode})
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE analysis_runs SET consume_tx_id=$2 WHERE id=$1`, runID, consumed.ID); err != nil {
			return err
		}
	}
	if rerun {
		if _, err = tx.Exec(ctx, deleteDocumentsSQL, tenantID, a.ID); err != nil {
			return err
		}
		resetForRerun(a)
	}
	a.CurrentRunID = runID
	statement := insertAnalysisSQL
	if rerun {
		statement = updateAnalysisSQL
	}
	if _, err = tx.Exec(ctx, statement, analysisArgs(tenantID, a)...); err != nil {
		return pgInternal(err)
	}
	payload, err := json.Marshal(TaskMessage{TenantID: tenantID, AnalysisID: a.ID, RunID: runID})
	if err != nil {
		return err
	}
	if err = q.PublishTx(ctx, tx, topicAnalysisTasks, payload); err != nil {
		return fmt.Errorf("analysis: enqueue: %w", err)
	}
	return tx.Commit(ctx)
}
func resetForRerun(a *AnalysisResult) {
	a.State = StateQueued
	a.Progress = 0
	a.ErrorCode = ""
	a.StartedAt = time.Time{}
	a.FinishedAt = time.Time{}
	a.DocCount = 0
	a.Summary = ""
	a.Warning = ""
	a.Sentiments = nil
	a.Topics = nil
	a.Dimensions = nil
	a.ReportID = ""
	a.ReportContent = ""
	a.RetrievalCoverage = nil
}

// syncRunTx commits terminal accounting and state in the same transaction.
func syncRunTx(ctx context.Context, tx pgx.Tx, tenantID string, a *AnalysisResult, previous State) error {
	if a.CurrentRunID == "" {
		return nil
	} // Explicit pre-cutover fixture/history only.
	if !IsTerminal(string(previous)) && (a.State == StateFailed || a.State == StateCanceled) {
		if _, err := credit.RefundRunTx(ctx, tx, tenantID, a.CurrentRunID); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `UPDATE analysis_runs SET state=$2,finished_at=$3 WHERE id=$1 AND tenant_id=$4 AND analysis_id=$5`, a.CurrentRunID, string(a.State), nullableTime(a.FinishedAt), tenantID, a.ID)
	return err
}

// ResolveTask rejects stale deliveries. Missing run IDs are accepted only for
// explicitly migrated legacy runs; they cannot attach to a new paid/free run.
func (s *Service) ResolveTask(ctx context.Context, msg TaskMessage) (TaskMessage, bool, error) {
	a, err := s.Get(ctx, msg.TenantID, msg.AnalysisID)
	if err != nil {
		return msg, false, err
	}
	if _, pg := s.store.(*pgStore); pg && msg.RunID == "" {
		if a.CurrentRunID == "" {
			return msg, false, nil
		}
		var mode string
		if err := s.store.(*pgStore).pool.QueryRow(ctx, `SELECT charge_mode FROM analysis_runs WHERE id=$1 AND tenant_id=$2 AND analysis_id=$3`, a.CurrentRunID, msg.TenantID, msg.AnalysisID).Scan(&mode); err != nil {
			return msg, false, err
		}
		if mode != "legacy_unbilled" {
			return msg, false, nil
		}
		msg.RunID = a.CurrentRunID
	}
	if msg.RunID != "" && msg.RunID != a.CurrentRunID {
		return msg, false, nil
	}
	return msg, true, nil
}
func (s *Service) TransitionRun(ctx context.Context, tenantID, analysisID, runID, to string) error {
	if runID == "" {
		return pkgerrors.ErrConflict
	}
	return s.Transition(billingpolicy.WithRun(ctx, runID), tenantID, analysisID, to)
}
func (s *Service) markFailedRun(ctx context.Context, tenantID, analysisID, runID, code string) error {
	if runID == "" {
		return pkgerrors.ErrConflict
	}
	return s.markFailed(billingpolicy.WithRun(ctx, runID), tenantID, analysisID, code)
}
