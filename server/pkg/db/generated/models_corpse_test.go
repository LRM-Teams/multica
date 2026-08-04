package db

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestModelsGoHasNoRadarCorpseTypes enforces task #85 P0: Frank deleted
// workspace radar long ago (migration 257_kill_workspace_radar dropped the
// tables). models.go must not keep AgentRadarAction / AgentRadarRun as
// zombie types after that cut.
//
// A full sqlc identity gate against the entire models.go surface is P1+
// (chat core drift). This test is the thin anti-corpse rail: if someone
// re-introduces radar tables into the sqlc schema, or re-pastes the
// corpses by hand, CI fails here before the types spread.
func TestModelsGoHasNoRadarCorpseTypes(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	modelsPath := filepath.Join(filepath.Dir(thisFile), "models.go")
	body, err := os.ReadFile(modelsPath)
	if err != nil {
		t.Fatalf("read models.go: %v", err)
	}
	src := string(body)
	for _, corpse := range []string{
		"type AgentRadarAction struct",
		"type AgentRadarRun struct",
	} {
		if strings.Contains(src, corpse) {
			t.Errorf("models.go still contains corpse %q — radar tables were dropped; delete the type (task #85 P0)", corpse)
		}
	}
}
