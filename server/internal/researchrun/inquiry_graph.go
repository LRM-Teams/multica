package researchrun

import (
	"fmt"

	"github.com/google/uuid"
)

type inquiryResolvedBranch struct {
	ID          string
	ParentID    string
	BudgetShare float64
}

// inquiryResolvedGraph is the persistence-ready Inquiry graph. All client keys
// have already resolved to same-session UUIDs; the module validates cross-row
// invariants that cannot be delegated to a single table CHECK constraint.
type inquiryResolvedGraph struct {
	Entities              []inquiryEndpoint
	Branches              []inquiryResolvedBranch
	Edges                 []inquiryEdgeCommand
	AuthorizedBudgetShare float64
}

func (m inquiryModule) ValidateResolvedGraph(graph inquiryResolvedGraph) error {
	if len(graph.Branches) == 0 {
		return fmt.Errorf("%w: inquiry graph requires a branch", ErrInvalidContract)
	}
	if graph.AuthorizedBudgetShare < 0 || graph.AuthorizedBudgetShare > 1 {
		return fmt.Errorf("%w: invalid authorized branch budget", ErrInvalidContract)
	}
	entities := make(map[inquiryEndpoint]struct{}, len(graph.Entities))
	for _, entity := range graph.Entities {
		if entity.Kind == InquiryKindDispute || !inquiryKinds[entity.Kind] {
			return fmt.Errorf("%w: unsupported resolved entity kind %q", ErrInvalidContract, entity.Kind)
		}
		if _, err := uuid.Parse(entity.ID); err != nil {
			return fmt.Errorf("%w: inquiry graph entity is not resolved", ErrInvalidContract)
		}
		if _, duplicate := entities[entity]; duplicate {
			return fmt.Errorf("%w: duplicate resolved inquiry entity", ErrInvalidContract)
		}
		entities[entity] = struct{}{}
	}

	branches := make(map[string]inquiryResolvedBranch, len(graph.Branches))
	budget := 0.0
	rootCount := 0
	for _, branch := range graph.Branches {
		if _, err := uuid.Parse(branch.ID); err != nil {
			return fmt.Errorf("%w: branch is not resolved", ErrInvalidContract)
		}
		if _, duplicate := branches[branch.ID]; duplicate {
			return fmt.Errorf("%w: duplicate resolved branch", ErrInvalidContract)
		}
		if _, ok := entities[inquiryEndpoint{Kind: InquiryKindBranch, ID: branch.ID}]; !ok {
			return fmt.Errorf("%w: branch is absent from resolved entities", ErrInvalidContract)
		}
		if branch.BudgetShare < 0 || branch.BudgetShare > 1 {
			return fmt.Errorf("%w: invalid branch budget share", ErrInvalidContract)
		}
		if branch.ParentID == "" {
			rootCount++
		} else if _, err := uuid.Parse(branch.ParentID); err != nil || branch.ParentID == branch.ID {
			return fmt.Errorf("%w: invalid branch parent", ErrInvalidContract)
		}
		branches[branch.ID] = branch
		budget += branch.BudgetShare
	}
	if rootCount == 0 {
		return fmt.Errorf("%w: inquiry branch forest has no root", ErrInvalidContract)
	}
	if budget > graph.AuthorizedBudgetShare+1e-9 {
		return fmt.Errorf("%w: branch budget exceeds authorization", ErrInvalidContract)
	}
	for _, branch := range graph.Branches {
		if branch.ParentID != "" {
			if _, ok := branches[branch.ParentID]; !ok {
				return fmt.Errorf("%w: branch references unknown parent", ErrInvalidContract)
			}
		}
	}
	if hasBranchParentCycle(branches) {
		return fmt.Errorf("%w: branch parent cycle", ErrInvalidContract)
	}

	seenEdges := make(map[inquiryEdgeIdentity]struct{}, len(graph.Edges))
	acyclicEdges := make(map[inquiryEndpoint][]inquiryEndpoint)
	for _, edge := range graph.Edges {
		if err := m.ValidateEdge(edge); err != nil {
			return err
		}
		if _, ok := entities[edge.From]; !ok {
			return fmt.Errorf("%w: inquiry edge from endpoint is outside the resolved graph", ErrInvalidContract)
		}
		if _, ok := entities[edge.To]; !ok {
			return fmt.Errorf("%w: inquiry edge to endpoint is outside the resolved graph", ErrInvalidContract)
		}
		identity := inquiryEdgeIdentity{From: edge.From, To: edge.To, Relation: edge.Relation}
		if _, duplicate := seenEdges[identity]; duplicate {
			return fmt.Errorf("%w: duplicate inquiry edge", ErrInvalidContract)
		}
		seenEdges[identity] = struct{}{}
		if inquiryRelationMustBeAcyclic(edge.Relation) {
			acyclicEdges[edge.From] = append(acyclicEdges[edge.From], edge.To)
		}
	}
	if hasInquiryDirectedCycle(entities, acyclicEdges) {
		return fmt.Errorf("%w: inquiry dependency cycle", ErrInvalidContract)
	}
	return nil
}

type inquiryEdgeIdentity struct {
	From     inquiryEndpoint
	To       inquiryEndpoint
	Relation InquiryRelation
}

func hasBranchParentCycle(branches map[string]inquiryResolvedBranch) bool {
	const (
		unseen = iota
		visiting
		done
	)
	state := make(map[string]int, len(branches))
	var visit func(string) bool
	visit = func(id string) bool {
		switch state[id] {
		case visiting:
			return true
		case done:
			return false
		}
		state[id] = visiting
		if parent := branches[id].ParentID; parent != "" && visit(parent) {
			return true
		}
		state[id] = done
		return false
	}
	for id := range branches {
		if visit(id) {
			return true
		}
	}
	return false
}

func hasInquiryDirectedCycle(
	entities map[inquiryEndpoint]struct{},
	edges map[inquiryEndpoint][]inquiryEndpoint,
) bool {
	state := make(map[inquiryEndpoint]uint8, len(entities))
	var visit func(inquiryEndpoint) bool
	visit = func(node inquiryEndpoint) bool {
		if state[node] == 1 {
			return true
		}
		if state[node] == 2 {
			return false
		}
		state[node] = 1
		for _, next := range edges[node] {
			if visit(next) {
				return true
			}
		}
		state[node] = 2
		return false
	}
	for node := range entities {
		if visit(node) {
			return true
		}
	}
	return false
}
