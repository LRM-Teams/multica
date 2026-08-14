package researcheval

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/multica-ai/multica/server/internal/researchrun"
)

const (
	evaluationDocumentIDMetadataKey  = "evaluation_document_id"
	evaluationSubjectHashMetadataKey = "evaluation_subject_hash"
	evaluationFactKeyDatumKey        = "evaluation_fact_key"
	evaluationFactValueDatumKey      = "evaluation_fact_value"
)

// CanonicalArtifactInput combines the production Run ledger with canonical
// surfaces not yet carried by researchrun.RunSnapshot. Supplements are facts
// loaded from their owning tables/projection, never reconstructed from prose.
type CanonicalArtifactInput struct {
	SubjectHash     string
	Subject         SubjectInput
	Snapshot        researchrun.RunSnapshot
	ReportClaimKeys []string
	ReportMD        string
	Conflicts       []ArtifactConflict
	Actions         []ArtifactAction
	GraphNodes      []ArtifactGraphNode
	GraphEdges      []ArtifactGraphEdge
	Projection      *ArtifactProjection
}

// BuildArtifactFromCanonicalRun converts observable production state into the
// exact Artifact consumed by the existing hidden-Oracle graders. Every Source
// and Observation must carry the evaluation identity committed during
// controlled ingestion; the adapter never guesses from URLs, titles, or text.
func BuildArtifactFromCanonicalRun(input CanonicalArtifactInput) (Artifact, error) {
	if !validSubjectHash(input.SubjectHash) {
		return Artifact{}, fmt.Errorf("%w: canonical evaluation subject hash is required", ErrInvalidEvaluation)
	}
	documents := make(map[string]Document, len(input.Subject.Environment.Documents))
	for _, document := range input.Subject.Environment.Documents {
		if strings.TrimSpace(document.ID) == "" || strings.TrimSpace(document.Family) == "" {
			return Artifact{}, fmt.Errorf("%w: controlled document identity and family are required", ErrInvalidEvaluation)
		}
		if _, duplicate := documents[document.ID]; duplicate {
			return Artifact{}, fmt.Errorf("%w: duplicate controlled document %q", ErrInvalidEvaluation, document.ID)
		}
		documents[document.ID] = document
	}

	artifact := Artifact{
		Sources: []ArtifactSource{}, Facts: []ArtifactFact{}, Claims: []ArtifactClaim{},
		Conflicts: append([]ArtifactConflict(nil), input.Conflicts...), Actions: append([]ArtifactAction(nil), input.Actions...),
		GraphNodes: append([]ArtifactGraphNode(nil), input.GraphNodes...), GraphEdges: append([]ArtifactGraphEdge(nil), input.GraphEdges...),
		Projection: cloneArtifactProjection(input.Projection), ReportMD: input.ReportMD,
	}
	sourceDocuments := make(map[string]string, len(input.Snapshot.Sources))
	artifactSources := make(map[string]ArtifactSource, len(input.Snapshot.Sources))
	for _, source := range input.Snapshot.Sources {
		var metadata map[string]any
		if err := json.Unmarshal(source.Metadata, &metadata); err != nil {
			return Artifact{}, fmt.Errorf("%w: source %q has malformed canonical metadata", ErrInvalidEvaluation, source.ID)
		}
		documentID, _ := metadata[evaluationDocumentIDMetadataKey].(string)
		subjectHash, _ := metadata[evaluationSubjectHashMetadataKey].(string)
		document, exists := documents[documentID]
		if !exists || subjectHash != input.SubjectHash {
			return Artifact{}, fmt.Errorf("%w: source %q is not bound to this controlled subject", ErrInvalidEvaluation, source.ID)
		}
		if _, duplicate := sourceDocuments[source.ID]; duplicate {
			return Artifact{}, fmt.Errorf("%w: duplicate canonical source %q", ErrInvalidEvaluation, source.ID)
		}
		sourceDocuments[source.ID] = documentID
		projected := artifactSources[documentID]
		projected.DocumentID, projected.Family = documentID, document.Family
		projected.Accepted = projected.Accepted || source.VerificationStatus == "verified"
		artifactSources[documentID] = projected
	}
	for _, source := range artifactSources {
		artifact.Sources = append(artifact.Sources, source)
	}

	factByObservation := make(map[string]ArtifactFact, len(input.Snapshot.Observations))
	for _, observation := range input.Snapshot.Observations {
		documentID, exists := sourceDocuments[observation.SourceSnapshotID]
		if !exists {
			return Artifact{}, fmt.Errorf("%w: observation %q references a source outside the controlled subject", ErrInvalidEvaluation, observation.ID)
		}
		var datum map[string]any
		if err := json.Unmarshal(observation.Datum, &datum); err != nil {
			return Artifact{}, fmt.Errorf("%w: observation %q has malformed canonical datum", ErrInvalidEvaluation, observation.ID)
		}
		key, _ := datum[evaluationFactKeyDatumKey].(string)
		value, _ := datum[evaluationFactValueDatumKey].(string)
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key == "" || value == "" {
			return Artifact{}, fmt.Errorf("%w: observation %q lacks canonical evaluation fact identity", ErrInvalidEvaluation, observation.ID)
		}
		fact := ArtifactFact{Key: key, Value: value, SourceIDs: []string{documentID}}
		if prior, duplicate := factByObservation[observation.ID]; duplicate && (prior.Key != fact.Key || prior.Value != fact.Value || prior.SourceIDs[0] != documentID) {
			return Artifact{}, fmt.Errorf("%w: conflicting duplicate observation %q", ErrInvalidEvaluation, observation.ID)
		}
		factByObservation[observation.ID] = fact
	}
	factsByKey := map[string]ArtifactFact{}
	for _, fact := range factByObservation {
		if prior, exists := factsByKey[fact.Key]; exists {
			if prior.Value != fact.Value {
				return Artifact{}, fmt.Errorf("%w: fact %q has conflicting canonical values", ErrInvalidEvaluation, fact.Key)
			}
			prior.SourceIDs = appendUniqueSorted(prior.SourceIDs, fact.SourceIDs...)
			factsByKey[fact.Key] = prior
			continue
		}
		factsByKey[fact.Key] = fact
	}
	for _, fact := range factsByKey {
		artifact.Facts = append(artifact.Facts, fact)
	}

	reportClaims := stringSet(input.ReportClaimKeys)
	observedClaims := map[string]bool{}
	for _, claim := range input.Snapshot.Claims {
		factKeys, sourceIDs := []string{}, []string{}
		for _, evidence := range claim.Evidence {
			fact, exists := factByObservation[evidence.ObservationID]
			if !exists {
				return Artifact{}, fmt.Errorf("%w: claim %q references an observation outside the controlled subject", ErrInvalidEvaluation, claim.ClientKey)
			}
			factKeys = appendUniqueSorted(factKeys, fact.Key)
			sourceIDs = appendUniqueSorted(sourceIDs, fact.SourceIDs...)
		}
		key := strings.TrimSpace(claim.ClientKey)
		if key == "" {
			return Artifact{}, fmt.Errorf("%w: canonical claim key is required", ErrInvalidEvaluation)
		}
		if observedClaims[key] {
			return Artifact{}, fmt.Errorf("%w: duplicate canonical claim %q", ErrInvalidEvaluation, key)
		}
		observedClaims[key] = true
		artifact.Claims = append(artifact.Claims, ArtifactClaim{Key: key, FactKeys: factKeys, SourceIDs: sourceIDs, InReport: reportClaims[key]})
	}
	for key := range reportClaims {
		if !observedClaims[key] {
			return Artifact{}, fmt.Errorf("%w: report references unknown canonical claim %q", ErrInvalidEvaluation, key)
		}
	}
	sort.Slice(artifact.Sources, func(i, j int) bool { return artifact.Sources[i].DocumentID < artifact.Sources[j].DocumentID })
	sort.Slice(artifact.Facts, func(i, j int) bool { return artifact.Facts[i].Key < artifact.Facts[j].Key })
	sort.Slice(artifact.Claims, func(i, j int) bool { return artifact.Claims[i].Key < artifact.Claims[j].Key })
	return artifact, nil
}

func validSubjectHash(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "sha256:") {
		return false
	}
	value = strings.TrimPrefix(value, "sha256:")
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func appendUniqueSorted(values []string, additional ...string) []string {
	seen := stringSet(values)
	for _, value := range additional {
		if value = strings.TrimSpace(value); value != "" && !seen[value] {
			values = append(values, value)
			seen[value] = true
		}
	}
	sort.Strings(values)
	return values
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out[value] = true
		}
	}
	return out
}

func cloneArtifactProjection(value *ArtifactProjection) *ArtifactProjection {
	if value == nil {
		return nil
	}
	clone := *value
	clone.ObservedNodeIDs = append([]string(nil), value.ObservedNodeIDs...)
	return &clone
}
