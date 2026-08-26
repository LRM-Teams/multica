package researchrun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const frozenV6ContractSHA256 = "1ea2c7feef1058fc6feb30568fde821412240236ca72d86c448acf33ec6c5655"

func TestV6DesignContractIsFrozenAndNotProductionEnabled(t *testing.T) {
	path := filepath.Join("..", "..", "..", "docs", "contracts", "research-run-v6-director.schema.json")
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
		"director_brief", "director_action_proposal", "work_manifest",
		"atomic_result_submission", "discussion_turn_submission",
		"integration_submission", "report_package_submission",
		"projection_snapshot", "projection_delta",
	} {
		if _, exists := definitions[required]; !exists {
			t.Errorf("V6 contract missing definition %q", required)
		}
	}
	if OrchestratorVersion != OrchestratorVersionV5 {
		t.Fatalf("omitted-version default=%q; old clients must still create V5", OrchestratorVersion)
	}
	if err = ensureSupportedOrchestratorVersion("research-run-v6"); err != nil {
		t.Fatalf("V6 must be a supported runtime version: %v", err)
	}
}
