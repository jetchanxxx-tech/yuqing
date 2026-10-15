package v1_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
)

func semanticIntentBody(run any, topic string) map[string]any {
	return map[string]any{"run_id":run,"version":"semantic-v2","model":"sandbox-model","intent":map[string]any{"version":"semantic-v2","topic":topic,"original_keywords":[]string{"iphone18","iphone18pro","iphone18max"},"entities":[]string{"iPhone18 family"},"aliases":[]string{},"relations":[]string{"related variants"},"focus":[]string{},"non_assumptions":[]string{"publication"},"search_queries":[]string{"iPhone18 Pro review"},"model":"sandbox-model"}}
}

func TestSemanticIntentPGActualEndpointImmutableConcurrentAndReconstructed(t *testing.T) {
	t.Setenv("YUQING_BILLING_SERVICE_TOKEN","isolated-semantic-secret")
	e:=newBillingActorPGEnv(t)
	token,_,actor:=mustRegister(t,e.router,"semantic-owner@example.invalid","Semantic owner")
	created:=adminContractResponse(t,doReq(t,e.router,http.MethodPost,"/api/v1/analyses",token,map[string]any{"name":"iPhone family","keywords":[]string{"iphone18","iphone18pro","iphone18max"},"sources":[]string{"toutiao"}}),http.StatusCreated)
	if version,_:=created["research_intent_version"].(string); version!="" {t.Fatalf("pre-run deterministic guess was presented as accepted model intent: %v",created)}
	ctx:=context.Background();id:=created["id"].(string);run:=created["current_run_id"].(string)
	if err:=e.deps.Analysis.TransitionRun(ctx,actor["tenant_id"].(string),id,run,"acquiring_budget");err!=nil {t.Fatal(err)}
	if err:=e.deps.Analysis.TransitionRun(ctx,actor["tenant_id"].(string),id,run,"fetching");err!=nil {t.Fatal(err)}
	path:="/internal/v1/analyses/"+id+"/research-intent"
	body:=semanticIntentBody(run,"iPhone18 family feedback")
	adminContractResponse(t,internalBillingRequest(e,path,body,"",""),http.StatusForbidden)
	start:=make(chan struct{});responses:=make(chan int,8);var workers sync.WaitGroup
	for i:=0;i<8;i++ {workers.Add(1);go func(){defer workers.Done();<-start;responses<-internalBillingRequest(e,path,body,"","isolated-semantic-secret").Code}()}
	close(start);workers.Wait();close(responses)
	createdCount:=0
	for status:=range responses {if status==http.StatusCreated {createdCount++} else if status!=http.StatusOK {t.Fatalf("concurrent intent write status=%d",status)}}
	if createdCount!=1 {t.Fatalf("immutable snapshot created %d times",createdCount)}
	e.rebuild()
	stored:=adminContractResponse(t,internalBillingRequest(e,path,body,"","isolated-semantic-secret"),http.StatusOK)
	if stored["intent"].(map[string]any)["topic"]!="iPhone18 family feedback" {t.Fatal("snapshot lost after reconstruction")}
	adminContractResponse(t,internalBillingRequest(e,path,semanticIntentBody(run,"replace original topic"),"","isolated-semantic-secret"),http.StatusConflict)
	var raw []byte
	if err:=e.pool.QueryRow(ctx,`SELECT research_intent FROM analysis_runs WHERE id=$1`,run).Scan(&raw);err!=nil {t.Fatal(err)}
	var intent map[string]any;if err:=json.Unmarshal(raw,&intent);err!=nil || intent["topic"]!="iPhone18 family feedback" {t.Fatalf("durable run snapshot=%s / %v",raw,err)}
	adminContractResponse(t,doReq(t,e.router,http.MethodPost,"/api/v1/analyses/"+id+"/cancel",token,nil),http.StatusOK)
	adminContractResponse(t,internalBillingRequest(e,path,body,"","isolated-semantic-secret"),http.StatusConflict)
	adminContractResponse(t,doReq(t,e.router,http.MethodPost,"/api/v1/analyses/"+id+"/rerun",token,nil),http.StatusOK)
	next:=adminContractResponse(t,doReq(t,e.router,http.MethodGet,"/api/v1/analyses/"+id,token,nil),http.StatusOK)
	if next["current_run_id"]==run || next["research_intent_version"]=="semantic-v2" {t.Fatalf("rerun reused previous immutable snapshot: %v",next)}
	if err:=e.pool.QueryRow(ctx,`SELECT research_intent FROM analysis_runs WHERE id=$1`,run).Scan(&raw);err!=nil {t.Fatal(err)}
	if err:=json.Unmarshal(raw,&intent);err!=nil || intent["topic"]!="iPhone18 family feedback" {t.Fatalf("rerun erased historical intent: %s / %v",raw,err)}
}

