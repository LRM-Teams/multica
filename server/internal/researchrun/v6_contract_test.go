package researchrun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const frozenV6ContractSHA256 = "f3f2c5f39d8d9490ad84d081f09f8245023dc22ae188935f54b5f393ff58bdcd"

func TestV6DesignContractIsFrozenAndNotProductionEnabled(t *testing.T) {
	path := filepath.Join("..", "..", "..", "docs", "contracts", "research-run-v6.schema.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err = json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("V6 contract is not valid JSON: %v", err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != frozenV6ContractSHA256 {
		t.Fatalf("V6 contract hash=%s want=%s; semantic changes require research-run-v7", got, frozenV6ContractSHA256)
	}
	definitions, ok := schema["$defs"].(map[string]any)
	if !ok {
		t.Fatal("V6 contract has no $defs object")
	}
	for _, required := range []string{
		"plan_result", "task_result", "prompt_context", "gate_input",
		"question", "hypothesis", "branch", "inquiry_edge", "task",
		"search_plan", "query_execution", "source_candidate", "status_update",
		"integration_contribution", "insight", "dispute", "divergence",
		"report_revision", "evaluation",
	} {
		if _, exists := definitions[required]; !exists {
			t.Errorf("V6 contract missing definition %q", required)
		}
	}
	if OrchestratorVersion != OrchestratorVersionV5 {
		t.Fatalf("default orchestrator=%q; V6 design must remain disabled", OrchestratorVersion)
	}
	if err = ensureSupportedOrchestratorVersion("research-run-v6"); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("V6 became production-supported before E-K exits: %v", err)
	}
}
