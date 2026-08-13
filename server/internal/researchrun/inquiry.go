package researchrun

import (
	"fmt"
	"sort"
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

type InquiryTransitionInput struct {
	WorkspaceID          string                    `json:"-"`
	SessionID            string                    `json:"-"`
	AttemptID            string                    `json:"attempt_id"`
	AgentID              string                    `json:"agent_id"`
	IdempotencyKey       string                    `json:"idempotency_key"`
	ExpectedStateVersion int64                     `json:"expected_state_version"`
	Changes              []InquiryTransitionChange `json:"changes"`
}

type InquiryTransitionChange struct {
	Kind         InquiryEntityKind `json:"kind"`
	EntityID     string            `json:"entity_id"`
	BeforeStatus string            `json:"before_status"`
	AfterStatus  string            `json:"after_status"`
	Reason       string            `json:"reason"`
}

type InquiryTransitionResult struct {
	Event RunEvent `json:"event"`
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

func (module inquiryModule) ValidateTransitionInput(in InquiryTransitionInput) error {
	if strings.TrimSpace(in.WorkspaceID) == "" || strings.TrimSpace(in.SessionID) == "" || strings.TrimSpace(in.AttemptID) == "" ||
		strings.TrimSpace(in.AgentID) == "" || strings.TrimSpace(in.IdempotencyKey) == "" || len(in.IdempotencyKey) > 512 ||
		in.ExpectedStateVersion < 1 || len(in.Changes) == 0 || len(in.Changes) > 256 {
		return fmt.Errorf("%w: incomplete inquiry transition identity", ErrInvalidContract)
	}
	seen := make(map[string]bool, len(in.Changes))
	for _, change := range in.Changes {
		key := string(change.Kind) + ":" + change.EntityID
		if strings.TrimSpace(change.EntityID) == "" || seen[key] || change.BeforeStatus == change.AfterStatus ||
			strings.TrimSpace(change.Reason) == "" || len(change.Reason) > 32768 {
			return fmt.Errorf("%w: invalid inquiry transition change %q", ErrInvalidContract, key)
		}
		if err := module.ValidateTransition(change.Kind, change.BeforeStatus, change.AfterStatus); err != nil {
			return err
		}
		seen[key] = true
	}
	return nil
}

func canonicalInquiryTransitionChanges(changes []InquiryTransitionChange) []InquiryTransitionChange {
	result := append([]InquiryTransitionChange(nil), changes...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind == result[j].Kind {
			return result[i].EntityID < result[j].EntityID
		}
		return result[i].Kind < result[j].Kind
	})
	return result
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