func TestSemanticQueryPlanningUsageRequiresFetchingAndStopsAfterCancellation(t *testing.T) {
	t.Setenv("YUQING_BILLING_SERVICE_TOKEN","isolated-semantic-secret")
	e:=newBillingActorPGEnv(t)
	token,_,actor:=mustRegister(t,e.router,"semantic-planner@example.invalid","Planner")
	created:=adminContractResponse(t,doReq(t,e.router,http.MethodPost,"/api/v1/analyses",token,map[string]any{"name":"iPhone family"}),http.StatusCreated)
	path:="/internal/v1/billing/llm-authorizations";run:=created["current_run_id"].(string);id:=created["id"].(string)
	body:=map[string]any{"call_id":"query-plan-call","run_id":run,"engine":"query","phase":"plan","attempt":1,"model":"sandbox-model"}
	adminContractResponse(t,internalBillingRequest(e,path,body,"","isolated-semantic-secret"),http.StatusConflict)
	ctx:=context.Background()
	for _,state:=range []string{"acquiring_budget","fetching"} {if err:=e.deps.Analysis.TransitionRun(ctx,actor["tenant_id"].(string),id,run,state);err!=nil {t.Fatal(err)}}
	authorized:=adminContractResponse(t,internalBillingRequest(e,path,body,"","isolated-semantic-secret"),http.StatusCreated)
	adminContractResponse(t,doReq(t,e.router,http.MethodPost,"/api/v1/analyses/"+id+"/cancel",token,nil),http.StatusOK)
	body["call_id"]="query-after-cancel"
	adminContractResponse(t,internalBillingRequest(e,path,body,"","isolated-semantic-secret"),http.StatusConflict)
	event:=map[string]any{"event_id":"query-plan-event","call_id":"query-plan-call","event_version":1,"attempt":1,"actual_model":"sandbox-model","usage_status":"reported","prompt_tokens":40,"completion_tokens":20,"cache_tokens":0,"outcome":"success"}
	adminContractResponse(t,internalBillingRequest(e,"/internal/v1/billing/usage-events",event,authorized["permit"].(string),"isolated-semantic-secret"),http.StatusCreated)
	var rows int;if err:=e.pool.QueryRow(ctx,`SELECT count(*) FROM usage_events WHERE run_id=$1 AND engine='query'`,run).Scan(&rows);err!=nil || rows!=1 {t.Fatalf("late query fact lost: %d / %v",rows,err)}
}

