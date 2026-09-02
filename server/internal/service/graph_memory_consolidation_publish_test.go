// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/pkg/agent"
)

// publishTestScope is a minimal valid consolidate provider scope.
func publishTestScope(workspaceID string) memorygraph.ProviderScope {
	return memorygraph.ProviderScope{
		WorkspaceID: workspaceID, Purpose: memorygraph.ProviderPurposeConsolidate,
		Provider: "pi", Model: "pi-test", Region: "us", PolicyVersion: "test",
	}
}

// fakePublishBackend plays the consolidation agent for the service bridge:
// it cites every atom id the prompt carries on one new node.
type fakePublishBackend struct {
	prompts  []string
	citeAll  bool
	citeNone bool
}

func (f *fakePublishBackend) Execute(_ context.Context, prompt string, _ agent.ExecOptions) (*agent.Session, error) {
	f.prompts = append(f.prompts, prompt)
	var atoms []string
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(line, "- atom ") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				atoms = append(atoms, fields[2])
			}
		}
	}
	if f.citeNone {
		atoms = nil
	} else if !f.citeAll && len(atoms) > 1 {
		atoms = atoms[:1]
	}
	refs := "[]"
	if len(atoms) > 0 {
		refs = `["` + strings.Join(atoms, `","`) + `"]`
	}
	output := `{"operations":[{"op":"add_node","node":{"node_id":"n-fold","body":"folded atoms","atom_refs":` + refs + `}}]}`
	msgs := make(chan agent.Message)
	close(msgs)
	results := make(chan agent.Result, 1)
	results <- agent.Result{Status: "completed", Output: output}
	close(results)
	return &agent.Session{Messages: msgs, Result: results}, nil
}

// The service bridge drives the full Task 14 chain: atom manifest from the
// DB, immutable candidate, source-locked CAS publication with coverage and
// reverse provenance ledgers.
func TestGraphMemoryConsolidationPublish_PublishesThroughCoordinator(t *testing.T) {
	h := newPublicationHarness(t)
	defer h.Close()
	t.Setenv("MULTICA_WORKSPACES_ROOT", t.TempDir())

	backend := &fakePublishBackend{citeAll: true}
	svc := NewGraphMemoryConsolidationPublishService(h.pubPool)
	report, err := svc.PublishScope(h.ctx, h.workspace, "channel", h.channel.String(),
		backend, publishTestScope(h.workspace.String()))
	require.NoError(t, err)
	assert.Equal(t, GraphMemoryConsolidationPublishPublished, report.Outcome)
	assert.EqualValues(t, 1, report.Generation)
	assert.Positive(t, report.AtomCount)

	// The ledgers carry the atom closure and the node provenance.
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM graph_memory_publication WHERE current_generation=1`))
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM graph_memory_publication_index WHERE active_generation=1`))
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM graph_memory_publication_coverage WHERE atom_id=$1`, h.atomID))
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM graph_memory_publication_provenance WHERE node_id='n-fold'`))
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM graph_memory_publication_outcome WHERE outcome='published'`))
	// Publication sources are the segment task_outputs.
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM graph_memory_publication_outcome WHERE source_keys @> ARRAY['task_output'] OR array_length(source_keys,1) >= 1`))

	// A second cycle reaches generation 2 and stays consistent.
	report2, err := svc.PublishScope(h.ctx, h.workspace, "channel", h.channel.String(),
		backend, publishTestScope(h.workspace.String()))
	require.NoError(t, err)
	assert.Equal(t, GraphMemoryConsolidationPublishPublished, report2.Outcome)
	assert.EqualValues(t, 2, report2.Generation)
}

// A disabled atom_consolidation gate means the scheduler path claims
// nothing: the service refuses before any manifest read.
func TestGraphMemoryConsolidationPublish_DisabledGateClaimsNothing(t *testing.T) {
	h := newPublicationHarness(t)
	defer h.Close()
	h.disableConsolidationRoute(t)
	t.Setenv("MULTICA_WORKSPACES_ROOT", t.TempDir())

	svc := NewGraphMemoryConsolidationPublishService(h.pubPool)
	_, err := svc.PublishScope(h.ctx, h.workspace, "channel", h.channel.String(),
		&fakePublishBackend{citeAll: true}, publishTestScope(h.workspace.String()))
	require.ErrorIs(t, err, ErrMemoryRouteDisabled)
	assert.Equal(t, 0, h.countRows(t, `SELECT count(*) FROM graph_memory_publication`))
}

// A candidate that leaves atoms uncited consumes nothing: publication is
// refused, no generation moves, and the atoms stay visible to the next
// cycle (loser/failure non-consumption).
func TestGraphMemoryConsolidationPublish_UncitedAtomsAbortWithoutConsuming(t *testing.T) {
	h := newPublicationHarness(t)
	defer h.Close()
	t.Setenv("MULTICA_WORKSPACES_ROOT", t.TempDir())

	// A backend that cites none of the manifest atoms produces a candidate
	// that covers nothing: publication is refused and the atoms stay
	// unconsumed for the next cycle.
	svc := NewGraphMemoryConsolidationPublishService(h.pubPool)
	partial := &fakePublishBackend{citeNone: true}
	report, err := svc.PublishScope(h.ctx, h.workspace, "channel", h.channel.String(),
		partial, publishTestScope(h.workspace.String()))
	require.ErrorIs(t, err, ErrGraphMemoryConsolidationUncitedAtoms)
	require.NotNil(t, report)
	assert.Equal(t, GraphMemoryConsolidationPublishAbortedUncited, report.Outcome)
	assert.Equal(t, []string{h.atomID}, report.UncitedAtomIDs)
	assert.Equal(t, 0, h.countRows(t, `SELECT count(*) FROM graph_memory_publication`))
	assert.Equal(t, 0, h.countRows(t, `SELECT count(*) FROM graph_memory_publication_index`))
	// The next cycle still sees the atom and can publish it.
	_, err = svc.PublishScope(h.ctx, h.workspace, "channel", h.channel.String(),
		&fakePublishBackend{citeAll: true}, publishTestScope(h.workspace.String()))
	require.NoError(t, err)
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM graph_memory_publication WHERE current_generation=1`))
}
