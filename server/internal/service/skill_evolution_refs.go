// SPDX-License-Identifier: Apache-2.0

package service

import (
	"errors"
	"fmt"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/skillevolution"
)

// MemoryPurpose names the canonical memory usage purpose of an operation
// (spec §12.5). It is derived server-side from capability and plan state;
// client request fields never set it, and client filters can only narrow
// the corpus the purpose already authorizes.
type MemoryPurpose string

const (
	MemoryPurposeTaskRecall      MemoryPurpose = "task_recall"
	MemoryPurposeSkillEvolution  MemoryPurpose = "skill_evolution"
	MemoryPurposeEvaluationAudit MemoryPurpose = "evaluation_audit"
	MemoryPurposeCuratorReview   MemoryPurpose = "curator_review"
)

// ErrSkillEvolutionRefUnauthorized is the fail-closed signal of the internal
// evolution-ref resolver.
var ErrSkillEvolutionRefUnauthorized = errors.New("skill evolution ref not authorized")

// ResolvedSkillEvolutionRef is the authorized projection outcome of one
// internal evolution ref. It deliberately never converts into a public
// memorygraph.MemoryRef.
type ResolvedSkillEvolutionRef struct {
	Ref    skillevolution.SkillEvolutionRef
	NodeID string
}

// SkillEvolutionRefResolver resolves internal capability-scoped evolution
// refs (spec §12.3) against the evolution plane's graph projection. It is
// deliberately separate from the public MemoryRef path: evolution refs never
// resolve through task-recall APIs, and task-recall refs never resolve here.
type SkillEvolutionRefResolver struct {
	store       *memorygraph.Store
	workspaceID string
}

// NewSkillEvolutionRefResolver binds the resolver to one workspace's
// evolution-plane projection store. The store is provisioned by the Phase 2
// evolution ledger; until then only pattern projections are resolvable.
func NewSkillEvolutionRefResolver(store *memorygraph.Store, workspaceID string) *SkillEvolutionRefResolver {
	return &SkillEvolutionRefResolver{store: store, workspaceID: workspaceID}
}

// Resolve authorizes one internal ref under a server-derived purpose.
// task_recall and evaluation_audit never read the pattern plane (plane
// separation), and the ledger-backed kinds fail closed until the Phase 2
// evolution ledger is provisioned — they have no durable authority to
// resolve against yet.
func (r *SkillEvolutionRefResolver) Resolve(ref skillevolution.SkillEvolutionRef, purpose MemoryPurpose) (ResolvedSkillEvolutionRef, error) {
	if r == nil || r.store == nil {
		return ResolvedSkillEvolutionRef{}, errors.New("skill evolution ref resolver not configured")
	}
	if err := ref.Validate(); err != nil {
		return ResolvedSkillEvolutionRef{}, fmt.Errorf("%w: %v", ErrSkillEvolutionRefUnauthorized, err)
	}
	if ref.WorkspaceID != r.workspaceID {
		return ResolvedSkillEvolutionRef{}, fmt.Errorf(
			"%w: ref workspace is outside this resolver", ErrSkillEvolutionRefUnauthorized)
	}
	if ref.Kind != skillevolution.RefPattern {
		return ResolvedSkillEvolutionRef{}, fmt.Errorf(
			"%w: kind %q awaits the evolution ledger", ErrSkillEvolutionRefUnauthorized, ref.Kind)
	}
	switch purpose {
	case MemoryPurposeSkillEvolution, MemoryPurposeCuratorReview:
	default:
		return ResolvedSkillEvolutionRef{}, fmt.Errorf(
			"%w: purpose %q cannot read the pattern plane", ErrSkillEvolutionRefUnauthorized, purpose)
	}
	version, err := r.store.CurrentVersion()
	if err != nil {
		return ResolvedSkillEvolutionRef{}, fmt.Errorf("%w: projection store: %v", ErrSkillEvolutionRefUnauthorized, err)
	}
	g, err := memorygraph.LoadGraph(r.store, version)
	if err != nil {
		return ResolvedSkillEvolutionRef{}, fmt.Errorf("%w: projection graph: %v", ErrSkillEvolutionRefUnauthorized, err)
	}
	node := g.Node(ref.ID)
	if node == nil || memorygraph.EffectiveNodeRole(node.Role) != memorygraph.NodeRolePattern {
		return ResolvedSkillEvolutionRef{}, fmt.Errorf(
			"%w: %s is not a pattern projection", ErrSkillEvolutionRefUnauthorized, ref.ID)
	}
	return ResolvedSkillEvolutionRef{Ref: ref, NodeID: node.NodeID}, nil
}
