// SPDX-License-Identifier: Apache-2.0

package memorygraph

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// patternRetrieverFixture builds a graph with one memory node and one
// pattern node sharing no vocabulary overlap with other bodies, so a leak
// of the pattern node is detectable as the top hit of its own topic.
func patternRetrieverFixture(t *testing.T) *Store {
	t.Helper()
	store := newTestStore(t)
	now := time.Now().UTC()
	nodes := []*Node{
		{NodeID: "n-dispatch", Body: "the dispatch router selects the cheapest model for batch jobs"},
		{NodeID: "n-pattern-curation", Role: NodeRolePattern, Body: "quokka telemetry pivot recurrence summarized for evolution curation"},
	}
	for _, n := range nodes {
		n.CreatedBy = CreatorIngester
		n.CreatedVersion = 1
		n.UpdatedVersion = 1
		n.ObservedAt = now
		require.NoError(t, store.SaveNode(1, n))
	}
	return store
}

// TestSearchExcludesPatternNodesWithoutGrant pins spec §12.3: the plain
// graph corpus never includes pattern nodes, including for legacy callers
// whose zero GraphView is inactive (unfiltered). Only a view carrying the
// explicit pattern grant may surface them.
func TestSearchExcludesPatternNodesWithoutGrant(t *testing.T) {
	store := patternRetrieverFixture(t)

	r := NewHybridRetriever(store, nil, DefaultRetrievalConfig())
	require.NoError(t, r.Rebuild(context.Background()))

	hits, err := r.Search(context.Background(), "quokka telemetry pivot recurrence")
	require.NoError(t, err)
	for _, hit := range hits {
		assert.NotEqual(t, "n-pattern-curation", hit.ID,
			"pattern node surfaced through the default corpus without a pattern grant")
	}

	memoryHits, err := r.Search(context.Background(), "dispatch router cheapest model")
	require.NoError(t, err)
	require.NotEmpty(t, memoryHits, "memory nodes must stay retrievable")
	assert.Equal(t, "n-dispatch", memoryHits[0].ID)

	granted := DefaultRetrievalConfig()
	granted.View.AllowPatternRole = true
	grantedRetriever := NewHybridRetriever(store, nil, granted)
	require.NoError(t, grantedRetriever.Rebuild(context.Background()))
	grantedHits, err := grantedRetriever.Search(context.Background(), "quokka telemetry pivot recurrence")
	require.NoError(t, err)
	require.NotEmpty(t, grantedHits, "the pattern grant must admit pattern nodes")
	assert.Equal(t, "n-pattern-curation", grantedHits[0].ID)
}

func TestAllowsNodeIDAndSearchAtExcludePatternWithoutGrant(t *testing.T) {
	store := patternRetrieverFixture(t)

	r := NewHybridRetriever(store, nil, DefaultRetrievalConfig())
	require.NoError(t, r.Rebuild(context.Background()))
	assert.False(t, r.AllowsNodeID("n-pattern-curation"),
		"continuation seed merge must not smuggle pattern nodes")
	assert.True(t, r.AllowsNodeID("n-dispatch"))

	hits, err := r.SearchAt(context.Background(), "quokka telemetry pivot recurrence", GraphView{}, 0)
	require.NoError(t, err)
	for _, hit := range hits {
		assert.NotEqual(t, "n-pattern-curation", hit.Ref.NodeID,
			"SearchAt surfaced a pattern node without a pattern grant")
	}

	hits, err = r.SearchAt(context.Background(), "quokka telemetry pivot recurrence",
		GraphView{AllowPatternRole: true}, 0)
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	assert.Equal(t, "n-pattern-curation", hits[0].Ref.NodeID)
}

