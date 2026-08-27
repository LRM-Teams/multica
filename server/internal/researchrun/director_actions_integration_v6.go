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
	// S-level promotion is an intra-direction consolidation step. Do not let
	// the Director skip the per-direction evidence threshold by promoting one
	// S result from several top-level directions directly into a shared M.
	if allV6InputsAtTier(inputs, V6TierS) {
		if len(inputs) < 3 {
			return fmt.Errorf("%w: S promotion requires at least three nodes from one research direction", ErrV6InvalidTierTransition)
		}
		var topDirectionCount, boundBranchCount int
		if err = s.pool.QueryRow(ctx, `WITH RECURSIVE branch_tree AS (
			SELECT id,id AS top_id
			FROM research_branch
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND parent_branch_id IS NULL
			UNION ALL
			SELECT child.id,branch_tree.top_id
			FROM research_branch child
			JOIN branch_tree ON branch_tree.id=child.parent_branch_id
			WHERE child.workspace_id=$1::uuid AND child.session_id=$2::uuid
		)
		SELECT count(DISTINCT branch_tree.top_id),count(DISTINCT branch_tree.id)
		FROM branch_tree WHERE branch_tree.id=ANY($3::uuid[])`, proposal.WorkspaceID, proposal.RunID, branchIDs(branches)).Scan(&topDirectionCount, &boundBranchCount); err != nil {
			return err
		}
		if boundBranchCount != len(branches) || topDirectionCount != 1 {
			return fmt.Errorf("%w: S promotion must use at least three nodes bound to one top-level research direction", ErrV6InvalidTierTransition)
		}
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
	discussion, err := (discussionV6Module{store: s}).Open(ctx, OpenV6DiscussionInput{
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
	if err = validateV6IntegrationDiscussionOutcome(discussion); err != nil {
		return err
	}
	return nil
}

func allV6InputsAtTier(inputs []V6NodeRef, tier V6Tier) bool {
	if len(inputs) == 0 {
		return false
	}
	for _, input := range inputs {
		if input.Tier != tier {
			return false
		}
	}
	return true
}

func branchIDs(branches []V6BranchRef) []string {
	ids := make([]string, len(branches))
	for i, branch := range branches {
		ids[i] = branch.ID
	}
	return ids
}

func validateV6IntegrationDiscussionOutcome(discussion V6Discussion) error {
	if discussion.Status != "active" {
		return fmt.Errorf("%w: integration inputs already have a terminal discussion; include new evidence before retrying", ErrInvalidContract)
	}
	return nil
}

func validateV6IntegrationCandidate(inputs []V6NodeRef) error {
	if allV6InputsAtTier(inputs, V6TierS) && len(inputs) < 3 {
		return fmt.Errorf("%w: S promotion requires at least three nodes from one research direction", ErrV6InvalidTierTransition)
	}
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
