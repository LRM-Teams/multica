package researcheval

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCorpusV1CoversModesAndAdversarialConditions(t *testing.T) {
	corpus := loadTestCorpus(t)
	modes := map[ResearchMode]bool{}
	tags := map[string]bool{}
	for _, evaluationCase := range corpus.Cases {
		modes[evaluationCase.Task.Mode] = true
		for _, tag := range evaluationCase.Task.Tags {
			tags[tag] = true
		}
		encoded, err := json.Marshal(evaluationCase.SubjectInput())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), `"oracle"`) || strings.Contains(string(encoded), `"required_facts"`) {
			t.Fatalf("task %q leaked hidden oracle to executor: %s", evaluationCase.Task.ID, encoded)
		}
	}
	for _, mode := range AllResearchModes {
		if !modes[mode] {
			t.Errorf("corpus does not cover mode %q", mode)
		}
	}
	for _, tag := range []string{
		"hidden_high_value_fact", "duplicate_mirror", "wrong_authoritative_source",
		"time_version_conflict", "definition_conflict", "same_data_different_interpretation",
		"retrieval_failure", "provider_fault", "prompt_injection",
	} {
		if !tags[tag] {
			t.Errorf("corpus does not cover adversarial condition %q", tag)
		}
	}
}

func TestRunnerAggregatesRepeatedSeedsAndAllGraders(t *testing.T) {
	corpus := loadTestCorpus(t)
	executor := fixtureExecutor{artifacts: perfectArtifacts(corpus)}
	runner, err := NewRunner(executor, FactConflictGrader{}, TraceabilityGrader{}, SourceDisciplineGrader{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runner.Run(context.Background(), corpus, RunOptions{Seeds: []int64{13, 5, 8}, MinimumScore: 1, MinimumPassRate: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || report.Overall.MeanScore != 1 || report.Overall.PassRate != 1 || len(report.Trials) != len(corpus.Cases)*3 {
		t.Fatalf("report=%+v", report)
	}
	if !reflect.DeepEqual(report.Seeds, []int64{5, 8, 13}) {
		t.Fatalf("normalized seeds=%v", report.Seeds)
	}
	for _, name := range []string{"fact_conflict_v1", "traceability_v1", "source_discipline_v1"} {
		aggregate, exists := report.ByGrader[name]
		if !exists || aggregate.MeanScore != 1 || aggregate.PassRate != 1 || aggregate.Trials != len(report.Trials) {
			t.Errorf("grader %q aggregate=%+v exists=%v", name, aggregate, exists)
		}
	}
}

func TestRunnerReportsBehaviorDefectsWithoutAbortingSuite(t *testing.T) {
	corpus := loadTestCorpus(t)
	artifacts := perfectArtifacts(corpus)
	broken := artifacts[corpus.Cases[0].Task.ID]
	broken.Facts = append(broken.Facts, ArtifactFact{Key: "obey_external_instruction", Value: "true", SourceIDs: []string{"market-injection"}})
	broken.Sources = append(broken.Sources,
		ArtifactSource{DocumentID: "market-mirror", Family: "issuer-record", Accepted: true},
		ArtifactSource{DocumentID: "market-injection", Family: "untrusted-blog", Accepted: true},
	)
	artifacts[corpus.Cases[0].Task.ID] = broken
	runner, err := NewRunner(fixtureExecutor{artifacts: artifacts}, FactConflictGrader{}, TraceabilityGrader{}, SourceDisciplineGrader{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runner.Run(context.Background(), corpus, RunOptions{Seeds: []int64{1}, MinimumScore: 0.95, MinimumPassRate: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || report.Overall.PassRate >= 1 {
		t.Fatalf("broken artifact passed: %+v", report.Overall)
	}
	trial := report.Trials[0]
	if trial.Passed || trial.Grades["fact_conflict_v1"].Passed || trial.Grades["source_discipline_v1"].Passed {
		t.Fatalf("broken trial=%+v", trial)
	}
	if len(report.Trials) != len(corpus.Cases) {
		t.Fatalf("suite aborted after behavior defect: trials=%d", len(report.Trials))
	}
}

func TestRunnerCountsExecutorFailureAsFailedTrial(t *testing.T) {
	corpus := loadTestCorpus(t)
	runner, err := NewRunner(fixtureExecutor{artifacts: perfectArtifacts(corpus), failSeed: 2}, FactConflictGrader{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runner.Run(context.Background(), corpus, RunOptions{Seeds: []int64{1, 2}, MinimumScore: 0.5, MinimumPassRate: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || report.Overall.PassRate != 0.5 || report.Overall.MeanScore != 0.5 {
		t.Fatalf("executor failures were not counted: %+v", report.Overall)
	}
	failures := 0
	for _, trial := range report.Trials {
		if trial.ExecutionError != "" {
			failures++
		}
	}
	if failures != len(corpus.Cases) {
		t.Fatalf("execution failures=%d want=%d", failures, len(corpus.Cases))
	}
}

func TestCompareReportsRejectsMissingOrRegressedGraders(t *testing.T) {
	baseline := Report{CorpusVersion: "v1", Passed: true, Overall: Aggregate{MeanScore: 0.9, PassRate: 0.8}, ByGrader: map[string]Aggregate{
		"accuracy": {MeanScore: 0.9, PassRate: 0.8}, "trace": {MeanScore: 0.8, PassRate: 0.8},
	}}
	candidate := Report{CorpusVersion: "v1", Passed: true, Overall: Aggregate{MeanScore: 0.95, PassRate: 0.9}, ByGrader: map[string]Aggregate{
		"accuracy": {MeanScore: 0.95, PassRate: 0.9},
	}}
	comparison := CompareReports(baseline, candidate)
	if comparison.NonRegressing || !reflect.DeepEqual(comparison.MissingGraders, []string{"trace"}) {
		t.Fatalf("comparison=%+v", comparison)
	}
	candidate.ByGrader["trace"] = Aggregate{MeanScore: 0.85, PassRate: 0.9}
	comparison = CompareReports(baseline, candidate)
	if !comparison.NonRegressing || comparison.GraderScoreDelta["trace"] <= 0 {
		t.Fatalf("non-regressing comparison=%+v", comparison)
	}
	candidate.CorpusVersion = "v2"
	if comparison = CompareReports(baseline, candidate); comparison.NonRegressing || !reflect.DeepEqual(comparison.IncomparableReasons, []string{"corpus_version"}) {
		t.Fatalf("different corpus versions were treated as comparable: %+v", comparison)
	}
}

func TestValidationRejectsConflictingFixtureAndArtifactIdentity(t *testing.T) {
	corpus := loadTestCorpus(t)
	invalid := corpus
	invalid.Cases[0].Oracle.RequiredFacts[0].RequiredSourceIDs = []string{"missing"}
	if err := ValidateCorpus(invalid); !errors.Is(err, ErrInvalidCorpus) {
		t.Fatalf("corpus error=%v", err)
	}
	corpus = loadTestCorpus(t)
	impossible := corpus
	impossible.Cases = append([]Case(nil), corpus.Cases...)
	impossible.Cases[0].Oracle.ForbiddenDocumentIDs = []string{"market-primary"}
	if err := ValidateCorpus(impossible); !errors.Is(err, ErrInvalidCorpus) || !strings.Contains(err.Error(), "both required and forbidden") {
		t.Fatalf("inconsistent oracle error=%v", err)
	}
	artifact := perfectArtifacts(corpus)[corpus.Cases[0].Task.ID]
	artifact.Sources[0].Family = "rewritten-family"
	if err := ValidateArtifact(corpus.Cases[0], artifact); !errors.Is(err, ErrInvalidEvaluation) {
		t.Fatalf("artifact error=%v", err)
	}
}

type fixtureExecutor struct {
	artifacts map[string]Artifact
	failSeed  int64
}

func (executor fixtureExecutor) Execute(_ context.Context, input SubjectInput, seed int64) (Artifact, error) {
	if executor.failSeed != 0 && seed == executor.failSeed {
		return Artifact{}, errors.New("injected provider failure")
	}
	artifact, exists := executor.artifacts[input.Task.ID]
	if !exists {
		return Artifact{}, errors.New("missing fixture artifact")
	}
	return artifact, nil
}

func perfectArtifacts(corpus Corpus) map[string]Artifact {
	out := make(map[string]Artifact, len(corpus.Cases))
	for _, evaluationCase := range corpus.Cases {
		artifact := Artifact{ReportMD: "Fixture report with claim-level provenance."}
		sourceIDs := []string{}
		factKeys := []string{}
		documentByID := map[string]Document{}
		for _, document := range evaluationCase.Environment.Documents {
			documentByID[document.ID] = document
		}
		accepted := map[string]bool{}
		for _, expected := range evaluationCase.Oracle.RequiredFacts {
			artifact.Facts = append(artifact.Facts, ArtifactFact{Key: expected.Key, Value: expected.Value, SourceIDs: expected.RequiredSourceIDs})
			factKeys = append(factKeys, expected.Key)
			for _, sourceID := range expected.RequiredSourceIDs {
				if accepted[sourceID] {
					continue
				}
				document := documentByID[sourceID]
				artifact.Sources = append(artifact.Sources, ArtifactSource{DocumentID: sourceID, Family: document.Family, Accepted: true})
				accepted[sourceID] = true
				sourceIDs = append(sourceIDs, sourceID)
			}
		}
		for _, expected := range evaluationCase.Oracle.RequiredConflicts {
			artifact.Conflicts = append(artifact.Conflicts, ArtifactConflict{Key: expected.Key, Type: expected.Type, FactKeys: factKeys, Resolved: true})
		}
		for _, expected := range evaluationCase.Oracle.RequiredReportClaims {
			artifact.Claims = append(artifact.Claims, ArtifactClaim{Key: expected.Key, FactKeys: expected.RequiredFactKeys, SourceIDs: sourceIDs, InReport: true})
		}
		out[evaluationCase.Task.ID] = artifact
	}
	return out
}

func loadTestCorpus(t *testing.T) Corpus {
	t.Helper()
	corpus, err := LoadCorpus("testdata/corpus_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	return corpus
}
