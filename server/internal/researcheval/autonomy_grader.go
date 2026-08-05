package researcheval

import (
	"context"
	"fmt"
)

type AutonomyGrader struct{}

func (AutonomyGrader) Name() string { return "autonomy_graph_v1" }

func (AutonomyGrader) Grade(_ context.Context, evaluationCase Case, artifact Artifact) (Grade, error) {
	oracle := evaluationCase.Oracle.Autonomy
	if oracle == nil {
		return Grade{Score: 1, Passed: true}, nil
	}
	total := 0
	earned := 0
	findings := []Finding{}
	for _, expected := range oracle.RequiredActions {
		total++
		if hasMatchingAction(artifact.Actions, expected) {
			earned++
			continue
		}
		findings = append(findings, Finding{Code: "required_action_missing", Message: fmt.Sprintf("required action %q by %q on %q is missing", expected.Kind, expected.Actor, expected.Target)})
	}
	for _, forbidden := range oracle.ForbiddenActions {
		total++
		if !hasMatchingAction(artifact.Actions, forbidden) {
			earned++
			continue
		}
		findings = append(findings, Finding{Code: "forbidden_action_observed", Message: fmt.Sprintf("forbidden action %q by %q on %q was observed", forbidden.Kind, forbidden.Actor, forbidden.Target)})
	}
	nodesByKey := map[string]ArtifactGraphNode{}
	nodeKeyByID := map[string]string{}
	for _, node := range artifact.GraphNodes {
		nodesByKey[node.Key] = node
		nodeKeyByID[node.ID] = node.Key
	}
	for _, expected := range oracle.RequiredNodes {
		total++
		actual, exists := nodesByKey[expected.Key]
		if exists && actual.Kind == expected.Kind && actual.Status == expected.Status && actual.Level == expected.Level &&
			(!expected.DetailsComplete || containsAllNonEmptyDetails(actual.Details, RequiredProjectionDetailFields)) {
			earned++
			continue
		}
		findings = append(findings, Finding{Code: "required_graph_node_missing", Message: fmt.Sprintf("graph node %q does not match kind/status/level/detail contract", expected.Key)})
	}
	edges := map[string]struct{}{}
	for _, edge := range artifact.GraphEdges {
		key := nodeKeyByID[edge.FromNodeID] + "\x00" + nodeKeyByID[edge.ToNodeID] + "\x00" + edge.Type
		edges[key] = struct{}{}
	}
	for _, expected := range oracle.RequiredEdges {
		total++
		key := expected.FromKey + "\x00" + expected.ToKey + "\x00" + expected.Type
		if _, exists := edges[key]; exists {
			earned++
			continue
		}
		findings = append(findings, Finding{Code: "required_graph_edge_missing", Message: fmt.Sprintf("graph edge %q -> %q (%s) is missing", expected.FromKey, expected.ToKey, expected.Type)})
	}
	if expected := oracle.Projection; expected != nil {
		actual := artifact.Projection
		if expected.RequireHashMatch {
			total++
			if actual != nil && actual.SnapshotHash != "" && actual.SnapshotHash == actual.ReplayHash {
				earned++
			} else {
				findings = append(findings, Finding{Code: "projection_hash_mismatch", Message: "snapshot and replay projection hashes differ"})
			}
		}
		if expected.RequireGapResync {
			total++
			if actual != nil && actual.GapDetected && actual.ResyncRequested {
				earned++
			} else {
				findings = append(findings, Finding{Code: "projection_gap_not_resynced", Message: "event sequence gap did not request a snapshot resync"})
			}
		}
		if expected.RequireUniqueNodes {
			total++
			if actual != nil && uniqueStrings(actual.ObservedNodeIDs) {
				earned++
			} else {
				findings = append(findings, Finding{Code: "projection_duplicate_node", Message: "projection contains duplicate or empty node identities"})
			}
		}
		if expected.MinimumTotalNodes > 0 {
			total++
			if actual != nil && actual.TotalNodes >= expected.MinimumTotalNodes && len(actual.ObservedNodeIDs) == actual.TotalNodes {
				earned++
			} else {
				findings = append(findings, Finding{Code: "projection_scale_missing", Message: fmt.Sprintf("projection did not reconstruct at least %d nodes", expected.MinimumTotalNodes)})
			}
		}
		if expected.MaximumPageNodes > 0 {
			total++
			if actual != nil && actual.LargestPageNodes > 0 && actual.LargestPageNodes <= expected.MaximumPageNodes {
				earned++
			} else {
				findings = append(findings, Finding{Code: "projection_page_unbounded", Message: fmt.Sprintf("projection page exceeded %d nodes", expected.MaximumPageNodes)})
			}
		}
	}
	return scoredGrade(earned, total, findings), nil
}

func hasMatchingAction(actions []ArtifactAction, expected ExpectedAction) bool {
	for _, action := range actions {
		if action.Kind != expected.Kind {
			continue
		}
		if expected.Actor != "" && action.Actor != expected.Actor {
			continue
		}
		if expected.Target != "" && action.Target != expected.Target {
			continue
		}
		if expected.Outcome != "" && action.Outcome != expected.Outcome {
			continue
		}
		return true
	}
	return false
}

func uniqueStrings(values []string) bool {
	if len(values) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func containsAllNonEmptyDetails(details map[string]string, required []string) bool {
	for _, key := range required {
		if details[key] == "" {
			return false
		}
	}
	return true
}
