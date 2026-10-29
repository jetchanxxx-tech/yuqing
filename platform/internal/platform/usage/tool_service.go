package usage

import (
	"context"
	"crypto/hmac"
	"encoding/hex"
	"errors"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yuqing/platform/internal/pkg/db"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/llm"
	"github.com/yuqing/platform/internal/platform/billing"
	"github.com/yuqing/platform/internal/platform/billingpolicy"
)

type ToolAuthorizationRequest struct {
	CallID      string `json:"call_id"`
	RunID       string `json:"run_id"`
	Provider    string `json:"provider"`
	Operation   string `json:"operation"`
	Attempt     int    `json:"attempt"`
	RequestHash string `json:"request_hash"`
}
type ToolAuthorization struct {
	CallID    string    `json:"call_id"`
	Permit    string    `json:"permit"`
	ExpiresAt time.Time `json:"expires_at"`
	Duplicate bool      `json:"duplicate"`
}
type ToolUsageEvent struct {
	EventID           string `json:"event_id"`
	CallID            string `json:"call_id"`
	EventVersion      int    `json:"event_version"`
	Attempt           int    `json:"attempt"`
	Provider          string `json:"provider"`
	Operation         string `json:"operation"`
	Outcome           string `json:"outcome"`
	HTTPStatus        int    `json:"http_status"`
	ResultCount       int    `json:"result_count"`
	UsageStatus       string `json:"usage_status"`
	ProviderRequestID string `json:"provider_request_id"`
}

func validTool(provider, operation string) bool {
	return provider == "deepseek" && (operation == "search" || operation == "fetch")
}

