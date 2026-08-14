package researchrun

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const SourceIngestionPolicyVersionV1 = "research-source-ingestion-v1"

type SourceIngestionKind string

const (
	SourceIngestionScreenedRetrieval SourceIngestionKind = "screened_retrieval"
	SourceIngestionAgentDirect       SourceIngestionKind = "agent_direct_evidence"
	SourceIngestionUserAttachment    SourceIngestionKind = "user_attachment"
	SourceIngestionWorkspaceArtifact SourceIngestionKind = "workspace_artifact"
	SourceIngestionAPIDataset        SourceIngestionKind = "api_dataset"
)

type SourceIngestionIntent struct {
	PolicyVersion                string
	Kind                         SourceIngestionKind
	WorkspaceID                  string
	SessionID                    string
	SourceSnapshotID             string
	ContentHash                  string
	CapturedAt                   time.Time
	Locator                      string
	Reason                       string
	CanonicalURL                 string
	TaskID                       string
	AttemptID                    string
	AgentID                      string
	UserID                       string
	SearchPlanID                 string
	QueryExecutionID             string
	SourceCandidateID            string
	ScreeningDecisionID          string
	ScreeningDecisionFingerprint string
	ScreeningDisposition         string
	AttachmentID                 string
	WorkspaceArtifactID          string
	Adapter                      string
	DatasetID                    string
}

type ValidatedSourceIngestion struct {
	Intent      SourceIngestionIntent
	Fingerprint string
}

// ValidateSourceIngestionIntent proves how a Source Snapshot entered the
// Corpus. Retrieval evidence must bind an accepted Screening Decision; every
// non-retrieval kind is forbidden from carrying fabricated Search lineage.
func ValidateSourceIngestionIntent(intent SourceIngestionIntent) (ValidatedSourceIngestion, error) {
	if intent.PolicyVersion != SourceIngestionPolicyVersionV1 ||
		!validSourceIngestionToken(intent.WorkspaceID, 512) || !validSourceIngestionToken(intent.SessionID, 512) ||
		!validSourceIngestionToken(intent.SourceSnapshotID, 512) || !validSourceIngestionHash(intent.ContentHash) || intent.CapturedAt.IsZero() ||
		intent.CapturedAt.After(time.Now().UTC().Add(10*time.Minute)) ||
		!validSourceIngestionToken(intent.Locator, 2048) || strings.TrimSpace(intent.Reason) != intent.Reason || substantiveRuneCount(intent.Reason) < 8 || len(intent.Reason) > 4096 {
		return ValidatedSourceIngestion{}, fmt.Errorf("%w: Source Ingestion identity or audit facts are invalid", ErrInvalidContract)
	}
	normalized := intent
	normalized.CapturedAt = intent.CapturedAt.UTC()
	if intent.CanonicalURL != "" {
		parsed, err := url.Parse(intent.CanonicalURL)
		canonical, canonicalErr := CanonicalURL(intent.CanonicalURL)
		if err != nil || canonicalErr != nil || parsed.User != nil || canonical != intent.CanonicalURL {
			return ValidatedSourceIngestion{}, fmt.Errorf("%w: Source Ingestion URL is not canonical or contains credentials", ErrInvalidContract)
		}
	}
	switch intent.Kind {
	case SourceIngestionScreenedRetrieval:
		if !sourceIngestionTaskActor(intent) || !sourceIngestionSearchLineage(intent) || intent.ScreeningDisposition != "accepted" || intent.CanonicalURL == "" || sourceIngestionHasDirectOrigin(intent) {
			return ValidatedSourceIngestion{}, invalidSourceIngestionKind()
		}
	case SourceIngestionAgentDirect:
		if !sourceIngestionTaskActor(intent) || sourceIngestionHasSearchLineage(intent) || sourceIngestionHasDirectOrigin(intent) || intent.UserID != "" {
			return ValidatedSourceIngestion{}, invalidSourceIngestionKind()
		}
	case SourceIngestionUserAttachment:
		if !validSourceIngestionToken(intent.UserID, 512) || !validSourceIngestionToken(intent.AttachmentID, 512) ||
			sourceIngestionHasTaskActor(intent) || sourceIngestionHasSearchLineage(intent) || intent.WorkspaceArtifactID != "" || intent.Adapter != "" || intent.DatasetID != "" || intent.CanonicalURL != "" {
			return ValidatedSourceIngestion{}, invalidSourceIngestionKind()
		}
	case SourceIngestionWorkspaceArtifact:
		if !sourceIngestionTaskActor(intent) || !validSourceIngestionToken(intent.WorkspaceArtifactID, 512) ||
			sourceIngestionHasSearchLineage(intent) || intent.UserID != "" || intent.AttachmentID != "" || intent.Adapter != "" || intent.DatasetID != "" || intent.CanonicalURL != "" {
			return ValidatedSourceIngestion{}, invalidSourceIngestionKind()
		}
	case SourceIngestionAPIDataset:
		if !sourceIngestionTaskActor(intent) || !validSourceIngestionToken(intent.Adapter, 160) || !validSourceIngestionToken(intent.DatasetID, 512) ||
			sourceIngestionHasSearchLineage(intent) || intent.UserID != "" || intent.AttachmentID != "" || intent.WorkspaceArtifactID != "" {
			return ValidatedSourceIngestion{}, invalidSourceIngestionKind()
		}
	default:
		return ValidatedSourceIngestion{}, invalidSourceIngestionKind()
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return ValidatedSourceIngestion{}, err
	}
	digest := sha256.Sum256(encoded)
	return ValidatedSourceIngestion{Intent: normalized, Fingerprint: fmt.Sprintf("sha256:%x", digest)}, nil
}

func sourceIngestionTaskActor(intent SourceIngestionIntent) bool {
	return validSourceIngestionToken(intent.TaskID, 512) && validSourceIngestionToken(intent.AttemptID, 512) && validSourceIngestionToken(intent.AgentID, 512)
}

func sourceIngestionHasTaskActor(intent SourceIngestionIntent) bool {
	return intent.TaskID != "" || intent.AttemptID != "" || intent.AgentID != ""
}

func sourceIngestionSearchLineage(intent SourceIngestionIntent) bool {
	return validSourceIngestionToken(intent.SearchPlanID, 512) && validSourceIngestionToken(intent.QueryExecutionID, 512) &&
		validSourceIngestionToken(intent.SourceCandidateID, 512) && validSourceIngestionToken(intent.ScreeningDecisionID, 512) &&
		validSourceIngestionHash(intent.ScreeningDecisionFingerprint)
}

func sourceIngestionHasSearchLineage(intent SourceIngestionIntent) bool {
	return intent.SearchPlanID != "" || intent.QueryExecutionID != "" || intent.SourceCandidateID != "" ||
		intent.ScreeningDecisionID != "" || intent.ScreeningDecisionFingerprint != "" || intent.ScreeningDisposition != ""
}

func sourceIngestionHasDirectOrigin(intent SourceIngestionIntent) bool {
	return intent.UserID != "" || intent.AttachmentID != "" || intent.WorkspaceArtifactID != "" || intent.Adapter != "" || intent.DatasetID != ""
}

func invalidSourceIngestionKind() error {
	return fmt.Errorf("%w: Source Ingestion kind does not match its lineage", ErrInvalidContract)
}

func validSourceIngestionToken(value string, limit int) bool {
	return value != "" && len(value) <= limit && strings.TrimSpace(value) == value
}

func validSourceIngestionHash(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	for _, r := range strings.TrimPrefix(value, "sha256:") {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
