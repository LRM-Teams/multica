package researcheval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestAutonomyCorpusV1FreezesSevenBehaviorContracts(t *testing.T) {
	corpus := loadAutonomyCorpus(t)
	wantIDs := []string{
		"autonomy-team-formation",
		"autonomy-conflict-deliberation",
		"autonomy-projection-rebuild",
		"autonomy-recursive-insight",
		"autonomy-divergence",
		"autonomy-assimilation",
		"autonomy-infinite-canvas",
	}
	gotIDs := make([]string, 0, len(corpus.Cases))
	for _, evaluationCase := range corpus.Cases {
		gotIDs = append(gotIDs, evaluationCase.Task.ID)
		if evaluationCase.Oracle.Autonomy == nil {
			t.Fatalf("case %q has no autonomy oracle", evaluationCase.Task.ID)
		}
		if len(evaluationCase.Oracle.Autonomy.RequiredActions) == 0 || len(evaluationCase.Oracle.Autonomy.ForbiddenActions) == 0 {
			t.Fatalf("case %q does not specify both required and forbidden behavior", evaluationCase.Task.ID)
		}
		if len(evaluationCase.Oracle.Autonomy.RequiredNodes) == 0 || len(evaluationCase.Oracle.Autonomy.RequiredEdges) == 0 {
			t.Fatalf("case %q does not specify observable graph artifacts", evaluationCase.Task.ID)
		}
		encoded, err := json.Marshal(evaluationCase.SubjectInput())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), `"oracle"`) || strings.Contains(string(encoded), `"required_actions"`) {
			t.Fatalf("case %q leaked hidden autonomy oracle to executor: %s", evaluationCase.Task.ID, encoded)
		}
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("case IDs=%v want=%v", gotIDs, wantIDs)
	}
	projectionCase := findCase(t, corpus, "autonomy-projection-rebuild")
	gotKinds := make([]string, 0, len(projectionCase.Oracle.Autonomy.RequiredNodes))
	for _, node := range projectionCase.Oracle.Autonomy.RequiredNodes {
		gotKinds = append(gotKinds, node.Kind)
	}
	if !reflect.DeepEqual(gotKinds, RequiredProjectionNodeKinds) {
		t.Fatalf("projection kinds=%v want=%v", gotKinds, RequiredProjectionNodeKinds)
	}
}

