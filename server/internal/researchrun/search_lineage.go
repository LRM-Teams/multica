package researchrun

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const maxSearchLineageCandidates = 100

var searchLineageFailureClasses = map[string]struct{}{
	"rate_limited": {}, "timeout": {}, "provider_unavailable": {}, "cursor_expired": {},
	"not_found": {}, "permission_denied": {}, "unsafe_target": {}, "unsupported_content": {},
	"content_too_large": {}, "invalid_response": {},
}

type SearchLineageBatch struct {
	WorkspaceID     string
	SessionID       string
	TaskID          string
	AttemptID       string
	PlanClientKey   string
	PlanObjective   string
	ClientRequestID string
	Adapter         string
	Query           string
	CursorIn        string
	CursorOut       string
	Status          string
	FailureClass    string
	FailureReason   string
	Cost            json.RawMessage
	Safety          json.RawMessage
	ExecutedAt      time.Time
	Candidates      []SearchLineageCandidate
}

type SearchLineageCandidate struct {
	ClientKey                   string
	CanonicalURL                string
	CanonicalIdentity           string
	Title                       string
	Snippet                     string
	Publisher                   string
	IndependenceFamily          string
	ContentHash                 string
	Position                    int
	Metadata                    json.RawMessage
	Disposition                 string
	ReasonCode                  string
	Reason                      string
	EffectiveIndependenceFamily string
	CanonicalCandidateKey       string
	DecidedAt                   time.Time
}

type SearchLineageBatchResult struct {
	PlanID           string
	QueryExecutionID string
	CandidateIDs     map[string]string
	DecisionIDs      map[string]string
	Replayed         bool
}

type SourceSearchLineage struct {
	SourceSnapshotID    string
	IngestionKind       string
	ScreeningDecisionID string
	SourceCandidateID   string
	QueryExecutionID    string
	SearchPlanID        string
	Disposition         string
	ReasonCode          string
}

func validateSearchLineageBatch(in SearchLineageBatch) error {
	for name, value := range map[string]string{
		"workspace_id": in.WorkspaceID, "session_id": in.SessionID,
		"task_id": in.TaskID, "attempt_id": in.AttemptID,
	} {
		if _, err := uuid.Parse(value); err != nil {
			return fmt.Errorf("%w: %s is not a UUID", ErrInvalidContract, name)
		}
	}
	for name, field := range map[string]struct {
		value string
		limit int
	}{
		"plan_client_key": {in.PlanClientKey, 512}, "plan_objective": {in.PlanObjective, 32768},
		"client_request_id": {in.ClientRequestID, 512}, "adapter": {in.Adapter, 160},
		"query": {in.Query, 32768},
	} {
		if strings.TrimSpace(field.value) != field.value || field.value == "" || len(field.value) > field.limit {
			return fmt.Errorf("%w: %s is invalid", ErrInvalidContract, name)
		}
	}
	if len(in.CursorIn) > 4096 || len(in.CursorOut) > 4096 || in.ExecutedAt.IsZero() {
		return fmt.Errorf("%w: Search execution cursor or time is invalid", ErrInvalidContract)
	}
	if in.Status != "succeeded" && in.Status != "failed" {
		return fmt.Errorf("%w: Search execution status is invalid", ErrInvalidContract)
	}
	if in.Status == "succeeded" && (in.FailureClass != "" || in.FailureReason != "") ||
		in.Status == "failed" && (strings.TrimSpace(in.FailureClass) == "" || strings.TrimSpace(in.FailureReason) == "" || len(in.FailureClass) > 160 || len(in.FailureReason) > 4096) {
		return fmt.Errorf("%w: Search execution failure facts are inconsistent", ErrInvalidContract)
	}
	if in.Status == "failed" {
		if _, ok := searchLineageFailureClasses[in.FailureClass]; !ok {
			return fmt.Errorf("%w: Search execution failure class is unknown", ErrInvalidContract)
		}
	}
	if in.Status == "failed" && len(in.Candidates) > 0 || len(in.Candidates) > maxSearchLineageCandidates {
		return fmt.Errorf("%w: failed or oversized Search execution has candidates", ErrInvalidContract)
	}
	if err := validateJSONObject(in.Cost, "Search cost"); err != nil {
		return err
	}
	if err := validateJSONObject(in.Safety, "Search safety"); err != nil {
		return err
	}
	keys := map[string]bool{}
	identities := map[string]bool{}
	positions := map[int]bool{}
	for _, candidate := range in.Candidates {
		if err := validateSearchLineageCandidate(candidate); err != nil {
			return err
		}
		if keys[candidate.ClientKey] || identities[candidate.CanonicalIdentity] || positions[candidate.Position] {
			return fmt.Errorf("%w: Search candidates repeat key, identity, or position", ErrInvalidContract)
		}
		keys[candidate.ClientKey], identities[candidate.CanonicalIdentity], positions[candidate.Position] = true, true, true
	}
	for _, candidate := range in.Candidates {
		if candidate.Disposition == "duplicate" {
			if !keys[candidate.CanonicalCandidateKey] || candidate.CanonicalCandidateKey == candidate.ClientKey {
				return fmt.Errorf("%w: duplicate Screening Decision has invalid canonical candidate", ErrInvalidContract)
			}
		} else if candidate.CanonicalCandidateKey != "" {
			return fmt.Errorf("%w: non-duplicate Screening Decision names a canonical candidate", ErrInvalidContract)
		}
	}
	return nil
}

func validateSearchLineageCandidate(candidate SearchLineageCandidate) error {
	canonical, err := CanonicalURL(candidate.CanonicalURL)
	parsed, parseErr := url.Parse(candidate.CanonicalURL)
	if err != nil || parseErr != nil || parsed.User != nil || canonical != candidate.CanonicalURL || !validBoundedSearchToken(candidate.ClientKey, 512) ||
		!validBoundedSearchToken(candidate.CanonicalIdentity, 512) || !validBoundedSearchToken(candidate.IndependenceFamily, 512) ||
		!validBoundedSearchToken(candidate.ReasonCode, 160) || !validBoundedSearchToken(candidate.Reason, 4096) ||
		!validBoundedSearchToken(candidate.EffectiveIndependenceFamily, 512) || candidate.Position < 1 || candidate.DecidedAt.IsZero() {
		return fmt.Errorf("%w: Source Candidate or Screening Decision is invalid", ErrInvalidContract)
	}
	if len(candidate.Title) > 4096 || len(candidate.Snippet) > 32768 || len(candidate.Publisher) > 4096 ||
		candidate.ContentHash != "" && !validPrefixedSHA256(candidate.ContentHash) {
		return fmt.Errorf("%w: Source Candidate content metadata is invalid", ErrInvalidContract)
	}
	if candidate.Disposition != "accepted" && candidate.Disposition != "excluded" && candidate.Disposition != "duplicate" {
		return fmt.Errorf("%w: Screening Decision disposition is invalid", ErrInvalidContract)
	}
	return validateJSONObject(candidate.Metadata, "Source Candidate metadata")
}

func validateJSONObject(raw json.RawMessage, name string) error {
	if len(raw) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil {
		return fmt.Errorf("%w: %s must be one JSON object", ErrInvalidContract, name)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("%w: %s must be one JSON object", ErrInvalidContract, name)
	}
	return nil
}

func validBoundedSearchToken(value string, limit int) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= limit
}

func validPrefixedSHA256(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, char := range value[7:] {
		if char < '0' || char > '9' && char < 'a' || char > 'f' {
			return false
		}
	}
	return true
}

func hashSearchLineageBatch(in SearchLineageBatch) (string, error) {
	encoded, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", hash), nil
}
