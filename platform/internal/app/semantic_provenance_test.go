package app

import (
	"encoding/json"
	"testing"

	"github.com/yuqing/platform/internal/business/analysis"
)

func TestSemanticProvenanceSurvivesDownstreamAdapter(t *testing.T) {
	docs:=[]analysis.Document{{ID:"d1",URL:"https://www.toutiao.com/article/1",Content:"original quote",SourceType:"toutiao",ContentKind:"body",FetchStatus:"body",FetchReason:"static",FinalURL:"https://www.toutiao.com/article/1",SemanticClassification:"direct",SemanticReason:"related model",EvidenceSpans:[]string{"original quote"}}}
	got:=toEngineDocuments(docs)
	raw,err:=json.Marshal(got);if err!=nil {t.Fatal(err)}
	var decoded []map[string]any;if err=json.Unmarshal(raw,&decoded);err!=nil {t.Fatal(err)}
	for _,field:=range []string{"content_kind","fetch_status","fetch_reason","final_url","semantic_classification","semantic_reason","evidence_spans"} {
		if _,exists:=decoded[0][field];!exists {t.Errorf("downstream LLM lost source provenance %s: %s",field,raw)}
	}
}
