package researchrun

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const MonitoringDiffPolicyV1 = "research-monitoring-diff-v1"

type MonitoringQueryExecution struct {
	ExecutionID        string
	WorkspaceID        string
	SessionID          string
	MonitorID          string
	CycleID            string
	SearchPlanID       string
	SearchPlanVersion  int64
	QueryKey           string
	CanonicalQueryHash string
	Status             string
	StartedAt          time.Time
	CompletedAt        time.Time
	ResultArtifactIDs  []string
}

type MonitoringArtifactDiff struct {
	ArtifactID          string
	WorkspaceID         string
	SessionID           string
	QueryExecutionID    string
	PreviousVersionID   string
	PreviousContentHash string
	CurrentVersionID    string
	CurrentContentHash  string
	ChangeKind          string
	ContentSimilarity   float64
}

type MonitoringExpectedQuery struct {
	QueryKey           string
	CanonicalQueryHash string
}

type MonitoringDiffRequest struct {
	PolicyVersion     string
	WorkspaceID       string
	SessionID         string
	MonitorID         string
	CycleID           string
	SearchPlanID      string
	SearchPlanVersion int64
	ExpectedQueries   []MonitoringExpectedQuery
	Executions        []MonitoringQueryExecution
	ArtifactDiffs     []MonitoringArtifactDiff
}

type MonitoringDiffManifest struct {
	QueryExecutionIDs  []string
	ChangedArtifactIDs []string
	MaterialityScore   float64
	Fingerprint        string
}

// ValidateMonitoringDiff admits one complete execution of a pinned Search Plan
// and computes materiality from server-resolved version/hash differences. It
// never accepts an Agent-supplied aggregate change score.
func ValidateMonitoringDiff(request MonitoringDiffRequest) (MonitoringDiffManifest, error) {
	if !validMonitoringDiffRequest(request) {
		return MonitoringDiffManifest{}, fmt.Errorf("%w: Monitoring diff request is invalid", ErrInvalidContract)
	}
	expected, err := normalizeMonitoringExpectedQueries(request.ExpectedQueries)
	if err != nil {
		return MonitoringDiffManifest{}, fmt.Errorf("%w: expected monitoring queries are invalid", ErrInvalidContract)
	}
	executions := append([]MonitoringQueryExecution(nil), request.Executions...)
	sort.Slice(executions, func(i, j int) bool { return executions[i].QueryKey < executions[j].QueryKey })
	if len(executions) != len(expected) {
		return MonitoringDiffManifest{}, fmt.Errorf("%w: Monitoring Cycle did not execute the complete Search Plan", ErrInvalidContract)
	}
	executionByID := make(map[string]MonitoringQueryExecution, len(executions))
	resultOwner := make(map[string]string)
	executionIDs := make([]string, 0, len(executions))
	for index := range executions {
		execution, normalizeErr := normalizeMonitoringExecution(executions[index], request)
		if normalizeErr != nil {
			return MonitoringDiffManifest{}, normalizeErr
		}
		if execution.QueryKey != expected[index].QueryKey || execution.CanonicalQueryHash != expected[index].CanonicalQueryHash {
			return MonitoringDiffManifest{}, fmt.Errorf("%w: Monitoring Cycle query set differs from pinned Search Plan", ErrControlTargetChanged)
		}
		if _, duplicate := executionByID[execution.ExecutionID]; duplicate {
			return MonitoringDiffManifest{}, fmt.Errorf("%w: duplicate Query Execution", ErrInvalidContract)
		}
		executions[index] = execution
		executionByID[execution.ExecutionID] = execution
		executionIDs = append(executionIDs, execution.ExecutionID)
		for _, artifactID := range execution.ResultArtifactIDs {
			if _, duplicate := resultOwner[artifactID]; duplicate {
				return MonitoringDiffManifest{}, fmt.Errorf("%w: result artifact belongs to multiple Query Executions", ErrInvalidContract)
			}
			resultOwner[artifactID] = execution.ExecutionID
		}
	}

	diffs := append([]MonitoringArtifactDiff(nil), request.ArtifactDiffs...)
	sort.Slice(diffs, func(i, j int) bool { return diffs[i].ArtifactID < diffs[j].ArtifactID })
	if len(diffs) > 4096 {
		return MonitoringDiffManifest{}, fmt.Errorf("%w: Monitoring diff set exceeds limit", ErrInvalidContract)
	}
	seenDiffs := make(map[string]struct{}, len(diffs))
	changedIDs := make([]string, 0, len(diffs))
	var materialityTotal float64
	for _, diff := range diffs {
		if err := validateMonitoringArtifactDiff(diff, request, executionByID, resultOwner); err != nil {
			return MonitoringDiffManifest{}, err
		}
		if _, duplicate := seenDiffs[diff.ArtifactID]; duplicate {
			return MonitoringDiffManifest{}, fmt.Errorf("%w: duplicate Monitoring artifact diff", ErrInvalidContract)
		}
		seenDiffs[diff.ArtifactID] = struct{}{}
		if diff.ChangeKind != "unchanged" {
			changedIDs = append(changedIDs, diff.ArtifactID)
			if diff.ChangeKind == "modified" {
				materialityTotal += 1 - diff.ContentSimilarity
			} else {
				materialityTotal++
			}
		}
	}
	for artifactID := range resultOwner {
		if _, present := seenDiffs[artifactID]; !present {
			return MonitoringDiffManifest{}, fmt.Errorf("%w: Query Execution result lacks a version diff", ErrInvalidContract)
		}
	}
	materiality := 0.0
	if len(diffs) > 0 {
		materiality = materialityTotal / float64(len(diffs))
	}
	manifest := MonitoringDiffManifest{
		QueryExecutionIDs:  executionIDs,
		ChangedArtifactIDs: changedIDs,
		MaterialityScore:   materiality,
	}
	request.ExpectedQueries = expected
	request.Executions = executions
	request.ArtifactDiffs = diffs
	encoded, err := json.Marshal(request)
	if err != nil {
		return MonitoringDiffManifest{}, err
	}
	digest := sha256.Sum256(encoded)
	manifest.Fingerprint = fmt.Sprintf("sha256:%x", digest)
	return manifest, nil
}