func TestGraphViewPatternGateIsRoleSpecific(t *testing.T) {
	memory := &Node{NodeID: "n-mem"}
	skill := &Node{NodeID: "n-skill", Role: NodeRoleSkill}
	legacy := &Node{NodeID: "n-legacy"}
	pattern := &Node{NodeID: "n-pattern", Role: NodeRolePattern}

	zero := GraphView{}
	assert.True(t, zero.visibleForRetrieval(memory))
	assert.True(t, zero.visibleForRetrieval(skill), "skill projections stay in the task corpus")
	assert.True(t, zero.visibleForRetrieval(legacy), "role-less nodes read as memory")
	assert.False(t, zero.visibleForRetrieval(pattern))

	granted := GraphView{AllowPatternRole: true}
	assert.True(t, granted.visibleForRetrieval(pattern))

	scoped := GraphView{AllowProject: true}
	scopedPattern := &Node{NodeID: "n-scoped-pattern", Role: NodeRolePattern}
	assert.False(t, scoped.visibleForRetrieval(scopedPattern),
		"scope flags alone must not imply the pattern grant")
	assert.True(t, scoped.visibleForRetrieval(&Node{NodeID: "n-project"}))
	assert.False(t, scoped.visibleForRetrieval(&Node{NodeID: "n-chan", ChannelID: "c1", Visibility: "channel"}))
}

func TestNodeRoleMetadataSerializationRoundTrip(t *testing.T) {
	in := &Node{
		NodeID:           "n-round",
		Role:             NodeRolePattern,
		DerivedAtomKinds: []AtomKind{AtomDecision, AtomConstraint},
		Body:             "round trip",
		ObservedAt:       time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
	}

	yamlBytes, err := yaml.Marshal(in)
	require.NoError(t, err)
	var fromYAML Node
	require.NoError(t, yaml.Unmarshal(yamlBytes, &fromYAML))
	assert.Equal(t, NodeRolePattern, fromYAML.Role)
	assert.Equal(t, []AtomKind{AtomDecision, AtomConstraint}, fromYAML.DerivedAtomKinds)

	jsonBytes, err := json.Marshal(in)
	require.NoError(t, err)
	var fromJSON Node
	require.NoError(t, json.Unmarshal(jsonBytes, &fromJSON))
	assert.Equal(t, NodeRolePattern, fromJSON.Role)
	assert.Equal(t, []AtomKind{AtomDecision, AtomConstraint}, fromJSON.DerivedAtomKinds)

	legacyBytes, err := yaml.Marshal(&Node{NodeID: "n-legacy", Body: "legacy"})
	require.NoError(t, err)
	var legacy Node
	require.NoError(t, yaml.Unmarshal(legacyBytes, &legacy))
	assert.Equal(t, NodeRole(""), legacy.Role, "legacy nodes persist without a role")
	assert.Equal(t, NodeRoleMemory, EffectiveNodeRole(legacy.Role))
}

func TestLegacyAtomKindDispositionForbidsSilentMapping(t *testing.T) {
	rule, ok := LegacyAtomKindDispositionFor("rule")
	require.True(t, ok)
	assert.Equal(t, LegacyAtomKindExplicitChoice, rule.Action)
	assert.ElementsMatch(t, []AtomKind{AtomInstruction, AtomConstraint}, rule.AllowedTargets)

	procedure, ok := LegacyAtomKindDispositionFor("procedure")
	require.True(t, ok)
	assert.Equal(t, LegacyAtomKindCandidateReEvaluation, procedure.Action)
	assert.ElementsMatch(t, []AtomKind{
		AtomFact, AtomEvent, AtomInstruction, AtomPreference,
		AtomDecision, AtomConstraint, AtomFallback,
	}, procedure.AllowedTargets)

	for _, kind := range []string{"fact", "decision", "summary", "rule-ish", ""} {
		_, ok := LegacyAtomKindDispositionFor(kind)
		assert.False(t, ok, "kind %q must not carry a legacy disposition", kind)
	}

	assert.False(t, ValidAtomKind("rule"), "rule must stay outside the closed kind set")
	assert.False(t, ValidAtomKind("procedure"), "procedure must stay outside the closed kind set")
}
