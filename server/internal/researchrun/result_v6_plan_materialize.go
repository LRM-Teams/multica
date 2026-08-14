package researchrun

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// researchV6PlanMaterialization is the persistence-neutral command produced
// from one accepted Planner result. It resolves every Agent-authored client
// key before the Postgres transaction writes any row.
type researchV6PlanMaterialization struct {
	Result       ResultEnvelope
	InquiryGraph CreateInquiryGraphInput
	Targets      []TaskInquiryTargetIntent
}

// TaskInquiryTargetIntent keeps the Task client key until materializeTasks has
// assigned its canonical database identity. EntityID is already resolved.
type TaskInquiryTargetIntent struct {
	TaskClientKey string
	Kind          InquiryEntityKind
	EntityKey     string
	EntityID      string
}

func prepareResearchV6PlanMaterialization(sessionID, attemptID, agentID string, stateVersion int64, plan ResearchV6PlanResult) (researchV6PlanMaterialization, error) {
	namespace, err := uuid.Parse(sessionID)
	if err != nil {
		return researchV6PlanMaterialization{}, fmt.Errorf("%w: invalid V6 plan session identity", ErrInvalidContract)
	}
	identity := func(kind, key string) string {
		return uuid.NewSHA1(namespace, []byte("research-run-v6/"+kind+"/"+key)).String()
	}

	result := researchV6PlanEnvelope(plan)
	questionIDs := make(map[string]string, len(plan.Questions))
	for _, item := range plan.Questions {
		questionIDs[item.ClientKey] = identity("question", item.ClientKey)
	}

	entityIDs := map[InquiryEntityKind]map[string]string{
		InquiryKindQuestion:   questionIDs,
		InquiryKindHypothesis: {},
		InquiryKindBranch:     {},
	}
	graph := CreateInquiryGraphInput{SessionID: sessionID, AttemptID: attemptID, AgentID: agentID, ExpectedStateVersion: stateVersion}
	materializationTargets := make([]TaskInquiryTargetIntent, 0)
	for _, item := range plan.Hypotheses {
		id := identity("hypothesis", item.ClientKey)
		entityIDs[InquiryKindHypothesis][item.ClientKey] = id
		applicability, _ := json.Marshal(item.Applicability)
		expected, _ := json.Marshal(item.ExpectedObservations)
		weakening, _ := json.Marshal(item.WeakeningConditions)
		graph.Hypotheses = append(graph.Hypotheses, InquiryHypothesisInput{
			ID: id, ClientKey: item.ClientKey, QuestionID: questionIDs[item.QuestionKey], Statement: item.Statement,
			Applicability: applicability, ExpectedObservations: expected, WeakeningConditions: weakening,
			ConfidenceLow: item.ConfidenceLow, ConfidenceHigh: item.ConfidenceHigh,
		})
	}
	for _, item := range orderedResearchV6Branches(plan.Branches) {
		entityIDs[InquiryKindBranch][item.ClientKey] = identity("branch", item.ClientKey)
	}
	for _, item := range orderedResearchV6Branches(plan.Branches) {
		entry, _ := json.Marshal(item.EntryConditions)
		exit, _ := json.Marshal(item.ExitConditions)
		graph.Branches = append(graph.Branches, InquiryBranchInput{
			ID: entityIDs[InquiryKindBranch][item.ClientKey], ClientKey: item.ClientKey, ParentBranchID: entityIDs[InquiryKindBranch][item.ParentBranchKey],
			Objective: item.Objective, EntryConditions: entry, ExitConditions: exit, BudgetShare: *item.BudgetShare,
		})
	}
	resolve := func(ref ResearchV6EntityRef) (InquiryEntityKind, string, error) {
		kind := InquiryEntityKind(ref.Kind)
		ids, ok := entityIDs[kind]
		if !ok || ids[ref.Key] == "" {
			return "", "", fmt.Errorf("%w: unresolved V6 plan target %s:%s", ErrInvalidResult, ref.Kind, ref.Key)
		}
		return kind, ids[ref.Key], nil
	}
	for _, item := range plan.InquiryEdges {
		fromKind, fromID, resolveErr := resolve(item.From)
		if resolveErr != nil {
			return researchV6PlanMaterialization{}, resolveErr
		}
		toKind, toID, resolveErr := resolve(item.To)
		if resolveErr != nil {
			return researchV6PlanMaterialization{}, resolveErr
		}
		graph.Edges = append(graph.Edges, InquiryEdgeInput{
			ID: identity("inquiry_edge", item.ClientKey), ClientKey: item.ClientKey, From: InquiryEndpoint{Kind: fromKind, ID: fromID},
			To: InquiryEndpoint{Kind: toKind, ID: toID}, Relation: InquiryRelation(item.Relation), Rationale: item.Rationale,
		})
	}

	for _, item := range plan.Tasks {
		criteria, marshalErr := json.Marshal(map[string]any{"task": item.AcceptanceCriteria, "search_plans": searchPlansForTask(item, plan.SearchPlans)})
		if marshalErr != nil {
			return researchV6PlanMaterialization{}, marshalErr
		}
		maxAttempts, timeout := 0, 0
		if item.MaxAttempts != nil {
			maxAttempts = *item.MaxAttempts
		}
		if item.TimeoutSeconds != nil {
			timeout = *item.TimeoutSeconds
		}
		questionKey := ""
		for _, target := range item.Targets {
			kind, entityID, resolveErr := resolve(target)
			if resolveErr != nil {
				return researchV6PlanMaterialization{}, resolveErr
			}
			if kind == InquiryKindQuestion && questionKey == "" {
				questionKey = target.Key
			}
			// Preserve Planner order; the target ledger records this order.
			// Duplicate targets were rejected by the strict V6 boundary.
			materializationTargets = append(materializationTargets, TaskInquiryTargetIntent{TaskClientKey: item.ClientKey, Kind: kind, EntityKey: target.Key, EntityID: entityID})
		}
		for index := range result.Plan.Tasks {
			if result.Plan.Tasks[index].ClientKey == item.ClientKey {
				result.Plan.Tasks[index].QuestionKey = questionKey
				result.Plan.Tasks[index].AcceptanceCriteria = criteria
				result.Plan.Tasks[index].MaxAttempts = maxAttempts
				result.Plan.Tasks[index].TimeoutSeconds = timeout
			}
		}
	}
	return researchV6PlanMaterialization{Result: result, InquiryGraph: graph, Targets: materializationTargets}, nil
}

