package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEvolutionModelRuntimeConfigStartsOffAndRequiresCandidate(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	listReq := newRequest(http.MethodGet, "/api/evolution/model-configs?workspace_id="+testWorkspaceID, nil)
	listRec := httptest.NewRecorder()
	testHandler.ListEvolutionModelRuntimeConfigs(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("ListEvolutionModelRuntimeConfigs: status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	var list struct {
		Configs []evolutionModelRuntimeConfigResponse `json:"configs"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode configs: %v", err)
	}
	if len(list.Configs) != 2 || list.Configs[0].Mode != "off" || list.Configs[1].Mode != "off" {
		t.Fatalf("configs=%+v, want two disabled model stubs", list.Configs)
	}

	badReq := withURLParam(newRequest(http.MethodPut, "/api/evolution/model-configs/attention_student?workspace_id="+testWorkspaceID, map[string]any{"mode": "shadow"}), "modelKind", "attention_student")
	badRec := httptest.NewRecorder()
	testHandler.UpdateEvolutionModelRuntimeConfig(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("shadow without candidate status=%d body=%s, want 400", badRec.Code, badRec.Body.String())
	}

	okReq := withURLParam(newRequest(http.MethodPut, "/api/evolution/model-configs/attention_student?workspace_id="+testWorkspaceID, map[string]any{"mode": "shadow", "candidate_version": "attn-shadow-v1", "rollout_percent": 10}), "modelKind", "attention_student")
	okRec := httptest.NewRecorder()
	testHandler.UpdateEvolutionModelRuntimeConfig(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Fatalf("UpdateEvolutionModelRuntimeConfig: status=%d body=%s", okRec.Code, okRec.Body.String())
	}
	var updated evolutionModelRuntimeConfigResponse
	if err := json.Unmarshal(okRec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated config: %v", err)
	}
	if updated.Mode != "shadow" || updated.CandidateVersion != "attn-shadow-v1" || updated.RolloutPercent != 10 {
		t.Fatalf("updated config=%+v", updated)
	}
}

func TestEvolutionModelEvalRunComputesAttentionAccuracy(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	if _, err := testPool.Exec(context.Background(), `
		DELETE FROM evolution_training_example
		WHERE workspace_id = $1 AND model_kind = 'attention_student' AND split = 'holdout' AND status = 'gold'
	`, testWorkspaceID); err != nil {
		t.Fatalf("clean examples: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO evolution_training_example (workspace_id, model_kind, source_kind, input, teacher_label, student_prediction, split, status)
		VALUES
		($1, 'attention_student', 'manual', '{"sample":1}'::jsonb, '{"decision":"ANSWER"}'::jsonb, '{"decision":"ANSWER"}'::jsonb, 'holdout', 'gold'),
		($1, 'attention_student', 'manual', '{"sample":2}'::jsonb, '{"decision":"SILENT"}'::jsonb, '{"decision":"ANSWER"}'::jsonb, 'holdout', 'gold')
	`, testWorkspaceID); err != nil {
		t.Fatalf("seed examples: %v", err)
	}

	req := newRequest(http.MethodPost, "/api/evolution/model-evals?workspace_id="+testWorkspaceID, map[string]any{
		"model_kind":     "attention_student",
		"model_version":  "offline-eval-test",
		"dataset_filter": map[string]any{"split": "holdout", "status": "gold"},
	})
	rec := httptest.NewRecorder()
	testHandler.CreateEvolutionModelEvalRun(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateEvolutionModelEvalRun: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var run evolutionModelEvalRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode eval run: %v", err)
	}
	if run.ExampleCount < 2 || run.Metrics["accuracy"].(float64) <= 0 || run.Metrics["accuracy"].(float64) >= 1 {
		t.Fatalf("eval run metrics=%+v example_count=%d, want partial accuracy over seeded examples", run.Metrics, run.ExampleCount)
	}
}
