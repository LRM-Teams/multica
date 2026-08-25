package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

type v6OpenIntegrationDiscussionActionPayload struct {
	Inputs     []V6NodeRef   `json:"inputs"`
	BranchRefs []V6BranchRef `json:"branch_refs"`
}

func (s *PostgresStore) executeV6OpenIntegrationDiscussionAction(
	ctx context.Context,
	proposal v6DirectorProposal,
	action v6DirectorAction,
	goalVersion int,
	expectedStateVersion int64,
) error {
	if action.PayloadSchema != "integration.create.v1" {
		return fmt.Errorf("%w: create_integration requires integration.create.v1", ErrInvalidContract)
	}
	var payload v6OpenIntegrationDiscussionActionPayload
	if json.Unmarshal(action.Payload, &payload) != nil || len(payload.Inputs) < 2 || len(payload.BranchRefs) == 0 {
		return ErrInvalidContract
	}
	inputs := append([]V6NodeRef(nil), payload.Inputs...)
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].VersionID < inputs[j].VersionID })
	branches := append([]V6BranchRef(nil), payload.BranchRefs...)
	sort.Slice(branches, func(i, j int) bool { return branches[i].ID < branches[j].ID })
	if err := validateV6IntegrationCandidate(inputs); err != nil {
		return err
	}
	var liveStateVersion int64
	if err := s.pool.QueryRow(ctx, `SELECT state_version FROM research_session
		WHERE workspace_id=$1::uuid AND id=$2::uuid AND status='running' AND goal_version=$3`,
		proposal.WorkspaceID, proposal.RunID, goalVersion).Scan(&liveStateVersion); err != nil {
		return err
	}
	if liveStateVersion != expectedStateVersion {
		return ErrWorkItemChanged
	}
	inputHash := v6InputSetHash(inputs)
	branchHash := v6BranchScopeHash(branches)
	scopeRaw, err := marshalV6CanonicalJSON(map[string]any{
		"branch_scope_hash": branchHash,
		"goal_version":      goalVersion,
		"input_set_hash":    inputHash,
		"kind":              "integration",
	})
	if err != nil {
		return err
	}
	_, err = (discussionV6Module{store: s}).Open(ctx, OpenV6DiscussionInput{
		WorkspaceID:          proposal.WorkspaceID,
		RunID:                proposal.RunID,
		Kind:                 "integration",
		ScopeHash:            ArtifactContentHashFromCanonicalJSON(scopeRaw),
		InputSetHash:         inputHash,
		BranchScopeHash:      branchHash,
		GoalVersion:          goalVersion,
		ThroughEventSequence: proposal.ThroughEventSequence,
		DirectorAssignmentID: proposal.DirectorAssignmentID,
		Inputs:               inputs,
		BranchRefs:           branches,
	})
	if err != nil {
		return err
	}
	return nil
}

func validateV6IntegrationCandidate(inputs []V6NodeRef) error {
	seen := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		if _, exists := seen[input.VersionID]; exists {
			return ErrV6InvalidTierTransition
		}
		seen[input.VersionID] = struct{}{}
	}
	for _, mode := range []string{"promotion", "assimilation", "xxl_merge"} {
		for _, output := range []V6Tier{V6TierM, V6TierL, V6TierXL, V6TierXXL} {
			if validateV6IntegrationTiers(mode, inputs, output) == nil {
				return nil
			}
		}
	}
	return ErrV6InvalidTierTransition
}