func orderedResearchV6Branches(branches []ResearchV6Branch) []ResearchV6Branch {
	pending := append([]ResearchV6Branch(nil), branches...)
	ordered := make([]ResearchV6Branch, 0, len(branches))
	created := map[string]bool{}
	for len(pending) > 0 {
		for index := 0; index < len(pending); {
			if pending[index].ParentBranchKey != "" && !created[pending[index].ParentBranchKey] {
				index++
				continue
			}
			created[pending[index].ClientKey] = true
			ordered = append(ordered, pending[index])
			pending = append(pending[:index], pending[index+1:]...)
		}
	}
	return ordered
}

func researchV6PlanEnvelope(plan ResearchV6PlanResult) ResultEnvelope {
	result := ResultEnvelope{SchemaVersion: 6, ClientRequestID: plan.ClientRequestID, Summary: plan.Summary, Plan: &PlanProposal{}}
	for _, item := range plan.Questions {
		result.Plan.Questions = append(result.Plan.Questions, QuestionProposal{ClientKey: item.ClientKey, ParentClientKey: item.ParentClientKey,
			Kind: QuestionKind(item.Kind), Text: item.Text, Required: *item.Required, Priority: *item.Priority,
			Impact: *item.Impact, Uncertainty: *item.Uncertainty, Novelty: *item.Novelty})
	}
	for _, item := range plan.Tasks {
		maxAttempts, timeout := 0, 0
		if item.MaxAttempts != nil {
			maxAttempts = *item.MaxAttempts
		}
		if item.TimeoutSeconds != nil {
			timeout = *item.TimeoutSeconds
		}
		criteria, _ := json.Marshal(item.AcceptanceCriteria)
		result.Plan.Tasks = append(result.Plan.Tasks, TaskProposal{ClientKey: item.ClientKey, Kind: TaskKind(item.Kind), Objective: item.Objective,
			RequiredCapability: item.RequiredCapability, ExpectedResult: item.ExpectedResult, AcceptanceCriteria: criteria,
			Priority: *item.Priority, DependsOn: item.DependsOn, MaxAttempts: maxAttempts, TimeoutSeconds: timeout})
	}
	return result
}

func searchPlansForTask(task ResearchV6PlanTask, plans []ResearchV6SearchPlan) []ResearchV6SearchPlan {
	wanted := map[ResearchV6EntityRef]bool{}
	for _, target := range task.Targets {
		wanted[target] = true
	}
	var result []ResearchV6SearchPlan
	for _, plan := range plans {
		for _, target := range plan.Targets {
			if wanted[target] {
				result = append(result, plan)
				break
			}
		}
	}
	return result
}
