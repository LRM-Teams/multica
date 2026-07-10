package service

import (
	"context"
	"strings"
	"testing"

	agentpkg "github.com/multica-ai/multica/server/pkg/agent"
)

// fakeBackend is a minimal agentpkg.Backend for exercising DiagnosisAgentRunner
// without spawning a real agent subprocess.
type fakeBackend struct {
	gotPrompt string
	gotOpts   agentpkg.ExecOptions
	output    string
	status    string
	execErr   error
}

func (f *fakeBackend) Execute(_ context.Context, prompt string, opts agentpkg.ExecOptions) (*agentpkg.Session, error) {
	f.gotPrompt = prompt
	f.gotOpts = opts
	if f.execErr != nil {
		return nil, f.execErr
	}
	status := f.status
	if status == "" {
		status = "completed"
	}
	msgCh := make(chan agentpkg.Message)
	close(msgCh)
	resCh := make(chan agentpkg.Result, 1)
	resCh <- agentpkg.Result{Status: status, Output: f.output}
	close(resCh)
	return &agentpkg.Session{Messages: msgCh, Result: resCh}, nil
}

func TestParseStepRewards_Valid(t *testing.T) {
	in := "```json\n[{\"segment_id\":\"s1\",\"seq\":1,\"score\":8,\"rationale\":\"x\"},{\"segment_id\":\"s1\",\"seq\":2,\"score\":2}]\n```"
	got, err := parseStepRewards(in, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SegmentID != "s1" || got[0].Seq != 1 || got[0].Score != 8 {
		t.Fatalf("%+v", got)
	}
}

func TestParseStepRewards_ClampsAndSkips(t *testing.T) {
	in := `[{"segment_id":"s1","seq":1,"score":99},{"segment_id":"s1","seq":-1,"score":5}]`
	got, _ := parseStepRewards(in, 10) // 99 clamps to 10; seq=-1 skipped
	if len(got) != 1 || got[0].Score != 10 {
		t.Fatalf("%+v", got)
	}
}

func TestParseStepRewards_Empty(t *testing.T) {
	got, err := parseStepRewards("not json", 10)
	if err == nil || len(got) != 0 {
		t.Fatalf("expected empty+err, got %+v %v", got, err)
	}
}

// TestSystemPrompt_EmbedsScoreMax verifies the system prompt carries the
// concrete [0, scoreMax] scoring range rather than an unspecified "specified
// range", so the model scores within valid bounds.
func TestSystemPrompt_EmbedsScoreMax(t *testing.T) {
	r, err := NewDiagnosisAgentRunner(DiagnosisAgentConfig{ScoreMax: 7, Backend: &fakeBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	p := r.systemPrompt()
	if !strings.Contains(p, "between 0 and 7 inclusive") {
		t.Fatalf("system prompt missing embedded range: %q", p)
	}
}

// TestNewDiagnosisAgentRunner_ErrorOnUnknownProvider verifies the constructor
// surfaces backend-creation failures instead of returning a runner with a nil
// backend that silently fails at Diagnose time.
func TestNewDiagnosisAgentRunner_ErrorOnUnknownProvider(t *testing.T) {
	r, err := NewDiagnosisAgentRunner(DiagnosisAgentConfig{Provider: "nonexistent-agent-type"})
	if err == nil {
		t.Fatalf("expected error for unknown provider, got runner %+v", r)
	}
	if r != nil {
		t.Fatalf("expected nil runner on error, got %+v", r)
	}
}

// TestNewDiagnosisAgentRunner_InjectsBackend verifies a caller-supplied Backend
// is used as-is (no agentpkg.New call) and config defaults are applied.
func TestNewDiagnosisAgentRunner_InjectsBackend(t *testing.T) {
	fb := &fakeBackend{}
	r, err := NewDiagnosisAgentRunner(DiagnosisAgentConfig{Provider: "pi", ScoreMax: 5, Backend: fb})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil || r.scoreMax != 5 {
		t.Fatalf("bad runner: %+v", r)
	}
}

// TestDiagnose_ParsesStepRewards exercises the full Diagnose flow against a
// fake backend: the runner wires in systemPrompt(), adds --no-tools for the pi
// provider, and returns parsed StepRewards from the backend's JSON output.
func TestDiagnose_ParsesStepRewards(t *testing.T) {
	fb := &fakeBackend{
		status: "completed",
		output: `[{"segment_id":"seg-a","seq":1,"score":7,"rationale":"good"},{"segment_id":"seg-a","seq":2,"score":12,"rationale":"clamped"}]`,
	}
	r, err := NewDiagnosisAgentRunner(DiagnosisAgentConfig{ScoreMax: 10, Backend: fb})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := r.Diagnose(context.Background(), "proj-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0].SegmentID != "seg-a" || got[0].Score != 7 || got[1].Score != 10 {
		t.Fatalf("%+v", got)
	}
	if !strings.Contains(fb.gotOpts.SystemPrompt, "between 0 and 10 inclusive") {
		t.Fatalf("system prompt missing range: %q", fb.gotOpts.SystemPrompt)
	}
	foundNoTools := false
	for _, a := range fb.gotOpts.CustomArgs {
		if a == "--no-tools" {
			foundNoTools = true
		}
	}
	if !foundNoTools {
		t.Fatalf("expected --no-tools in custom args for pi provider, got %v", fb.gotOpts.CustomArgs)
	}
}

// TestDiagnose_PropagatesNonCompleted verifies a non-completed backend result
// surfaces as an error rather than being parsed as success.
func TestDiagnose_PropagatesNonCompleted(t *testing.T) {
	fb := &fakeBackend{status: "failed", output: ""}
	r, err := NewDiagnosisAgentRunner(DiagnosisAgentConfig{ScoreMax: 10, Backend: fb})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := r.Diagnose(context.Background(), "proj-1"); err == nil {
		t.Fatal("expected error for non-completed diagnosis, got nil")
	}
}