func (s *CallService) AuthorizeTool(ctx context.Context, req ToolAuthorizationRequest) (ToolAuthorization, error) {
	result := ToolAuthorization{CallID: req.CallID}
	rawHash, hashErr := hex.DecodeString(req.RequestHash)
	if !validIdentity(req.CallID) || !validIdentity(req.RunID) || !validTool(req.Provider, req.Operation) || req.Attempt < 1 || req.Attempt > 100 || hashErr != nil || len(rawHash) != 32 {
		return result, pkgerrors.ErrBadRequest
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(context.Background())
	authorizationHash := hash(req)
	result.Permit = s.permit("tool:"+req.CallID, authorizationHash)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,741917))`, req.CallID); err != nil {
		return result, err
	}
	var previous string
	err = tx.QueryRow(ctx, `SELECT authorization_hash,expires_at FROM tool_call_authorizations WHERE call_id=$1`, req.CallID).Scan(&previous, &result.ExpiresAt)
	duplicate := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	if duplicate && previous != authorizationHash {
		return result, pkgerrors.ErrConflict
	}
	if duplicate && !result.ExpiresAt.After(time.Now()) {
		return result, pkgerrors.ErrForbidden
	}
	var tenantID, analysisID, actorID, keyID, mode, runState string
	err = tx.QueryRow(ctx, `SELECT tenant_id,analysis_id,COALESCE(actor_user_id,''),COALESCE(actor_api_key_id,''),charge_mode,state FROM analysis_runs WHERE id=$1`, req.RunID).Scan(&tenantID, &analysisID, &actorID, &keyID, &mode, &runState)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, pkgerrors.ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if err = billingpolicy.CheckActorTx(ctx, tx, tenantID, billingpolicy.Actor{UserID: actorID, APIKeyID: keyID, Permission: "analyses:create"}); err != nil {
		return result, err
	}
	var current, state string
	if err = tx.QueryRow(ctx, `SELECT COALESCE(current_run_id,''),state FROM analyses WHERE id=$1 AND tenant_id=$2 FOR SHARE`, analysisID, tenantID).Scan(&current, &state); err != nil {
		return result, err
	}
	if current != req.RunID || state == "completed" || state == "failed" || state == "canceled" || state != runState {
		return result, pkgerrors.ErrConflict
	}
	budget, err := billing.NewEntitlementService(s.pool).EffectiveBudgetTx(ctx, tx, tenantID)
	if err != nil {
		return result, err
	}
	if mode != "exempt" && budget.Mode == llm.BudgetHardCap {
		var spent int64
		if err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(quota_tokens),0) FROM usage_events WHERE tenant_id=$1 AND ($2::timestamptz IS NULL OR created_at >= $2) AND ($3::timestamptz IS NULL OR created_at < $3)`, tenantID, budget.PeriodStart, budget.PeriodEnd).Scan(&spent); err != nil {
			return result, err
		}
		if spent >= budget.TokenQuota {
			return result, pkgerrors.ErrTokenQuotaExceeded
		}
	}
	if duplicate {
		result.Duplicate = true
		return result, tx.Commit(ctx)
	}
	priceVersion := os.Getenv("YUQING_TOOL_PRICE_VERSION")
	if os.Getenv("YUQING_TOOL_PRICE_CURRENCY") != "CNY" {
		priceVersion = ""
	}
	result.ExpiresAt = time.Now().UTC().Add(2 * time.Minute)
	_, err = tx.Exec(ctx, `INSERT INTO tool_call_authorizations(call_id,run_id,provider,operation,attempt,request_hash,authorization_hash,permit_hash,price_version,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, req.CallID, req.RunID, req.Provider, req.Operation, req.Attempt, req.RequestHash, authorizationHash, hash(result.Permit), priceVersion, result.ExpiresAt)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

// RecordTool settles already accepted supplier facts after cancellation or
// credential revocation; it cannot authorize another supplier request.
func (s *CallService) RecordTool(ctx context.Context, event ToolUsageEvent, permit string) (bool, error) {
	if !validIdentity(event.EventID) || !validIdentity(event.CallID) || !validTool(event.Provider, event.Operation) || event.EventVersion != 1 || event.Attempt < 1 || event.Attempt > 100 || len(event.ProviderRequestID) > 512 || len(event.Outcome) == 0 || len(event.Outcome) > 64 || event.HTTPStatus < 0 || event.HTTPStatus > 599 || event.ResultCount < 0 || event.ResultCount > 10000 || (event.UsageStatus != "reported" && event.UsageStatus != "unknown") {
		return false, pkgerrors.ErrBadRequest
	}
	if event.UsageStatus == "unknown" && event.ResultCount != 0 {
		return false, pkgerrors.ErrBadRequest
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.Background())
	var runID, provider, operation, expectedPermit, priceVersion string
	var attempt int
	err = tx.QueryRow(ctx, `SELECT run_id,provider,operation,attempt,permit_hash,price_version FROM tool_call_authorizations WHERE call_id=$1 FOR UPDATE`, event.CallID).Scan(&runID, &provider, &operation, &attempt, &expectedPermit, &priceVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, pkgerrors.ErrForbidden
	}
	if err != nil {
		return false, err
	}
	if !hmac.Equal([]byte(hash(permit)), []byte(expectedPermit)) {
		return false, pkgerrors.ErrForbidden
	}
	if event.Attempt != attempt || event.Provider != provider || event.Operation != operation {
		return false, pkgerrors.ErrBadRequest
	}
	eventHash := hash(event)
	var existing string
	err = tx.QueryRow(ctx, `SELECT event_hash FROM tool_usage_events WHERE event_id=$1 OR call_id=$2`, event.EventID, event.CallID).Scan(&existing)
	if err == nil {
		if existing != eventHash {
			return false, pkgerrors.ErrConflict
		}
		return true, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	var tenantID, analysisID, actorID string
	if err = tx.QueryRow(ctx, `SELECT tenant_id,analysis_id,COALESCE(actor_user_id,'') FROM analysis_runs WHERE id=$1`, runID).Scan(&tenantID, &analysisID, &actorID); err != nil {
		return false, err
	}
	var cost *int64
	costStatus := "pending"
	// The published CNY tool tariff applies only to successful nonempty calls.
	// An HTTP error or uncertain response remains pending reconciliation.
	if event.UsageStatus == "reported" && priceVersion != "" && event.HTTPStatus >= 200 && event.HTTPStatus < 300 && (event.Outcome == "success" || event.Outcome == "empty" || event.Outcome == "provider_response") {
		amount := int64(0)
		if event.ResultCount > 0 {
			amount = 5000
		}
		cost = &amount
		costStatus = "known"
	}
	_, err = tx.Exec(ctx, `INSERT INTO tool_usage_events(event_id,call_id,run_id,tenant_id,analysis_id,actor_user_id,provider,operation,event_version,attempt,outcome,http_status,result_count,usage_status,cost_micro_cny,cost_status,provider_price_version,provider_request_id,event_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`, event.EventID, event.CallID, runID, tenantID, analysisID, actorID, event.Provider, event.Operation, event.EventVersion, event.Attempt, event.Outcome, event.HTTPStatus, event.ResultCount, event.UsageStatus, cost, costStatus, priceVersion, event.ProviderRequestID, eventHash)
	if err != nil {
		if db.IsUniqueViolation(err) {
			return false, pkgerrors.ErrConflict
		}
		return false, err
	}
	return false, tx.Commit(ctx)
}
