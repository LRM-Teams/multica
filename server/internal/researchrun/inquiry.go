package researchrun

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type InquiryEntityKind string
type InquiryRelation string

const (
	InquiryKindQuestion   InquiryEntityKind = "question"
	InquiryKindHypothesis InquiryEntityKind = "hypothesis"
	InquiryKindBranch     InquiryEntityKind = "branch"
	InquiryKindClaim      InquiryEntityKind = "claim"
	InquiryKindInsight    InquiryEntityKind = "insight"
	InquiryKindDispute    InquiryEntityKind = "dispute"

	InquiryRelationDecomposes   InquiryRelation = "decomposes"
	InquiryRelationTests        InquiryRelation = "tests"
	InquiryRelationExplains     InquiryRelation = "explains"
	InquiryRelationDependsOn    InquiryRelation = "depends_on"
	InquiryRelationCompetesWith InquiryRelation = "competes_with"
	InquiryRelationRefines      InquiryRelation = "refines"
	InquiryRelationInvalidates  InquiryRelation = "invalidates"
	InquiryRelationMotivates    InquiryRelation = "motivates"
)

type inquiryEndpoint struct {
	Kind InquiryEntityKind
	ID   string
}

type inquiryEdgeCommand struct {
	From      inquiryEndpoint
	To        inquiryEndpoint
	Relation  InquiryRelation
	Rationale string
}

type inquiryStatusEvidenceRef struct {
	Kind string
	ID   string
}

type inquiryStatusUpdateCommand struct {
	Target       inquiryEndpoint
	Before       string
	After        string
	Reason       string
	EvidenceRefs []inquiryStatusEvidenceRef
}

// inquiryModule is the internal seam for Inquiry Graph invariants. Callers
// submit intent; endpoint resolution, lifecycle rules and DAG rules remain
// implementation details shared by production persistence and tests.
type inquiryModule struct{}

func (inquiryModule) ValidateTransition(kind InquiryEntityKind, from, to string) error {
	statuses, knownKind := inquiryStatuses[kind]
	if !knownKind || !statuses[from] || !statuses[to] {
		return fmt.Errorf("%w: unknown %s status transition %s -> %s", ErrInvalidTransition, kind, from, to)
	}
	if from == to {
		return nil
	}
	allowed := inquiryTransitions[kind][from][to]
	if !allowed {
		return fmt.Errorf("%w: invalid %s status transition %s -> %s", ErrInvalidTransition, kind, from, to)
	}
	return nil
}

// ValidateStatusUpdate is the interface for evidence-driven Inquiry lifecycle
// changes. The persistence adapter supplies resolved, same-session UUIDs; this
// module owns transition vocabulary and the minimum auditable explanation.
func (m inquiryModule) ValidateStatusUpdate(command inquiryStatusUpdateCommand) error {
	if command.Target.Kind != InquiryKindQuestion && command.Target.Kind != InquiryKindHypothesis &&
		command.Target.Kind != InquiryKindBranch && command.Target.Kind != InquiryKindInsight {
		return fmt.Errorf("%w: unsupported inquiry status target %q", ErrInvalidContract, command.Target.Kind)
	}
	if _, err := uuid.Parse(command.Target.ID); err != nil {
		return fmt.Errorf("%w: inquiry status target is not resolved", ErrInvalidContract)
	}
	if command.Before == command.After {
		return fmt.Errorf("%w: inquiry status update is a no-op", ErrInvalidTransition)
	}
	if err := m.ValidateTransition(command.Target.Kind, command.Before, command.After); err != nil {
		return err
	}
	if strings.TrimSpace(command.Reason) == "" || len(command.Reason) > 32768 {
		return fmt.Errorf("%w: inquiry status update requires a substantive reason", ErrInvalidContract)
	}
	if len(command.EvidenceRefs) == 0 || len(command.EvidenceRefs) > 128 {
		return fmt.Errorf("%w: inquiry status update requires bounded evidence", ErrInvalidContract)
	}
	seen := make(map[inquiryStatusEvidenceRef]struct{}, len(command.EvidenceRefs))
	for _, ref := range command.EvidenceRefs {
		if !inquiryStatusEvidenceKinds[ref.Kind] {
			return fmt.Errorf("%w: unknown status evidence kind %q", ErrInvalidContract, ref.Kind)
		}
		if _, err := uuid.Parse(ref.ID); err != nil {
			return fmt.Errorf("%w: status evidence reference is not resolved", ErrInvalidContract)
		}
		if _, duplicate := seen[ref]; duplicate {
			return fmt.Errorf("%w: duplicate status evidence reference", ErrInvalidContract)
		}
		seen[ref] = struct{}{}
	}
	return nil
}

