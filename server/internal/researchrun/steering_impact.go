package researchrun

import (
	"fmt"
	"sort"
	"strings"
)

type SteeringInquiryRef struct {
	Kind InquiryEntityKind `json:"kind"`
	ID   string            `json:"id"`
}

type SteeringBranch struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id,omitempty"`
}

type SteeringInquiryEdge struct {
	From     SteeringInquiryRef `json:"from"`
	To       SteeringInquiryRef `json:"to"`
	Relation InquiryRelation    `json:"relation"`
}

type SteeringTaskScope struct {
	ID        string               `json:"id"`
	Targets   []SteeringInquiryRef `json:"targets"`
	DependsOn []string             `json:"depends_on,omitempty"`
}

type SteeringImpactInput struct {
	Entities      []SteeringInquiryRef  `json:"entities"`
	Branches      []SteeringBranch      `json:"branches"`
	Edges         []SteeringInquiryEdge `json:"edges"`
	Tasks         []SteeringTaskScope   `json:"tasks"`
	AffectedRoots []SteeringInquiryRef  `json:"affected_roots"`
}

type SteeringImpact struct {
	AffectedEntities []SteeringInquiryRef `json:"affected_entities"`
	AffectedBranches []string             `json:"affected_branches"`
	AffectedTasks    []string             `json:"affected_tasks"`
	RetainedTasks    []string             `json:"retained_tasks"`
}

// ComputeSteeringImpact returns the minimal structural invalidation closure.
// Semantic association edges (tests/explains/competes_with/invalidates/
// motivates) remain facts and do not by themselves authorize destructive
// propagation.
func ComputeSteeringImpact(in SteeringImpactInput) (SteeringImpact, error) {
	known := make(map[string]SteeringInquiryRef, len(in.Entities)+len(in.Branches))
	addKnown := func(ref SteeringInquiryRef) error {
		key, err := steeringRefKey(ref)
		if err != nil {
			return err
		}
		if _, exists := known[key]; exists {
			return fmt.Errorf("%w: duplicate steering entity %s", ErrInvalidContract, key)
		}
		known[key] = ref
		return nil
	}
	for _, entity := range in.Entities {
		if err := addKnown(entity); err != nil {
			return SteeringImpact{}, err
		}
	}
	branchParents := make(map[string]string, len(in.Branches))
	branchSeen := make(map[string]bool, len(in.Branches))
	for _, branch := range in.Branches {
		if strings.TrimSpace(branch.ID) == "" || branchSeen[branch.ID] {
			return SteeringImpact{}, fmt.Errorf("%w: duplicate or empty steering branch", ErrInvalidContract)
		}
		branchSeen[branch.ID] = true
		key := string(InquiryKindBranch) + ":" + branch.ID
		if _, exists := known[key]; !exists {
			known[key] = SteeringInquiryRef{Kind: InquiryKindBranch, ID: branch.ID}
		}
		branchParents[branch.ID] = branch.ParentID
	}
	for _, entity := range known {
		if entity.Kind == InquiryKindBranch && !branchSeen[entity.ID] {
			return SteeringImpact{}, fmt.Errorf("%w: steering Branch %s lacks parent metadata", ErrInvalidContract, entity.ID)
		}
	}
	for id, parent := range branchParents {
		if parent != "" {
			if _, ok := branchParents[parent]; !ok {
				return SteeringImpact{}, fmt.Errorf("%w: branch %s has missing parent %s", ErrInvalidContract, id, parent)
			}
		}
	}
	if !steeringParentGraphAcyclic(branchParents) {
		return SteeringImpact{}, fmt.Errorf("%w: steering Branch parent cycle", ErrInvalidContract)
	}
	impacted := map[string]bool{}
	for _, root := range in.AffectedRoots {
		key, err := steeringRefKey(root)
		if err != nil {
			return SteeringImpact{}, err
		}
		if _, ok := known[key]; !ok {
			return SteeringImpact{}, fmt.Errorf("%w: affected root %s is unknown", ErrInvalidContract, key)
		}
		impacted[key] = true
	}
	if len(impacted) == 0 {
		return SteeringImpact{}, fmt.Errorf("%w: steering requires at least one affected root", ErrInvalidContract)
	}
	for _, edge := range in.Edges {
		fromKey, err := steeringRefKey(edge.From)
		if err != nil {
			return SteeringImpact{}, err
		}
		toKey, err := steeringRefKey(edge.To)
		if err != nil {
			return SteeringImpact{}, err
		}
		if _, ok := known[fromKey]; !ok {
			return SteeringImpact{}, fmt.Errorf("%w: steering edge source %s is unknown", ErrInvalidContract, fromKey)
		}
		if _, ok := known[toKey]; !ok {
			return SteeringImpact{}, fmt.Errorf("%w: steering edge target %s is unknown", ErrInvalidContract, toKey)
		}
		if !inquiryRelations[edge.Relation] || fromKey == toKey {
			return SteeringImpact{}, fmt.Errorf("%w: invalid steering edge", ErrInvalidContract)
		}
	}
	changed := true
	for changed {
		changed = false
		for child, parent := range branchParents {
			if parent != "" && impacted[string(InquiryKindBranch)+":"+parent] && !impacted[string(InquiryKindBranch)+":"+child] {
				impacted[string(InquiryKindBranch)+":"+child] = true
				changed = true
			}
		}
		for _, edge := range in.Edges {
			from := mustSteeringRefKey(edge.From)
			to := mustSteeringRefKey(edge.To)
			switch edge.Relation {
			case InquiryRelationDecomposes, InquiryRelationRefines:
				if impacted[from] && !impacted[to] {
					impacted[to] = true
					changed = true
				}
			case InquiryRelationDependsOn:
				if impacted[to] && !impacted[from] {
					impacted[from] = true
					changed = true
				}
			}
		}
	}
	taskIDs := map[string]bool{}
	affectedTasks := map[string]bool{}
	for _, task := range in.Tasks {
		if strings.TrimSpace(task.ID) == "" || taskIDs[task.ID] {
			return SteeringImpact{}, fmt.Errorf("%w: duplicate or empty steering Task", ErrInvalidContract)
		}
		taskIDs[task.ID] = true
		if len(task.Targets) == 0 {
			return SteeringImpact{}, fmt.Errorf("%w: steering Task %s has no Inquiry target", ErrInvalidContract, task.ID)
		}
		for _, target := range task.Targets {
			key, err := steeringRefKey(target)
			if err != nil {
				return SteeringImpact{}, err
			}
			if _, ok := known[key]; !ok {
				return SteeringImpact{}, fmt.Errorf("%w: Task %s target %s is unknown", ErrInvalidContract, task.ID, key)
			}
			if impacted[key] {
				affectedTasks[task.ID] = true
			}
		}
	}
	for _, task := range in.Tasks {
		seen := map[string]bool{}
		for _, dependency := range task.DependsOn {
			if !taskIDs[dependency] || dependency == task.ID || seen[dependency] {
				return SteeringImpact{}, fmt.Errorf("%w: Task %s dependency %s is invalid", ErrInvalidContract, task.ID, dependency)
			}
			seen[dependency] = true
		}
	}
	if !steeringTaskGraphAcyclic(in.Tasks) {
		return SteeringImpact{}, fmt.Errorf("%w: steering Task dependency cycle", ErrInvalidContract)
	}
	changed = true
	for changed {
		changed = false
		for _, task := range in.Tasks {
			if affectedTasks[task.ID] {
				continue
			}
			for _, dependency := range task.DependsOn {
				if affectedTasks[dependency] {
					affectedTasks[task.ID] = true
					changed = true
					break
				}
			}
		}
	}
	result := SteeringImpact{}
	for key, ref := range known {
		if impacted[key] {
			result.AffectedEntities = append(result.AffectedEntities, ref)
			if ref.Kind == InquiryKindBranch {
				result.AffectedBranches = append(result.AffectedBranches, ref.ID)
			}
		}
	}
	for id := range taskIDs {
		if affectedTasks[id] {
			result.AffectedTasks = append(result.AffectedTasks, id)
		} else {
			result.RetainedTasks = append(result.RetainedTasks, id)
		}
	}
	sort.Slice(result.AffectedEntities, func(i, j int) bool {
		if result.AffectedEntities[i].Kind == result.AffectedEntities[j].Kind {
			return result.AffectedEntities[i].ID < result.AffectedEntities[j].ID
		}
		return steeringKindRank(result.AffectedEntities[i].Kind) < steeringKindRank(result.AffectedEntities[j].Kind)
	})
	sort.Strings(result.AffectedBranches)
	sort.Strings(result.AffectedTasks)
	sort.Strings(result.RetainedTasks)
	return result, nil
}

