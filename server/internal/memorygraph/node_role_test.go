// SPDX-License-Identifier: Apache-2.0

package memorygraph

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNodeRolesAreClosedAndLegacyCompatible(t *testing.T) {
	for _, role := range []NodeRole{
		"", NodeRoleMemory, NodeRoleEntity, NodeRoleDaily, NodeRoleTopic,
		NodeRoleProfile, NodeRoleSkill, NodeRolePattern,
	} {
		assert.True(t, ValidNodeRole(role), role)
	}
	assert.False(t, ValidNodeRole("summary"))
	assert.Equal(t, NodeRoleMemory, EffectiveNodeRole(""))
	assert.Equal(t, NodeRoleSkill, EffectiveNodeRole(NodeRoleSkill))
}

func TestGraphValidateRejectsInvalidNodeRoleAndDerivedAtomKind(t *testing.T) {
	roleGraph := newGraph()
	require.NoError(t, roleGraph.AddNode(&Node{NodeID: "bad-role", Role: "summary"}))
	require.ErrorContains(t, roleGraph.Validate(), `node bad-role has invalid role "summary"`)

	kindGraph := newGraph()
	require.NoError(t, kindGraph.AddNode(&Node{
		NodeID:           "bad-kind",
		Role:             NodeRoleMemory,
		DerivedAtomKinds: []AtomKind{AtomFact, "procedure"},
	}))
	require.ErrorContains(t, kindGraph.Validate(), `node bad-kind has invalid derived atom kind "procedure"`)

	legacyGraph := newGraph()
	require.NoError(t, legacyGraph.AddNode(&Node{NodeID: "legacy", DerivedAtomKinds: []AtomKind{AtomFact, AtomDecision}}))
	require.NoError(t, legacyGraph.Validate())
}
