package researchrun

import (
	"encoding/json"
	"fmt"
	"sort"
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

type InquiryHypothesisInput struct {
	ID                   string          `json:"id"`
	ClientKey            string          `json:"client_key,omitempty"`
	QuestionID           string          `json:"question_id"`
	Statement            string          `json:"statement"`
	Applicability        json.RawMessage `json:"applicability"`
	ExpectedObservations json.RawMessage `json:"expected_observations"`
	WeakeningConditions  json.RawMessage `json:"weakening_conditions"`
	ConfidenceLow        *float64        `json:"confidence_low,omitempty"`
	ConfidenceHigh       *float64        `json:"confidence_high,omitempty"`
}

type InquiryBranchInput struct {
	ID              string          `json:"id"`
	ClientKey       string          `json:"client_key,omitempty"`
	ParentBranchID  string          `json:"parent_branch_id,omitempty"`
	Objective       string          `json:"objective"`
	EntryConditions json.RawMessage `json:"entry_conditions"`
	ExitConditions  json.RawMessage `json:"exit_conditions"`
	BudgetShare     float64         `json:"budget_share"`
}

type InquiryInsightInput struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Summary    string  `json:"summary"`
	Importance float64 `json:"importance"`
	Level      int     `json:"level"`
}

type InquiryEdgeInput struct {
	ID        string          `json:"id"`
	ClientKey string          `json:"client_key,omitempty"`
	From      InquiryEndpoint `json:"from"`
	To        InquiryEndpoint `json:"to"`
	Relation  InquiryRelation `json:"relation"`
	Rationale string          `json:"rationale,omitempty"`
}

type InquiryEndpoint struct {
	Kind InquiryEntityKind `json:"kind"`
	ID   string            `json:"id"`
}

type CreateInquiryGraphInput struct {
	WorkspaceID          string                   `json:"-"`
	SessionID            string                   `json:"-"`
	AttemptID            string                   `json:"attempt_id"`
	AgentID              string                   `json:"agent_id"`
	IdempotencyKey       string                   `json:"idempotency_key"`
	ExpectedStateVersion int64                    `json:"expected_state_version"`
	Hypotheses           []InquiryHypothesisInput `json:"hypotheses"`
	Branches             []InquiryBranchInput     `json:"branches"`
	Insights             []InquiryInsightInput    `json:"insights"`
	Edges                []InquiryEdgeInput       `json:"edges"`
}

type InquiryGraphCreateResult struct {
	Event RunEvent `json:"event"`
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

func (module inquiryModule) ValidateCreate(in CreateInquiryGraphInput) error {
	if strings.TrimSpace(in.WorkspaceID) == "" || strings.TrimSpace(in.SessionID) == "" ||
		strings.TrimSpace(in.AttemptID) == "" || strings.TrimSpace(in.AgentID) == "" ||
		strings.TrimSpace(in.IdempotencyKey) == "" || len(in.IdempotencyKey) > 512 || in.ExpectedStateVersion < 1 {
		return fmt.Errorf("%w: incomplete inquiry graph identity", ErrInvalidContract)
	}
	if len(in.Hypotheses)+len(in.Branches)+len(in.Insights)+len(in.Edges) == 0 {
		return fmt.Errorf("%w: inquiry graph mutation is empty", ErrInvalidContract)
	}
	ids := make(map[string]struct{})
	claimID := func(id string) error {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("%w: inquiry entity id is empty", ErrInvalidContract)
		}
		if _, exists := ids[id]; exists {
			return fmt.Errorf("%w: duplicate inquiry entity id %q", ErrInvalidContract, id)
		}
		ids[id] = struct{}{}
		return nil
	}
	for _, item := range in.Hypotheses {
		if err := claimID(item.ID); err != nil {
			return err
		}
		if strings.TrimSpace(item.QuestionID) == "" || strings.TrimSpace(item.Statement) == "" || len(item.Statement) > 32768 ||
			!validJSONObject(item.Applicability) || !validJSONArray(item.ExpectedObservations) || !validJSONArray(item.WeakeningConditions) ||
			!validConfidenceRange(item.ConfidenceLow, item.ConfidenceHigh) {
			return fmt.Errorf("%w: invalid hypothesis %q", ErrInvalidContract, item.ID)
		}
	}
	for _, item := range in.Branches {
		if err := claimID(item.ID); err != nil {
			return err
		}
		if strings.TrimSpace(item.Objective) == "" || len(item.Objective) > 32768 || item.BudgetShare < 0 || item.BudgetShare > 1 ||
			!validJSONArray(item.EntryConditions) || !validJSONArray(item.ExitConditions) {
			return fmt.Errorf("%w: invalid branch %q", ErrInvalidContract, item.ID)
		}
	}
	for _, item := range in.Insights {
		if err := claimID(item.ID); err != nil {
			return err
		}
		if strings.TrimSpace(item.Title) == "" || len(item.Title) > 4096 || strings.TrimSpace(item.Summary) == "" || len(item.Summary) > 32768 ||
			item.Importance < 0 || item.Importance > 1 || item.Level < 1 {
			return fmt.Errorf("%w: invalid insight %q", ErrInvalidContract, item.ID)
		}
	}
	for _, item := range in.Edges {
		if err := claimID(item.ID); err != nil {
			return err
		}
		if err := module.ValidateEdge(inquiryEdgeCommand{
			From: inquiryEndpoint{Kind: item.From.Kind, ID: item.From.ID}, To: inquiryEndpoint{Kind: item.To.Kind, ID: item.To.ID},
			Relation: item.Relation, Rationale: item.Rationale,
		}); err != nil {
			return err
		}
	}
	return nil
}

func validJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var value map[string]any
	return json.Unmarshal(raw, &value) == nil && value != nil
}

func validJSONArray(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var value []any
	return json.Unmarshal(raw, &value) == nil && value != nil
}

func validConfidenceRange(low, high *float64) bool {
	if low != nil && (*low < 0 || *low > 1) {
		return false
	}
	if high != nil && (*high < 0 || *high > 1) {
		return false
	}
	return low == nil || high == nil || *low <= *high
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