func normalizeMonitoringExecution(in MonitoringQueryExecution, request MonitoringDiffRequest) (MonitoringQueryExecution, error) {
	if !validMonitoringUUID(in.ExecutionID) || in.WorkspaceID != request.WorkspaceID || in.SessionID != request.SessionID ||
		in.MonitorID != request.MonitorID || in.CycleID != request.CycleID || in.SearchPlanID != request.SearchPlanID ||
		in.SearchPlanVersion != request.SearchPlanVersion || strings.TrimSpace(in.QueryKey) == "" ||
		!validMonitoringHash(in.CanonicalQueryHash) || in.StartedAt.IsZero() || in.CompletedAt.Before(in.StartedAt) ||
		(in.Status != "succeeded" && in.Status != "no_results") {
		return MonitoringQueryExecution{}, fmt.Errorf("%w: Query Execution is invalid or outside the pinned Monitoring Cycle", ErrInvalidContract)
	}
	normalized := in
	var err error
	normalized.ResultArtifactIDs, err = normalizedMonitoringUUIDs(in.ResultArtifactIDs, 4096)
	if err != nil || in.Status == "no_results" && len(normalized.ResultArtifactIDs) != 0 {
		return MonitoringQueryExecution{}, fmt.Errorf("%w: Query Execution result artifacts are invalid", ErrInvalidContract)
	}
	return normalized, nil
}

