package usage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuqing/platform/internal/config"
	"github.com/yuqing/platform/internal/pkg/db"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/llm"
	"github.com/yuqing/platform/internal/platform/billing"
	"github.com/yuqing/platform/internal/platform/billingpolicy"
)

type CallAuthorizationRequest struct {
	CallID  string `json:"call_id"`
	RunID   string `json:"run_id"`
	Engine  string `json:"engine"`
	Phase   string `json:"phase"`
	Attempt int    `json:"attempt"`
	Model   string `json:"model"`
}
type CallAuthorization struct {
	CallID    string    `json:"call_id"`
	ExpiresAt time.Time `json:"expires_at"`
	Permit    string    `json:"permit"`
	Duplicate bool      `json:"-"`
}
type ProviderUsageEvent struct {
	EventID           string `json:"event_id"`
	CallID            string `json:"call_id"`
	EventVersion      int    `json:"event_version"`
	Attempt           int    `json:"attempt"`
	ActualModel       string `json:"actual_model"`
	ProviderRequestID string `json:"provider_request_id"`
	UsageStatus       string `json:"usage_status"`
	PromptTokens      int64  `json:"prompt_tokens"`
	CompletionTokens  int64  `json:"completion_tokens"`
	CacheTokens       int64  `json:"cache_tokens"`
	Outcome           string `json:"outcome"`
}
type priceSnapshot struct {
	Model    string  `json:"model"`
	Provider string  `json:"provider"`
	Version  string  `json:"version"`
	Input    float64 `json:"input_cny_per_m"`
	Output   float64 `json:"output_cny_per_m"`
}
type CallService struct {
	pool   *pgxpool.Pool
	secret string
	prices map[string]priceSnapshot
}

func NewCallService(pool *pgxpool.Pool, secret, priceVersion string, models []config.ModelConfig) *CallService {
	s := &CallService{pool: pool, secret: secret, prices: map[string]priceSnapshot{}}
	for _, m := range models {
		if priceVersion != "" && m.InputCostPerM >= 0 && m.OutputCostPerM >= 0 && (m.InputCostPerM > 0 || m.OutputCostPerM > 0) && !math.IsInf(m.InputCostPerM, 0) && !math.IsNaN(m.InputCostPerM) && !math.IsInf(m.OutputCostPerM, 0) && !math.IsNaN(m.OutputCostPerM) {
			s.prices[m.ID] = priceSnapshot{Model: m.ID, Provider: m.Provider, Version: priceVersion, Input: m.InputCostPerM, Output: m.OutputCostPerM}
		}
	}
	return s
}
func hash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (s *CallService) permit(callID, requestHash string) string {
	mac := hmac.New(sha256.New, []byte(s.secret))
	mac.Write([]byte(callID + "\x00" + requestHash))
	return hex.EncodeToString(mac.Sum(nil))
}
func validIdentity(s string) bool { return strings.TrimSpace(s) == s && len(s) > 0 && len(s) <= 128 }