func TestAutonomyGraderAcceptsCompleteArtifactsAcrossSeeds(t *testing.T) {
	corpus := loadAutonomyCorpus(t)
	runner, err := NewRunner(fixtureExecutor{artifacts: perfectAutonomyArtifacts(corpus)}, AutonomyGrader{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runner.Run(context.Background(), corpus, RunOptions{
		Seeds: []int64{29, 11, 17}, MinimumScore: 1, MinimumPassRate: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || report.Overall.MeanScore != 1 || report.Overall.PassRate != 1 {
		t.Fatalf("complete autonomy fixtures did not pass: %+v", report.Overall)
	}
	wantTrials := len(corpus.Cases) * 3
	if len(report.Trials) != wantTrials || report.ByGrader[AutonomyGrader{}.Name()].Trials != wantTrials {
		t.Fatalf("trial counts report=%d grader=%+v want=%d", len(report.Trials), report.ByGrader[AutonomyGrader{}.Name()], wantTrials)
	}
}

func TestAutonomyGraderDetectsBehaviorAndProjectionRegressions(t *testing.T) {
	tests := []struct {
		name        string
		caseID      string
		mutate      func(*Artifact, Case)
		findingCode string
	}{
		{
			name:   "forbidden non-director authorization",
			caseID: "autonomy-team-formation",
			mutate: func(artifact *Artifact, evaluationCase Case) {
				artifact.Actions = append(artifact.Actions, artifactAction(evaluationCase.Oracle.Autonomy.ForbiddenActions[0]))
			},
			findingCode: "forbidden_action_observed",
		},
		{
			name:   "missing recursive derivation edge",
			caseID: "autonomy-recursive-insight",
			mutate: func(artifact *Artifact, _ Case) {
				artifact.GraphEdges = artifact.GraphEdges[1:]
			},
			findingCode: "required_graph_edge_missing",
		},
		{
			name:   "incomplete projection node detail",
			caseID: "autonomy-projection-rebuild",
			mutate: func(artifact *Artifact, _ Case) {
				delete(artifact.GraphNodes[0].Details, "objective")
			},
			findingCode: "required_graph_node_missing",
		},
		{
			name:   "duplicate node after delta replay",
			caseID: "autonomy-infinite-canvas",
			mutate: func(artifact *Artifact, _ Case) {
				artifact.Projection.ObservedNodeIDs[1] = artifact.Projection.ObservedNodeIDs[0]
			},
			findingCode: "projection_duplicate_node",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			corpus := loadAutonomyCorpus(t)
			evaluationCase := findCase(t, corpus, test.caseID)
			artifact := perfectAutonomyArtifact(evaluationCase)
			test.mutate(&artifact, evaluationCase)
			if err := ValidateArtifact(evaluationCase, artifact); err != nil {
				t.Fatalf("mutated artifact should remain structurally valid: %v", err)
			}
			grade, err := (AutonomyGrader{}).Grade(context.Background(), evaluationCase, artifact)
			if err != nil {
				t.Fatal(err)
			}
			if grade.Passed || !hasFinding(grade.Findings, test.findingCode) {
				t.Fatalf("grade=%+v want finding=%q", grade, test.findingCode)
			}
		})
	}
}

func TestAutonomyValidationRejectsImpossibleOrMalformedContracts(t *testing.T) {
	t.Run("edge references missing node", func(t *testing.T) {
		corpus := loadAutonomyCorpus(t)
		corpus.Cases[0].Oracle.Autonomy.RequiredEdges[0].ToKey = "missing"
		err := ValidateCorpus(corpus)
		if !errors.Is(err, ErrInvalidCorpus) || !strings.Contains(err.Error(), "unknown to node") {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("action is required and forbidden", func(t *testing.T) {
		corpus := loadAutonomyCorpus(t)
		action := corpus.Cases[0].Oracle.Autonomy.RequiredActions[0]
		corpus.Cases[0].Oracle.Autonomy.ForbiddenActions = append(corpus.Cases[0].Oracle.Autonomy.ForbiddenActions, action)
		err := ValidateCorpus(corpus)
		if !errors.Is(err, ErrInvalidCorpus) || !strings.Contains(err.Error(), "both required and forbidden") {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("large graph has no bounded page", func(t *testing.T) {
		corpus := loadAutonomyCorpus(t)
		evaluationCase := findCase(t, corpus, "autonomy-infinite-canvas")
		evaluationCase.Oracle.Autonomy.Projection.MaximumPageNodes = 0
		for index := range corpus.Cases {
			if corpus.Cases[index].Task.ID == evaluationCase.Task.ID {
				corpus.Cases[index] = evaluationCase
			}
		}
		err := ValidateCorpus(corpus)
		if !errors.Is(err, ErrInvalidCorpus) || !strings.Contains(err.Error(), "page limit") {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("edge points outside artifact graph", func(t *testing.T) {
		corpus := loadAutonomyCorpus(t)
		evaluationCase := corpus.Cases[0]
		artifact := perfectAutonomyArtifact(evaluationCase)
		artifact.GraphEdges[0].ToNodeID = "missing"
		err := ValidateArtifact(evaluationCase, artifact)
		if !errors.Is(err, ErrInvalidEvaluation) || !strings.Contains(err.Error(), "unknown to node") {
			t.Fatalf("error=%v", err)
		}
	})
}

func perfectAutonomyArtifacts(corpus Corpus) map[string]Artifact {
	artifacts := make(map[string]Artifact, len(corpus.Cases))
	for _, evaluationCase := range corpus.Cases {
		artifacts[evaluationCase.Task.ID] = perfectAutonomyArtifact(evaluationCase)
	}
	return artifacts
}

func perfectAutonomyArtifact(evaluationCase Case) Artifact {
	oracle := evaluationCase.Oracle.Autonomy
	artifact := Artifact{ReportMD: "Controlled autonomy evaluation artifact."}
	for _, action := range oracle.RequiredActions {
		artifact.Actions = append(artifact.Actions, artifactAction(action))
	}
	nodeIDs := make(map[string]string, len(oracle.RequiredNodes))
	for index, node := range oracle.RequiredNodes {
		id := fmt.Sprintf("node-%03d", index)
		nodeIDs[node.Key] = id
		details := make(map[string]string, len(RequiredProjectionDetailFields))
		for _, field := range RequiredProjectionDetailFields {
			details[field] = "fixture-" + field
		}
		artifact.GraphNodes = append(artifact.GraphNodes, ArtifactGraphNode{
			ID: id, Key: node.Key, Kind: node.Kind, Status: node.Status, Level: node.Level, Details: details,
		})
	}
	for index, edge := range oracle.RequiredEdges {
		artifact.GraphEdges = append(artifact.GraphEdges, ArtifactGraphEdge{
			ID: fmt.Sprintf("edge-%03d", index), FromNodeID: nodeIDs[edge.FromKey], ToNodeID: nodeIDs[edge.ToKey], Type: edge.Type,
		})
	}
	if expected := oracle.Projection; expected != nil {
		totalNodes := expected.MinimumTotalNodes
		if totalNodes < len(artifact.GraphNodes) {
			totalNodes = len(artifact.GraphNodes)
		}
		observed := make([]string, totalNodes)
		for index := range observed {
			observed[index] = fmt.Sprintf("projection-node-%05d", index)
		}
		largestPage := expected.MaximumPageNodes
		if largestPage > totalNodes {
			largestPage = totalNodes
		}
		artifact.Projection = &ArtifactProjection{
			SnapshotHash: "fixture-hash", ReplayHash: "fixture-hash", ObservedNodeIDs: observed,
			TotalNodes: totalNodes, LargestPageNodes: largestPage,
			GapDetected: expected.RequireGapResync, ResyncRequested: expected.RequireGapResync,
		}
	}
	return artifact
}

func artifactAction(expected ExpectedAction) ArtifactAction {
	return ArtifactAction{Kind: expected.Kind, Actor: expected.Actor, Target: expected.Target, Outcome: expected.Outcome}
}

func findCase(t *testing.T, corpus Corpus, id string) Case {
	t.Helper()
	for _, evaluationCase := range corpus.Cases {
		if evaluationCase.Task.ID == id {
			return evaluationCase
		}
	}
	t.Fatalf("case %q not found", id)
	return Case{}
}

func hasFinding(findings []Finding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func loadAutonomyCorpus(t *testing.T) Corpus {
	t.Helper()
	corpus, err := LoadCorpus("testdata/autonomy_corpus_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	return corpus
}