func validateMonitoringArtifactDiff(diff MonitoringArtifactDiff, request MonitoringDiffRequest, executions map[string]MonitoringQueryExecution, resultOwner map[string]string) error {
	if !validMonitoringUUID(diff.ArtifactID) || diff.WorkspaceID != request.WorkspaceID || diff.SessionID != request.SessionID ||
		!validMonitoringUUID(diff.QueryExecutionID) || diff.ContentSimilarity < 0 || diff.ContentSimilarity > 1 {
		return fmt.Errorf("%w: Monitoring artifact diff is invalid", ErrInvalidContract)
	}
	if _, exists := executions[diff.QueryExecutionID]; !exists {
		return fmt.Errorf("%w: Monitoring artifact diff has no Cycle Query Execution", ErrInvalidContract)
	}
	owner, currentResult := resultOwner[diff.ArtifactID]
	if currentResult && owner != diff.QueryExecutionID {
		return fmt.Errorf("%w: Monitoring artifact diff is bound to the wrong Query Execution", ErrInvalidContract)
	}
	previousValid := validMonitoringUUID(diff.PreviousVersionID) && validMonitoringHash(diff.PreviousContentHash)
	currentValid := validMonitoringUUID(diff.CurrentVersionID) && validMonitoringHash(diff.CurrentContentHash)
	previousAbsent := diff.PreviousVersionID == "" && diff.PreviousContentHash == ""
	currentAbsent := diff.CurrentVersionID == "" && diff.CurrentContentHash == ""
	switch diff.ChangeKind {
	case "added":
		if !previousAbsent || !currentValid || !currentResult || diff.ContentSimilarity != 0 {
			return fmt.Errorf("%w: added Monitoring artifact diff is inconsistent", ErrInvalidContract)
		}
	case "removed":
		if !previousValid || !currentAbsent || currentResult || diff.ContentSimilarity != 0 {
			return fmt.Errorf("%w: removed Monitoring artifact diff is inconsistent", ErrInvalidContract)
		}
	case "modified":
		if !previousValid || !currentValid || !currentResult || diff.PreviousVersionID == diff.CurrentVersionID || diff.PreviousContentHash == diff.CurrentContentHash || diff.ContentSimilarity >= 1 {
			return fmt.Errorf("%w: modified Monitoring artifact diff is inconsistent", ErrInvalidContract)
		}
	case "unchanged":
		if !previousValid || !currentValid || !currentResult || diff.PreviousVersionID != diff.CurrentVersionID || diff.PreviousContentHash != diff.CurrentContentHash || diff.ContentSimilarity != 1 {
			return fmt.Errorf("%w: unchanged Monitoring artifact diff is inconsistent", ErrInvalidContract)
		}
	default:
		return fmt.Errorf("%w: unsupported Monitoring artifact change kind", ErrInvalidContract)
	}
	return nil
}

func validMonitoringDiffRequest(request MonitoringDiffRequest) bool {
	return request.PolicyVersion == MonitoringDiffPolicyV1 && validMonitoringUUID(request.WorkspaceID) &&
		validMonitoringUUID(request.SessionID) && validMonitoringUUID(request.MonitorID) && validMonitoringUUID(request.CycleID) &&
		validMonitoringUUID(request.SearchPlanID) && request.SearchPlanVersion > 0
}

func normalizeMonitoringExpectedQueries(values []MonitoringExpectedQuery) ([]MonitoringExpectedQuery, error) {
	if len(values) == 0 || len(values) > 512 {
		return nil, fmt.Errorf("invalid query count")
	}
	normalized := append([]MonitoringExpectedQuery(nil), values...)
	for index := range normalized {
		normalized[index].QueryKey = strings.TrimSpace(normalized[index].QueryKey)
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].QueryKey < normalized[j].QueryKey })
	for index, query := range normalized {
		if query.QueryKey == "" || !validMonitoringHash(query.CanonicalQueryHash) || index > 0 && normalized[index-1].QueryKey == query.QueryKey {
			return nil, fmt.Errorf("invalid or duplicate query")
		}
	}
	return normalized, nil
}

func normalizedMonitoringUUIDs(values []string, limit int) ([]string, error) {
	if len(values) > limit {
		return nil, fmt.Errorf("too many UUIDs")
	}
	normalized := append([]string(nil), values...)
	sort.Strings(normalized)
	for index, value := range normalized {
		if !validMonitoringUUID(value) || index > 0 && normalized[index-1] == value {
			return nil, fmt.Errorf("invalid or duplicate UUID")
		}
	}
	return normalized, nil
}

func validMonitoringUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func validMonitoringHash(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	for _, char := range strings.TrimPrefix(value, "sha256:") {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
