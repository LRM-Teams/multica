package researchrun

import "fmt"

// ArtifactEntityKind identifies one canonical Research artifact passport.
type ArtifactEntityKind string

const (
	ArtifactKindRunSession          ArtifactEntityKind = "run_session"
	ArtifactKindContractRevision    ArtifactEntityKind = "contract_revision"
	ArtifactKindMethodDecision        ArtifactEntityKind = "method_decision"
	ArtifactKindQuestion              ArtifactEntityKind = "question"
	ArtifactKindTask                  ArtifactEntityKind = "task"
	ArtifactKindAttempt               ArtifactEntityKind = "attempt"
	ArtifactKindResultArtifact        ArtifactEntityKind = "result_artifact"
	ArtifactKindLegacySource          ArtifactEntityKind = "legacy_source"
	ArtifactKindSourceSnapshot        ArtifactEntityKind = "source_snapshot"
	ArtifactKindObservation           ArtifactEntityKind = "observation"
	ArtifactKindClaim                 ArtifactEntityKind = "claim"
	ArtifactKindEvidenceLink          ArtifactEntityKind = "evidence_link"
	ArtifactKindReportRevision        ArtifactEntityKind = "report_revision"
	ArtifactKindEvaluationDecision    ArtifactEntityKind = "evaluation_decision"
	ArtifactKindStageEvaluation       ArtifactEntityKind = "stage_evaluation"
	ArtifactKindResearchMessage       ArtifactEntityKind = "research_message"
	ArtifactKindProductRoundDecision  ArtifactEntityKind = "product_round_decision"
	ArtifactKindContextManifest       ArtifactEntityKind = "context_manifest"
	ArtifactKindRunEvent              ArtifactEntityKind = "run_event"
	ArtifactKindGraphNode             ArtifactEntityKind = "graph_node"
	ArtifactKindGraphEdge             ArtifactEntityKind = "graph_edge"
)

var registeredArtifactEntityKinds = map[ArtifactEntityKind]struct{}{
	ArtifactKindRunSession:         {},
	ArtifactKindContractRevision:   {},
	ArtifactKindMethodDecision:     {},
	ArtifactKindQuestion:           {},
	ArtifactKindTask:               {},
	ArtifactKindAttempt:            {},
	ArtifactKindResultArtifact:     {},
	ArtifactKindLegacySource:       {},
	ArtifactKindSourceSnapshot:     {},
	ArtifactKindObservation:        {},
	ArtifactKindClaim:              {},
	ArtifactKindEvidenceLink:       {},
	ArtifactKindReportRevision:     {},
	ArtifactKindEvaluationDecision: {},
	ArtifactKindStageEvaluation:    {},
	ArtifactKindResearchMessage:    {},
	ArtifactKindProductRoundDecision: {},
	ArtifactKindContextManifest:    {},
	ArtifactKindRunEvent:           {},
	ArtifactKindGraphNode:          {},
	ArtifactKindGraphEdge:          {},
}

// ArtifactLifecycleStatus is passport admissibility, not domain status.
type ArtifactLifecycleStatus string

const (
	ArtifactLifecycleRegistered ArtifactLifecycleStatus = "registered"
	ArtifactLifecycleAccepted   ArtifactLifecycleStatus = "accepted"
	ArtifactLifecycleRejected   ArtifactLifecycleStatus = "rejected"
	ArtifactLifecycleStale      ArtifactLifecycleStatus = "stale"
	ArtifactLifecycleSuperseded ArtifactLifecycleStatus = "superseded"
	ArtifactLifecycleWithdrawn  ArtifactLifecycleStatus = "withdrawn"
)

// ArtifactProvenanceCompleteness records how much producer history storage proves.
type ArtifactProvenanceCompleteness string

const (
	ArtifactProvenanceComplete ArtifactProvenanceCompleteness = "complete"
	ArtifactProvenancePartial  ArtifactProvenanceCompleteness = "partial"
	ArtifactProvenanceUnknown  ArtifactProvenanceCompleteness = "unknown"
)

// ArtifactAccessLevel is a policy profile for ordinary Research execution.
type ArtifactAccessLevel string

const (
	ArtifactAccessVerifiedOnly ArtifactAccessLevel = "verified_only"
	ArtifactAccessRedacted     ArtifactAccessLevel = "redacted"
	ArtifactAccessRaw          ArtifactAccessLevel = "raw"
)

// ArtifactHashOrigin records how a version content hash was produced.
type ArtifactHashOrigin string

const (
	ArtifactHashOriginProduction           ArtifactHashOrigin = "production"
	ArtifactHashOriginMigrationRecomputed  ArtifactHashOrigin = "migration_recomputed"
	ArtifactHashOriginLegacyStored         ArtifactHashOrigin = "legacy_stored"
)

// ArtifactCanonicalizationVersion is the active canonical JSON profile.
const ArtifactCanonicalizationVersion = "research-artifact-c14n-v1"

// LegacyV1V5CompatPolicy is the named ordinary-task admission exception for backfilled rows.
const LegacyV1V5CompatPolicy = "legacy-v1-v5-compat-v1"

func ParseArtifactEntityKind(raw string) (ArtifactEntityKind, error) {
	kind := ArtifactEntityKind(raw)
	if _, ok := registeredArtifactEntityKinds[kind]; !ok {
		return "", fmt.Errorf("%w: unknown artifact entity kind %q", ErrInvalidContract, raw)
	}
	return kind, nil
}

func RegisteredArtifactEntityKinds() []ArtifactEntityKind {
	out := make([]ArtifactEntityKind, 0, len(registeredArtifactEntityKinds))
	for kind := range registeredArtifactEntityKinds {
		out = append(out, kind)
	}
	return out
}
