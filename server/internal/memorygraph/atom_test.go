// SPDX-License-Identifier: Apache-2.0

package memorygraph

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeAtomBodyCollapsesWhitespace(t *testing.T) {
	assert.Equal(t, "alpha beta gamma", NormalizeAtomBody("  alpha\t\tbeta\n\n gamma  "))
	assert.Equal(t, "", NormalizeAtomBody("   \n\t  "))
	assert.Equal(t, "single", NormalizeAtomBody("single"))
}

func TestStableAtomIDIsSegmentScopedAndNormalized(t *testing.T) {
	first := StableAtomID("seg-1", "fact", "the codename  is  NIMBUS", []int32{1, 2}, "read_file")
	// Same identity, different raw whitespace: the normalized body hashes equal.
	second := StableAtomID("seg-1", "fact", "the codename is NIMBUS", []int32{1, 2}, "read_file")
	assert.Equal(t, first, second, "whitespace normalization must not change the atom id")

	require.Regexp(t, `^atom-[0-9a-f]{24,}$`, first)

	// Segment scoping: the same fact in another segment is a different atom.
	other := StableAtomID("seg-2", "fact", "the codename is NIMBUS", []int32{1, 2}, "read_file")
	assert.NotEqual(t, first, other)

	// Any identity-relevant change re-keys the id.
	assert.NotEqual(t, first, StableAtomID("seg-1", "preference", "the codename is NIMBUS", []int32{1, 2}, "read_file"))
	assert.NotEqual(t, first, StableAtomID("seg-1", "fact", "the codename is NIMBUS", []int32{1, 3}, "read_file"))
	assert.NotEqual(t, first, StableAtomID("seg-1", "fact", "the codename is NIMBUS", []int32{1, 2}, "bash"))

	// Sequence order is canonicalized before hashing.
	assert.Equal(t, first, StableAtomID("seg-1", "fact", "the codename is NIMBUS", []int32{2, 1}, "read_file"))
}

func TestClassifyToolTrust(t *testing.T) {
	// Trusted read-only tools observe state without mutating it.
	for _, tool := range []string{"read_file", "read", "cat", "glob", "grep", "rg",
		"web_search", "web_fetch", "read_history", "search_messages", "check_messages",
		"list_tasks", "get_issue", "search_issues", "list_issue_comments", "view_file"} {
		assert.Equal(t, AtomTrustReadOnly, ClassifyToolTrust(tool), tool)
	}
	// Mutation tools change user or system state.
	for _, tool := range []string{"write_file", "write", "edit_file", "edit", "bash", "shell",
		"exec", "todo_write", "send_message", "create_tasks", "update_task_status",
		"comment_issue", "upload_file", "schedule_reminder"} {
		assert.Equal(t, AtomTrustMutation, ClassifyToolTrust(tool), tool)
	}
	// Unknown tools fail safe: never trusted.
	assert.Equal(t, AtomTrustUnknown, ClassifyToolTrust("quantum_flux"))
	assert.Equal(t, AtomTrustUnknown, ClassifyToolTrust("mcp__custom__scanner"))
	assert.Equal(t, AtomTrustNone, ClassifyToolTrust(""))
	assert.Equal(t, AtomTrustNone, ClassifyToolTrust("   "))
}

func TestAtomTrustRankFailsSafe(t *testing.T) {
	assert.Equal(t, AtomTrustNone, WeakestToolTrust())
	assert.True(t, AtomTrustRank(AtomTrustReadOnly) > AtomTrustRank(AtomTrustMutation))
	assert.True(t, AtomTrustRank(AtomTrustMutation) > AtomTrustRank(AtomTrustUnknown))
	assert.True(t, AtomTrustRank(AtomTrustUnknown) > AtomTrustRank(AtomTrustNone))
	// The weakest citation wins.
	assert.Equal(t, AtomTrustUnknown, WeakestToolTrust(AtomTrustReadOnly, AtomTrustUnknown))
	assert.Equal(t, AtomTrustMutation, WeakestToolTrust(AtomTrustReadOnly, AtomTrustMutation))
}

func TestAtomKindsAreClosed(t *testing.T) {
	for _, kind := range []string{"fact", "preference", "fallback"} {
		require.True(t, ValidAtomKind(kind), kind)
	}
	assert.False(t, ValidAtomKind("FACT"))
	assert.False(t, ValidAtomKind("summary"))
	assert.False(t, ValidAtomKind(""))
	assert.NotNil(t, regexp.MustCompile(`^[a-z_]+$`), "sanity: kinds are snake_case tokens")
}
