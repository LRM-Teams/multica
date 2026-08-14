package researchrun

import (
	"errors"
	"testing"
	"time"
)

const sourceIngestionHash = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"

func sourceIngestionFixture() SourceIngestionIntent {
	return SourceIngestionIntent{
		PolicyVersion: SourceIngestionPolicyVersionV1, Kind: SourceIngestionScreenedRetrieval,
		WorkspaceID: "workspace-1", SessionID: "session-1", SourceSnapshotID: "source-1",
		ContentHash: sourceIngestionHash, CapturedAt: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		Locator: "search-result:4", Reason: "The accepted candidate was captured as immutable evidence.",
		CanonicalURL: "https://example.com/source", TaskID: "task-1", AttemptID: "attempt-1", AgentID: "agent-1",
		SearchPlanID: "search-plan-1", QueryExecutionID: "query-1", SourceCandidateID: "candidate-1",
		ScreeningDecisionID: "screening-1", ScreeningDecisionFingerprint: sourceIngestionHash, ScreeningDisposition: "accepted",
	}
}

func TestValidateSourceIngestionIntentAcceptsExplicitKinds(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*SourceIngestionIntent)
		want   SourceIngestionKind
	}{
		"screened retrieval": {func(*SourceIngestionIntent) {}, SourceIngestionScreenedRetrieval},
		"agent direct": {func(intent *SourceIngestionIntent) {
			intent.Kind = SourceIngestionAgentDirect
			clearSourceSearchLineage(intent)
		}, SourceIngestionAgentDirect},
		"user attachment": {func(intent *SourceIngestionIntent) {
			intent.Kind, intent.UserID, intent.AttachmentID = SourceIngestionUserAttachment, "user-1", "attachment-1"
			clearSourceSearchLineage(intent)
			intent.TaskID, intent.AttemptID, intent.AgentID, intent.CanonicalURL = "", "", "", ""
		}, SourceIngestionUserAttachment},
		"workspace artifact": {func(intent *SourceIngestionIntent) {
			intent.Kind, intent.WorkspaceArtifactID = SourceIngestionWorkspaceArtifact, "artifact-1"
			clearSourceSearchLineage(intent)
			intent.CanonicalURL = ""
		}, SourceIngestionWorkspaceArtifact},
		"api dataset": {func(intent *SourceIngestionIntent) {
			intent.Kind, intent.Adapter, intent.DatasetID = SourceIngestionAPIDataset, "registry-v1", "dataset-1"
			clearSourceSearchLineage(intent)
		}, SourceIngestionAPIDataset},
	} {
		t.Run(name, func(t *testing.T) {
			intent := sourceIngestionFixture()
			tc.mutate(&intent)
			validated, err := ValidateSourceIngestionIntent(intent)
			if err != nil {
				t.Fatal(err)
			}
			if validated.Intent.Kind != tc.want || len(validated.Fingerprint) != 71 {
				t.Fatalf("validated=%+v", validated)
			}
		})
	}
}

func TestValidateSourceIngestionIntentNormalizesCaptureInstant(t *testing.T) {
	intent := sourceIngestionFixture()
	first, err := ValidateSourceIngestionIntent(intent)
	if err != nil {
		t.Fatal(err)
	}
	intent.CapturedAt = intent.CapturedAt.In(time.FixedZone("local", 8*60*60))
	second, err := ValidateSourceIngestionIntent(intent)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint != second.Fingerprint || second.Intent.CapturedAt.Location() != time.UTC {
		t.Fatalf("unstable capture instant: first=%+v second=%+v", first, second)
	}
}

func TestValidateSourceIngestionIntentRejectsMixedOrFabricatedLineage(t *testing.T) {
	for name, mutate := range map[string]func(*SourceIngestionIntent){
		"retrieval not accepted":     func(intent *SourceIngestionIntent) { intent.ScreeningDisposition = "excluded" },
		"retrieval missing decision": func(intent *SourceIngestionIntent) { intent.ScreeningDecisionID = "" },
		"direct fakes search":        func(intent *SourceIngestionIntent) { intent.Kind = SourceIngestionAgentDirect },
		"attachment carries agent": func(intent *SourceIngestionIntent) {
			intent.Kind, intent.UserID, intent.AttachmentID = SourceIngestionUserAttachment, "user-1", "attachment-1"
			clearSourceSearchLineage(intent)
			intent.CanonicalURL = ""
		},
		"workspace carries URL": func(intent *SourceIngestionIntent) {
			intent.Kind, intent.WorkspaceArtifactID = SourceIngestionWorkspaceArtifact, "artifact-1"
			clearSourceSearchLineage(intent)
		},
		"dataset fakes screening": func(intent *SourceIngestionIntent) {
			intent.Kind, intent.Adapter, intent.DatasetID = SourceIngestionAPIDataset, "api-v1", "dataset-1"
		},
		"credential URL": func(intent *SourceIngestionIntent) { intent.CanonicalURL = "https://user:secret@example.com/source" },
		"future capture": func(intent *SourceIngestionIntent) { intent.CapturedAt = time.Now().UTC().Add(time.Hour) },
		"unknown kind":   func(intent *SourceIngestionIntent) { intent.Kind = "future" },
		"weak reason":    func(intent *SourceIngestionIntent) { intent.Reason = "because" },
	} {
		t.Run(name, func(t *testing.T) {
			intent := sourceIngestionFixture()
			mutate(&intent)
			if _, err := ValidateSourceIngestionIntent(intent); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("err=%v want ErrInvalidContract", err)
			}
		})
	}
}

func clearSourceSearchLineage(intent *SourceIngestionIntent) {
	intent.SearchPlanID, intent.QueryExecutionID, intent.SourceCandidateID = "", "", ""
	intent.ScreeningDecisionID, intent.ScreeningDecisionFingerprint, intent.ScreeningDisposition = "", "", ""
}
