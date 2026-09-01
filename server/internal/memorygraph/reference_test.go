// SPDX-License-Identifier: Apache-2.0

package memorygraph

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Ref identities are hostiles' playgrounds: colons, slashes, unicode and
// emoji must round-trip through the canonical key form untouched, because
// the key prefix — not a naive colon split — carries the kind.
func TestMemoryRefRoundTripsHostileIDs(t *testing.T) {
	ids := []string{
		"plain",
		"with:colon:and:more",
		"with/slash/deep/path",
		"中文标识符",
		"emoji-🧠-graph",
		"mixed:中文/slash🧠",
	}
	for _, id := range ids {
		node := MemoryRef{Kind: MemoryRefGraphNode, NodeID: id}
		require.NoError(t, ValidateMemoryRef(node), id)
		parsed, err := ParseMemoryRefKey(node.Key())
		require.NoError(t, err, id)
		assert.Equal(t, node, parsed, "graph_node ref must round-trip: %s", id)

		atom := MemoryRef{Kind: MemoryRefStagingAtom, AtomID: id, SegmentID: "seg-1"}
		require.NoError(t, ValidateMemoryRef(atom), id)
		parsed, err = ParseMemoryRefKey(atom.Key())
		require.NoError(t, err, id)
		assert.Equal(t, atom.Kind, parsed.Kind, id)
		assert.Equal(t, atom.AtomID, parsed.AtomID, id)
	}
}

// A ref that forges a graph identity or combines fields illegally is
// rejected before any resolver sees it.
func TestMemoryRefValidationRejectsForgedIdentities(t *testing.T) {
	cases := []struct {
		name string
		ref  MemoryRef
	}{
		{"unknown kind", MemoryRef{Kind: MemoryRefKind("admin"), NodeID: "n"}},
		{"empty kind", MemoryRef{NodeID: "n"}},
		{"graph node without id", MemoryRef{Kind: MemoryRefGraphNode}},
		{"graph node with atom field", MemoryRef{Kind: MemoryRefGraphNode, NodeID: "n", AtomID: "a"}},
		{"atom without id", MemoryRef{Kind: MemoryRefStagingAtom, SegmentID: "s"}},
		{"atom with node field", MemoryRef{Kind: MemoryRefStagingAtom, AtomID: "a", NodeID: "n"}},
		{"control chars in id", MemoryRef{Kind: MemoryRefGraphNode, NodeID: "n\x00bad"}},
		{"oversize id", MemoryRef{Kind: MemoryRefGraphNode, NodeID: padTo(257)}},
		{"oversize segment", MemoryRef{Kind: MemoryRefStagingAtom, AtomID: "a", SegmentID: padTo(257)}},
	}
	for _, tc := range cases {
		assert.Error(t, ValidateMemoryRef(tc.ref), tc.name)
	}
	// Legal shapes pass.
	assert.NoError(t, ValidateMemoryRef(MemoryRef{Kind: MemoryRefGraphNode, NodeID: "n", ChannelID: "chan-a"}))
	assert.NoError(t, ValidateMemoryRef(MemoryRef{Kind: MemoryRefStagingAtom, AtomID: "a", SegmentID: "seg"}))
}

func TestParseMemoryRefKeyRejectsGarbage(t *testing.T) {
	for _, key := range []string{"", "graph_node", "staging_atom", "unknown:x", "graph_node:"} {
		_, err := ParseMemoryRefKey(key)
		assert.Error(t, err, key)
	}
	// A key whose id itself contains the kind prefixes still parses by
	// prefix, not by first colon.
	ref, err := ParseMemoryRefKey("staging_atom:graph_node:atom-abc")
	require.NoError(t, err)
	assert.Equal(t, MemoryRefStagingAtom, ref.Kind)
	assert.Equal(t, "graph_node:atom-abc", ref.AtomID)
}

func padTo(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = 'x'
	}
	return string(out)
}