func (inquiryModule) ValidateEdge(command inquiryEdgeCommand) error {
	if !inquiryKinds[command.From.Kind] || !inquiryKinds[command.To.Kind] || !inquiryRelations[command.Relation] {
		return fmt.Errorf("%w: unknown inquiry edge vocabulary", ErrInvalidContract)
	}
	if command.From.ID == "" || command.To.ID == "" {
		return fmt.Errorf("%w: inquiry edge endpoint is empty", ErrInvalidContract)
	}
	if command.From == command.To {
		return fmt.Errorf("%w: inquiry edge cannot reference itself", ErrInvalidContract)
	}
	if len(command.Rationale) > 32768 {
		return fmt.Errorf("%w: inquiry edge rationale is too large", ErrInvalidContract)
	}
	return nil
}

func inquiryRelationMustBeAcyclic(relation InquiryRelation) bool {
	switch relation {
	case InquiryRelationDecomposes, InquiryRelationDependsOn, InquiryRelationRefines:
		return true
	default:
		return false
	}
}

var inquiryKinds = map[InquiryEntityKind]bool{
	InquiryKindQuestion: true, InquiryKindHypothesis: true, InquiryKindBranch: true,
	InquiryKindClaim: true, InquiryKindInsight: true, InquiryKindDispute: true,
}

var inquiryRelations = map[InquiryRelation]bool{
	InquiryRelationDecomposes: true, InquiryRelationTests: true,
	InquiryRelationExplains: true, InquiryRelationDependsOn: true,
	InquiryRelationCompetesWith: true, InquiryRelationRefines: true,
	InquiryRelationInvalidates: true, InquiryRelationMotivates: true,
}

var inquiryStatuses = map[InquiryEntityKind]map[string]bool{
	InquiryKindQuestion: {
		"open": true, "in_progress": true, "answered": true, "unresolved": true, "obsolete": true,
	},
	InquiryKindHypothesis: {
		"proposed": true, "investigating": true, "supported": true, "weakened": true,
		"refuted": true, "conditional": true, "obsolete": true,
	},
	InquiryKindBranch: {
		"proposed": true, "active": true, "paused": true, "completed": true,
		"terminated": true, "obsolete": true,
	},
	InquiryKindInsight: {
		"proposed": true, "accepted": true, "stale": true, "superseded": true, "obsolete": true,
	},
}

var inquiryTransitions = map[InquiryEntityKind]map[string]map[string]bool{
	InquiryKindQuestion: {
		"open":        {"in_progress": true, "answered": true, "unresolved": true, "obsolete": true},
		"in_progress": {"answered": true, "unresolved": true, "obsolete": true},
		"answered":    {"in_progress": true, "unresolved": true, "obsolete": true},
		"unresolved":  {"in_progress": true, "answered": true, "obsolete": true},
	},
	InquiryKindHypothesis: {
		"proposed":      {"investigating": true, "obsolete": true},
		"investigating": {"supported": true, "weakened": true, "refuted": true, "conditional": true, "obsolete": true},
		"supported":     {"investigating": true, "weakened": true, "refuted": true, "conditional": true, "obsolete": true},
		"weakened":      {"investigating": true, "supported": true, "refuted": true, "conditional": true, "obsolete": true},
		"refuted":       {"investigating": true, "obsolete": true},
		"conditional":   {"investigating": true, "supported": true, "weakened": true, "refuted": true, "obsolete": true},
	},
	InquiryKindBranch: {
		"proposed":   {"active": true, "terminated": true, "obsolete": true},
		"active":     {"paused": true, "completed": true, "terminated": true, "obsolete": true},
		"paused":     {"active": true, "terminated": true, "obsolete": true},
		"completed":  {"obsolete": true},
		"terminated": {"obsolete": true},
	},
	InquiryKindInsight: {
		"proposed":   {"accepted": true, "obsolete": true},
		"accepted":   {"stale": true, "superseded": true, "obsolete": true},
		"stale":      {"accepted": true, "superseded": true, "obsolete": true},
		"superseded": {"obsolete": true},
	},
}

var inquiryStatusEvidenceKinds = map[string]bool{
	"question": true, "hypothesis": true, "branch": true, "claim": true,
	"insight": true, "task": true, "source": true,
}