func (s *CallService) Authorize(ctx context.Context, req CallAuthorizationRequest) (CallAuthorization, error) {
	if !validIdentity(req.CallID) || !validIdentity(req.RunID) || !validIdentity(req.Model) || req.Attempt < 1 || req.Attempt > 100 || !((req.Engine == "insight" && req.Phase == "analyze") || (req.Engine == "report" && req.Phase == "generate")) {
		return CallAuthorization{}, pkgerrors.ErrBadRequest
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CallAuthorization{}, err
	}
	defer tx.Rollback(context.Background())
	requestHash := hash(req)
	result := CallAuthorization{CallID: req.CallID, Permit: s.permit(req.CallID, requestHash)}
	// Serialize duplicate authorization requests without allowing a client call ID
	// to be rebound to a different run or actual provider attempt.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,741916))`, req.CallID); err != nil {
		return result, err
	}
	var previous string
	err = tx.QueryRow(ctx, `SELECT request_hash,expires_at FROM llm_call_authorizations WHERE call_id=$1`, req.CallID).Scan(&previous, &result.ExpiresAt)
	duplicate := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	if duplicate && previous != requestHash {
		return result, pkgerrors.ErrConflict
	}
	if duplicate && !result.ExpiresAt.After(time.Now()) {
		return result, pkgerrors.ErrForbidden
	}
	var tenantID, analysisID, actorID, keyID, chargeMode, runState string
	err = tx.QueryRow(ctx, `SELECT tenant_id,analysis_id,COALESCE(actor_user_id,''),COALESCE(actor_api_key_id,''),charge_mode,state FROM analysis_runs WHERE id=$1`, req.RunID).Scan(&tenantID, &analysisID, &actorID, &keyID, &chargeMode, &runState)
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
	if current != req.RunID || state == "completed" || state == "failed" || state == "canceled" || runState != state {
		return result, pkgerrors.ErrConflict
	}
	budget, err := billing.NewEntitlementService(s.pool).EffectiveBudgetTx(ctx, tx, tenantID)
	if err != nil {
		return result, err
	}
	if chargeMode != "exempt" && budget.Mode == llm.BudgetHardCap {
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
	snapshot := s.prices[req.Model]
	priceJSON, err := json.Marshal(snapshot)
	if err != nil {
		return result, err
	}
	result.ExpiresAt = time.Now().UTC().Add(2 * time.Minute)
	_, err = tx.Exec(ctx, `INSERT INTO llm_call_authorizations(call_id,run_id,engine,phase,attempt,requested_model,provider_price_version,price_snapshot,expires_at,permit_hash,request_hash) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,$11)`, req.CallID, req.RunID, req.Engine, req.Phase, req.Attempt, req.Model, snapshot.Version, priceJSON, result.ExpiresAt, hash(result.Permit), requestHash)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

func (s *CallService) Record(ctx context.Context, event ProviderUsageEvent, permit string) (bool, error) {
	if !validIdentity(event.EventID) || !validIdentity(event.CallID) || !validIdentity(event.ActualModel) || event.EventVersion != 1 || event.Attempt < 1 || len(event.ProviderRequestID) > 512 || len(event.Outcome) > 64 || event.Outcome == "" {
		return false, pkgerrors.ErrBadRequest
	}
	for _, n := range []int64{event.PromptTokens, event.CompletionTokens, event.CacheTokens} {
		if n < 0 || n > math.MaxInt32 {
			return false, pkgerrors.ErrBadRequest
		}
	}
	if (event.UsageStatus != "reported" && event.UsageStatus != "unknown") || event.CacheTokens > event.PromptTokens {
		return false, pkgerrors.ErrBadRequest
	}
	if event.UsageStatus == "unknown" && (event.PromptTokens != 0 || event.CompletionTokens != 0 || event.CacheTokens != 0) {
		return false, pkgerrors.ErrBadRequest
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.Background())
	var runID, engine, expectedPermit string
	var attempt int
	var snapshotRaw []byte
	err = tx.QueryRow(ctx, `SELECT run_id,engine,attempt,price_snapshot,permit_hash FROM llm_call_authorizations WHERE call_id=$1 FOR UPDATE`, event.CallID).Scan(&runID, &engine, &attempt, &snapshotRaw, &expectedPermit)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, pkgerrors.ErrForbidden
	}
	if err != nil {
		return false, err
	}
	if !hmac.Equal([]byte(hash(permit)), []byte(expectedPermit)) {
		return false, pkgerrors.ErrForbidden
	}
	if event.Attempt != attempt {
		return false, pkgerrors.ErrBadRequest
	}
	eventHash := hash(event)
	var existing string
	err = tx.QueryRow(ctx, `SELECT event_hash FROM usage_events WHERE event_id=$1 OR call_id=$2`, event.EventID, event.CallID).Scan(&existing)
	if err == nil {
		if existing != eventHash {
			return false, pkgerrors.ErrConflict
		}
		return true, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	var tenantID, analysisID, actorID, mode string
	if err = tx.QueryRow(ctx, `SELECT tenant_id,analysis_id,COALESCE(actor_user_id,''),charge_mode FROM analysis_runs WHERE id=$1`, runID).Scan(&tenantID, &analysisID, &actorID, &mode); err != nil {
		return false, err
	}
	// No current-state authorization here: the provider has already spent tokens.
	// Late accepted facts settle after cancellation, suspension, or key revocation.
	exempt := mode == "exempt"
	quota := event.PromptTokens + event.CompletionTokens
	if exempt {
		quota = 0
	}
	var price priceSnapshot
	if err = json.Unmarshal(snapshotRaw, &price); err != nil {
		return false, err
	}
	var cost *int64
	costStatus := "pending"
	if event.UsageStatus == "reported" && price.Version != "" && price.Model == event.ActualModel {
		amount := float64(event.PromptTokens)*price.Input + float64(event.CompletionTokens)*price.Output
		if !math.IsInf(amount, 0) && amount >= 0 && amount < float64(math.MaxInt64) {
			n := int64(math.Round(amount))
			cost = &n
			costStatus = "known"
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO usage_events(tenant_id,user_id,model,analysis_id,engine,prompt_tokens,completion_tokens,cache_tokens,cost_micro_cny,billed_micro_cny,event_id,call_id,run_id,event_version,attempt,billing_exempt,quota_tokens,usage_status,cost_status,provider_price_version,provider_request_id,event_hash,outcome) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,0,$10,$11,$12,$13,$14,$15,$16,$17,$18,NULLIF($19,''),NULLIF($20,''),$21,$22)`, tenantID, actorID, event.ActualModel, analysisID, engine, event.PromptTokens, event.CompletionTokens, event.CacheTokens, cost, event.EventID, event.CallID, runID, event.EventVersion, event.Attempt, exempt, quota, event.UsageStatus, costStatus, price.Version, event.ProviderRequestID, eventHash, event.Outcome)
	if err != nil {
		if db.IsUniqueViolation(err) {
			return false, pkgerrors.ErrConflict
		}
		return false, err
	}
	return false, tx.Commit(ctx)
}
