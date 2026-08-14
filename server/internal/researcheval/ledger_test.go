package researcheval

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestRunnerSealsSuccessfulArtifactHash(t *testing.T) {
	corpus := loadTestCorpus(t)
	runner, err := NewRunner(fixtureExecutor{artifacts: perfectArtifacts(corpus)}, FactConflictGrader{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runner.Run(context.Background(), corpus, RunOptions{Seeds: []int64{7}, MinimumScore: 1, MinimumPassRate: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, trial := range report.Trials {
		if trial.Artifact == nil || !validEvaluationHash(trial.ArtifactHash) {
			t.Fatalf("trial %s has invalid artifact hash %q", trial.TaskID, trial.ArtifactHash)
		}
	}
}

func TestCanonicalEvaluationInputs(t *testing.T) {
	seeds, err := canonicalEvaluationSeeds([]int64{9, 2, 5})
	if err != nil || !reflect.DeepEqual(seeds, []int64{2, 5, 9}) {
		t.Fatalf("seeds=%v err=%v", seeds, err)
	}
	if _, err = canonicalEvaluationSeeds([]int64{2, 2}); !errors.Is(err, ErrInvalidEvaluation) {
		t.Fatalf("duplicate seed error=%v", err)
	}
	left, err := canonicalEvaluationObject(json.RawMessage(`{"provider":"fixture","limits":{"b":2,"a":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	right, err := canonicalEvaluationObject(json.RawMessage(`{"limits":{"a":1,"b":2},"provider":"fixture"}`))
	if err != nil || string(left) != string(right) {
		t.Fatalf("canonical objects differ: %s != %s (err=%v)", left, right, err)
	}
	if _, err = canonicalEvaluationObject(json.RawMessage(`[]`)); err == nil {
		t.Fatal("array environment was accepted")
	}
}

func TestValidateDurableReportRejectsMutatedEvidence(t *testing.T) {
	report := validDurableReport(t)
	if err := validateDurableReport(report); err != nil {
		t.Fatalf("valid report: %v", err)
	}

	tests := map[string]func(*Report){
		"missing artifact hash": func(report *Report) { report.Trials[0].ArtifactHash = "" },
		"mutated artifact": func(report *Report) {
			report.Trials[0].Artifact.ReportMD = "mutated after grading"
		},
		"unknown seed":         func(report *Report) { report.Trials[0].Seed = 999 },
		"missing grader":       func(report *Report) { delete(report.Trials[0].Grades, "fact_conflict_v1") },
		"mutated trial score":  func(report *Report) { report.Trials[0].Score = 0.5 },
		"mutated aggregate":    func(report *Report) { report.Overall.MeanScore = 0.5 },
		"non-finite aggregate": func(report *Report) { report.Overall.PassRate = math.NaN() },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := cloneReport(t, report)
			mutate(&candidate)
			if err := validateDurableReport(candidate); !errors.Is(err, ErrInvalidEvaluation) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestValidateDurableComparisonBindsCandidateCorpus(t *testing.T) {
	report := validDurableReport(t)
	comparison := &Comparison{BaselineCorpusVersion: report.CorpusVersion, CandidateCorpusVersion: report.CorpusVersion}
	if err := validateDurableComparison(report, comparison); err != nil {
		t.Fatal(err)
	}
	comparison.CandidateCorpusVersion = "different"
	if err := validateDurableComparison(report, comparison); !errors.Is(err, ErrInvalidEvaluation) {
		t.Fatalf("error=%v", err)
	}
}

func TestValidateDurableReportKeepsExecutionFailuresInDenominator(t *testing.T) {
	corpus := loadTestCorpus(t)
	runner, err := NewRunner(
		fixtureExecutor{artifacts: perfectArtifacts(corpus), failSeed: 2},
		FactConflictGrader{},
	)
	if err != nil {
		t.Fatal(err)
	}
	report, err := runner.Run(context.Background(), corpus, RunOptions{
		Seeds: []int64{1, 2}, MinimumScore: 0.5, MinimumPassRate: 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = validateDurableReport(report); err != nil {
		t.Fatalf("report with execution failures: %v", err)
	}
	for _, trial := range report.Trials {
		if trial.ExecutionError != "" && (trial.Artifact != nil || trial.ArtifactHash != "" || len(trial.Grades) != 0) {
			t.Fatalf("failed trial retained success evidence: %+v", trial)
		}
	}
}

func validDurableReport(t *testing.T) Report {
	t.Helper()
	corpus := loadTestCorpus(t)
	runner, err := NewRunner(fixtureExecutor{artifacts: perfectArtifacts(corpus)}, FactConflictGrader{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runner.Run(context.Background(), corpus, RunOptions{Seeds: []int64{3}, MinimumScore: 1, MinimumPassRate: 1})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func cloneReport(t *testing.T, report Report) Report {
	t.Helper()
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var clone Report
	if err = json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}
