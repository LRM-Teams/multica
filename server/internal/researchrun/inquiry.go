package researchrun

import (
	"fmt"
	"strings"
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

type TaskInquiryTarget struct {
	Kind     InquiryEntityKind `json:"kind"`
	EntityID string            `json:"entity_id"`
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

func (inquiryModule) ValidateTaskTargets(targets []TaskInquiryTarget) error {
	if len(targets) == 0 || len(targets) > 32 {
		return fmt.Errorf("%w: a Task requires 1..32 Inquiry targets", ErrInvalidContract)
	}
	seen := make(map[string]bool, len(targets))
	for _, target := range targets {
		key := string(target.Kind) + ":" + target.EntityID
		if !inquiryKinds[target.Kind] || target.Kind == InquiryKindDispute || strings.TrimSpace(target.EntityID) == "" || seen[key] {
			return fmt.Errorf("%w: invalid Task Inquiry target %q", ErrInvalidContract, key)
		}
		seen[key] = true
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
