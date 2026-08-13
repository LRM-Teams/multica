package researchrun

// ArtifactPurpose identifies why a principal reads an artifact.
type ArtifactPurpose string

const (
	ArtifactPurposeTaskExecution ArtifactPurpose = "task_execution"
	ArtifactPurposeEvaluation    ArtifactPurpose = "evaluation"
)

// ArtifactClearance is the normal access profile held by a principal.
type ArtifactClearance string

const (
	ArtifactClearanceVerifiedOnly ArtifactClearance = "verified_only"
	ArtifactClearanceRedacted     ArtifactClearance = "redacted"
	ArtifactClearanceRaw          ArtifactClearance = "raw"
)

// ArtifactDenyReason classifies authorization failures.
type ArtifactDenyReason string

const (
	ArtifactDenyUnknownKind           ArtifactDenyReason = "unknown_kind"
	ArtifactDenyUnknownAccess         ArtifactDenyReason = "unknown_access"
	ArtifactDenyInsufficientClearance ArtifactDenyReason = "insufficient_clearance"
	ArtifactDenyEvaluationCompartment ArtifactDenyReason = "evaluation_compartment"
	ArtifactDenyLifecycle             ArtifactDenyReason = "lifecycle"
	ArtifactDenyMissingPassport       ArtifactDenyReason = "missing_passport"
)

// ArtifactPolicy implements the section 6 access lattice and legacy admission gate.
type ArtifactPolicy struct{}

func (ArtifactPolicy) NormalAccessDominates(holder, required ArtifactAccessLevel) bool {
	order := map[ArtifactAccessLevel]int{
		ArtifactAccessVerifiedOnly: 0,
		ArtifactAccessRedacted:     1,
		ArtifactAccessRaw:          2,
	}
	holderRank, okHolder := order[holder]
	requiredRank, okRequired := order[required]
	if !okHolder || !okRequired {
		return false
	}
	return holderRank >= requiredRank
}

func (ArtifactPolicy) ClearanceForAccess(level ArtifactAccessLevel) ArtifactClearance {
	switch level {
	case ArtifactAccessVerifiedOnly:
		return ArtifactClearanceVerifiedOnly
	case ArtifactAccessRedacted:
		return ArtifactClearanceRedacted
	case ArtifactAccessRaw:
		return ArtifactClearanceRaw
	default:
		return ""
	}
}

func (ArtifactPolicy) CanReadNormal(
	clearance ArtifactClearance,
	level ArtifactAccessLevel,
	purpose ArtifactPurpose,
	evaluationPrivate bool,
) (bool, ArtifactDenyReason) {
	if evaluationPrivate && purpose != ArtifactPurposeEvaluation {
		return false, ArtifactDenyEvaluationCompartment
	}
	required := (ArtifactPolicy{}).ClearanceForAccess(level)
	if required == "" {
		return false, ArtifactDenyUnknownAccess
	}
	if !(ArtifactPolicy{}).NormalAccessDominates(ArtifactAccessLevel(clearance), level) {
		return false, ArtifactDenyInsufficientClearance
	}
	return true, ""
}

// EvaluationPrivateKind reports entity kinds that belong to the evaluation compartment
// and must never appear in ordinary task-execution manifests or subject context.
func (ArtifactPolicy) EvaluationPrivateKind(kind ArtifactEntityKind) bool {
	switch kind {
	case ArtifactKindStageEvaluation:
		return true
	default:
		return false
	}
}

// LegacyAdmissionAllowed implements legacy-v1-v5-compat-v1 ordinary-task admission.
func (ArtifactPolicy) LegacyAdmissionAllowed(
	kind ArtifactEntityKind,
	lifecycle ArtifactLifecycleStatus,
	provenance ArtifactProvenanceCompleteness,
) (bool, ArtifactDenyReason) {
	if _, err := ParseArtifactEntityKind(string(kind)); err != nil {
		return false, ArtifactDenyUnknownKind
	}
	switch lifecycle {
	case ArtifactLifecycleRegistered, ArtifactLifecycleAccepted:
	default:
		return false, ArtifactDenyLifecycle
	}
	switch provenance {
	case ArtifactProvenanceComplete, ArtifactProvenancePartial, ArtifactProvenanceUnknown:
	default:
		return false, ArtifactDenyMissingPassport
	}
	return true, ""
}

func (ArtifactPolicy) ManifestOmissionReason(deny ArtifactDenyReason) string {
	switch deny {
	case ArtifactDenyInsufficientClearance:
		return "insufficient_clearance"
	case ArtifactDenyEvaluationCompartment:
		return "evaluation_compartment"
	case ArtifactDenyLifecycle:
		return "lifecycle"
	default:
		return "policy_denied"
	}
}

func defaultTaskExecutionClearance() ArtifactClearance {
	return ArtifactClearanceRaw
}

func manifestPurposeForTask() ArtifactPurpose {
	return ArtifactPurposeTaskExecution
}

func manifestPurposeForTaskKind(kind TaskKind) ArtifactPurpose {
	if kind == TaskKindQualityGate || kind == TaskKindCitationAudit {
		return ArtifactPurposeEvaluation
	}
	return ArtifactPurposeTaskExecution
}
