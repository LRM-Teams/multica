package problemevolution

import "testing"

func validInput() EvolverInput {
	return EvolverInput{
		SchemaVersion: SchemaVersion,
		RunID:         "0f3a4d1e-0000-4000-8000-000000000001",
		Mode:          ModeSolution,
		Problem: ProblemSpec{
			Statement:    "Prove the bound and give the complexity.",
			ArtifactType: "markdown",
		},
		Evaluator: EvaluatorRef{
			ContractID:  "contract-1",
			ContentHash: "sha256:abc",
			Kind:        EvaluatorKindBuiltinDeterministic,
			Invoke: EvaluatorInvoke{
				Transport:  "cli",
				Command:    []string{"multica", "problem-evolution", "evaluate"},
				InputPath:  DefaultEvaluatorInput,
				OutputPath: DefaultEvaluatorOutput,
			},
		},
		Budget:   DefaultBudget(),
		Feedback: FeedbackBundle{Policy: DefaultFeedbackPolicy()},
		Output: OutputConfig{
			ArtifactDir:       DefaultArtifactDir,
			CandidateManifest: DefaultCandidateManifest,
		},
	}
}

func TestEvolverInputValidateAcceptsDefaults(t *testing.T) {
	if err := validInput().Validate(); err != nil {
		t.Fatalf("expected the default input to be valid, got %v", err)
	}
}

func TestEvolverInputRequiresPinnedEvaluatorHash(t *testing.T) {
	// Without a pinned hash there is nothing to re-check before scoring, so a
	// mid-run contract edit would go unnoticed.
	input := validInput()
	input.Evaluator.ContentHash = ""
	if err := input.Validate(); err == nil {
		t.Fatal("expected a missing evaluator content_hash to be rejected")
	}
}

func TestEvolverInputRejectsEscapingArtifactDir(t *testing.T) {
	input := validInput()
	input.Output.ArtifactDir = "../elsewhere"
	if err := input.Validate(); err == nil {
		t.Fatal("expected an artifact dir outside the workdir to be rejected")
	}
}

func TestEvolverInputRejectsUnsupportedMode(t *testing.T) {
	input := validInput()
	input.Mode = "harness_freeform"
	if err := input.Validate(); err == nil {
		t.Fatal("expected an unknown mode to be rejected")
	}
}

func TestEvolverInputRejectsUnboundedBatch(t *testing.T) {
	input := validInput()
	input.Budget.BatchTimeoutSeconds = 0
	if err := input.Validate(); err == nil {
		t.Fatal("expected a batch without a timeout to be rejected")
	}
}

func TestIsRelativeContainedPath(t *testing.T) {
	for _, allowed := range []string{"artifacts", "artifacts/c1.md", "eval/request.json"} {
		if !IsRelativeContainedPath(allowed) {
			t.Fatalf("expected %q to be allowed", allowed)
		}
	}
	for _, rejected := range []string{"", ".", "..", "../x", "/abs", `windows\path`, "D:/data"} {
		if IsRelativeContainedPath(rejected) {
			t.Fatalf("expected %q to be rejected", rejected)
		}
	}
}