func steeringRefKey(ref SteeringInquiryRef) (string, error) {
	if !inquiryKinds[ref.Kind] || strings.TrimSpace(ref.ID) == "" {
		return "", fmt.Errorf("%w: invalid steering Inquiry reference", ErrInvalidContract)
	}
	return string(ref.Kind) + ":" + ref.ID, nil
}
func mustSteeringRefKey(ref SteeringInquiryRef) string { return string(ref.Kind) + ":" + ref.ID }

func steeringKindRank(kind InquiryEntityKind) int {
	order := []InquiryEntityKind{InquiryKindQuestion, InquiryKindHypothesis, InquiryKindBranch, InquiryKindClaim, InquiryKindInsight, InquiryKindDispute}
	for index, value := range order {
		if value == kind {
			return index
		}
	}
	return 1 << 30
}

func steeringParentGraphAcyclic(parents map[string]string) bool {
	for key := range parents {
		seen := map[string]bool{}
		for current := key; current != ""; current = parents[current] {
			if seen[current] {
				return false
			}
			seen[current] = true
		}
	}
	return true
}

func steeringTaskGraphAcyclic(tasks []SteeringTaskScope) bool {
	dependencies := make(map[string][]string, len(tasks))
	for _, task := range tasks {
		dependencies[task.ID] = task.DependsOn
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(key string) bool {
		if visiting[key] {
			return false
		}
		if visited[key] {
			return true
		}
		visiting[key] = true
		for _, dependency := range dependencies[key] {
			if !visit(dependency) {
				return false
			}
		}
		delete(visiting, key)
		visited[key] = true
		return true
	}
	for key := range dependencies {
		if !visit(key) {
			return false
		}
	}
	return true
}
