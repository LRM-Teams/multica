package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/problemevolution"
)

func TestProblemEvolutionEnabledRequiresConfiguredEvolver(t *testing.T) {
	// The capability must not be advertised without a local evolver, otherwise
	// the server queues runs to a machine that cannot execute them.
	d := &Daemon{}
	if d.problemEvolutionEnabled() {
		t.Fatal("expected the capability to be disabled without an evolver path")
	}
	d.cfg.ProblemEvolutionEvolverPath = "/usr/local/bin/evolver"
	if !d.problemEvolutionEnabled() {
		t.Fatal("expected the capability to be enabled with an evolver path")
	}
}

func TestBeginProblemEvolutionRunIsSingleFlightPerRuntime(t *testing.T) {
	d := &Daemon{}
	if !d.beginProblemEvolutionRun("runtime-1") {
		t.Fatal("expected the first claim attempt to be admitted")
	}
	if d.beginProblemEvolutionRun("runtime-1") {
		t.Fatal("expected a second concurrent batch on the same runtime to be refused")
	}
	if !d.beginProblemEvolutionRun("runtime-2") {
		t.Fatal("expected a different runtime to be admitted")
	}
	d.finishProblemEvolutionRun("runtime-1")
	if !d.beginProblemEvolutionRun("runtime-1") {
		t.Fatal("expected the runtime to be claimable again after finishing")
	}
}

func TestSplitEvolverArgs(t *testing.T) {
	if got := splitEvolverArgs("  "); got != nil {
		t.Fatalf("expected nil for blank args, got %v", got)
	}
	got := splitEvolverArgs(" -m my_evolver.cli  --verbose ")
	want := []string{"-m", "my_evolver.cli", "--verbose"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

func TestPrepareProblemEvolutionWorkdirWritesInput(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	d := &Daemon{}
	claim := problemEvolutionClaim{ClaimToken: "token"}
	claim.Run.ID = "11111111-2222-4333-8444-555555555555"
	claim.Input = problemevolution.EvolverInput{
		SchemaVersion: problemevolution.SchemaVersion,
		RunID:         claim.Run.ID,
		Mode:          problemevolution.ModeSolution,
		Problem:       problemevolution.ProblemSpec{Statement: "solve it"},
		Budget:        problemevolution.DefaultBudget(),
		Output: problemevolution.OutputConfig{
			ArtifactDir:       problemevolution.DefaultArtifactDir,
			CandidateManifest: problemevolution.DefaultCandidateManifest,
		},
	}

	workdir, err := d.prepareProblemEvolutionWorkdir(claim)
	if err != nil {
		t.Fatalf("prepare workdir: %v", err)
	}
	encoded, err := os.ReadFile(filepath.Join(workdir, problemevolution.InputFileName))
	if err != nil {
		t.Fatalf("read input.json: %v", err)
	}
	var decoded problemevolution.EvolverInput
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode input.json: %v", err)
	}
	if decoded.RunID != claim.Run.ID {
		t.Fatalf("input run_id = %q, want %q", decoded.RunID, claim.Run.ID)
	}
	if info, statErr := os.Stat(filepath.Join(workdir, problemevolution.DefaultArtifactDir)); statErr != nil || !info.IsDir() {
		t.Fatal("expected the artifact directory to be created")
	}
}

func TestProblemEvolutionEnvExcludesPlatformCredentials(t *testing.T) {
	// The evolver reaches scoring through the evaluator command, never through
	// Multica credentials, so none may leak into its environment.
	t.Setenv("MULTICA_DAEMON_TOKEN", "secret-token")
	t.Setenv("MULTICA_API_KEY", "secret-key")
	for _, entry := range problemEvolutionEnv("/tmp/workdir") {
		if entry == "MULTICA_DAEMON_TOKEN=secret-token" || entry == "MULTICA_API_KEY=secret-key" {
			t.Fatalf("credential leaked into the evolver environment: %q", entry)
		}
	}
}

func TestProblemEvolutionEnvForwardsOnlyExplicitProviderCredentials(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "provider-secret")
	t.Setenv("MULTICA_API_KEY", "platform-secret")
	env := problemEvolutionEnvWithForwarded("/tmp/workdir", []string{
		"OPENAI_API_KEY", "MULTICA_API_KEY", "invalid-name",
	})
	if !containsEnvEntry(env, "OPENAI_API_KEY=provider-secret") {
		t.Fatal("expected explicitly allowed provider credential")
	}
	if containsEnvEntry(env, "MULTICA_API_KEY=platform-secret") {
		t.Fatal("platform credential must never be forwarded")
	}
	if containsEnvEntry(env, "invalid-name="+os.Getenv("invalid-name")) {
		t.Fatal("invalid environment name must not be forwarded")
	}
}

func TestSplitEvolverEnv(t *testing.T) {
	got := splitEvolverEnv(" OPENAI_API_KEY, ANTHROPIC_API_KEY, MULTICA_API_KEY, bad-name ")
	want := []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY"}
	if len(got) != len(want) {
		t.Fatalf("env names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("env names = %v, want %v", got, want)
		}
	}
}

func containsEnvEntry(env []string, wanted string) bool {
	for _, entry := range env {
		if entry == wanted {
			return true
		}
	}
	return false
}

func TestForwardProblemEvolutionEventsRejectsOversizedOutput(t *testing.T) {
	d := &Daemon{}
	oversized := strings.NewReader(strings.Repeat("x", problemevolution.MaxEventLineBytes+1))
	err := d.forwardProblemEvolutionEvents(
		context.Background(), Runtime{}, problemEvolutionClaim{}, oversized, nil,
	)
	if err == nil {
		t.Fatal("expected oversized evolver output to fail event forwarding")
	}
}