func TestSemanticIntentPGCallbackRollbackKeepsBothViewsUnset(t *testing.T) {
	t.Setenv("YUQING_BILLING_SERVICE_TOKEN","isolated-semantic-secret")
	e:=newBillingActorPGEnv(t)
	token,_,actor:=mustRegister(t,e.router,"semantic-rollback@example.invalid","Rollback")
	created:=adminContractResponse(t,doReq(t,e.router,http.MethodPost,"/api/v1/analyses",token,map[string]any{"name":"rollback"}),http.StatusCreated)
	ctx:=context.Background();id:=created["id"].(string);run:=created["current_run_id"].(string)
	for _,state:=range []string{"acquiring_budget","fetching"} {if err:=e.deps.Analysis.TransitionRun(ctx,actor["tenant_id"].(string),id,run,state);err!=nil {t.Fatal(err)}}
	if _,err:=e.pool.Exec(ctx,`CREATE FUNCTION semantic_reject_view() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.research_intent_version='semantic-v2' THEN RAISE EXCEPTION 'semantic_view_rejected'; END IF; RETURN NEW; END; $$; CREATE TRIGGER semantic_reject_view BEFORE UPDATE ON analyses FOR EACH ROW EXECUTE FUNCTION semantic_reject_view()`);err!=nil {t.Fatal(err)}
	adminContractResponse(t,internalBillingRequest(e,"/internal/v1/analyses/"+id+"/research-intent",semanticIntentBody(run,"must rollback"),"","isolated-semantic-secret"),http.StatusInternalServerError)
	var runVersion,viewVersion string
	if err:=e.pool.QueryRow(ctx,`SELECT r.research_intent_version,a.research_intent_version FROM analysis_runs r JOIN analyses a ON a.id=r.analysis_id WHERE r.id=$1`,run).Scan(&runVersion,&viewVersion);err!=nil {t.Fatal(err)}
	if runVersion!="" || viewVersion!="" {t.Fatalf("snapshot partial commit after view rejection: %q/%q",runVersion,viewVersion)}
}

func TestSemanticAuditPGCancellationAndStaleRunCannotLoseOrOverwriteFacts(t *testing.T) {
	t.Setenv("YUQING_BILLING_SERVICE_TOKEN","isolated-semantic-secret")
	e:=newBillingActorPGEnv(t)
	token,_,actor:=mustRegister(t,e.router,"semantic-audit@example.invalid","Audit")
	created:=adminContractResponse(t,doReq(t,e.router,http.MethodPost,"/api/v1/analyses",token,map[string]any{"name":"audit"}),http.StatusCreated)
	id:=created["id"].(string);run:=created["current_run_id"].(string)
	adminContractResponse(t,doReq(t,e.router,http.MethodPost,"/api/v1/analyses/"+id+"/cancel",token,nil),http.StatusOK)
	coverage:=map[string]any{"admission_version":"semantic-v2","provider_candidates":3,"accepted_count":0,"direct_count":0,"context_count":0,"per_keyword":[]any{},"per_platform":[]any{},"candidate_audit":[]any{map[string]any{"id":"canceled-candidate","status":"unprocessed","reason":"canceled"}}}
	body:=map[string]any{"run_id":run,"tenant_id":actor["tenant_id"],"coverage":coverage}
	path:="/internal/v1/analyses/"+id+"/retrieval-audit"
	adminContractResponse(t,internalBillingRequest(e,path,body,"","isolated-semantic-secret"),http.StatusOK)
	adminContractResponse(t,doReq(t,e.router,http.MethodPost,"/api/v1/analyses/"+id+"/rerun",token,nil),http.StatusOK)
	coverage["provider_candidates"]=4
	adminContractResponse(t,internalBillingRequest(e,path,body,"","isolated-semantic-secret"),http.StatusOK)
	e.rebuild()
	current:=adminContractResponse(t,doReq(t,e.router,http.MethodGet,"/api/v1/analyses/"+id,token,nil),http.StatusOK)
	if current["retrieval_coverage"]!=nil {t.Fatalf("late historical audit contaminated new run: %v",current)}
	var raw []byte;if err:=e.pool.QueryRow(context.Background(),`SELECT retrieval_coverage FROM analysis_runs WHERE id=$1`,run).Scan(&raw);err!=nil {t.Fatal(err)}
	var recorded map[string]any;if err:=json.Unmarshal(raw,&recorded);err!=nil || recorded["provider_candidates"]!=float64(4) {t.Fatalf("canceled historical audit lost: %s / %v",raw,err)}
}
