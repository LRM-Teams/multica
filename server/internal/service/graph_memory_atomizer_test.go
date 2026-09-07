// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func atomizerFixturePayload() SanitizedTrajectory {
	return SanitizedTrajectory{
		SanitizerVersion: interactionDAGSanitizerVersion,
		Messages: []SanitizedTaskMessage{
			{Sequence: 1, Type: "user", Content: "My project codename is NIMBUS and the launch date is March 3rd."},
			{Sequence: 2, Type: "assistant", Tool: "read_file", Content: "I noted the codename preference."},
			{Sequence: 3, Type: "assistant", Tool: "bash", Output: "deploy finished successfully"},
		},
	}
}

func atomizerFixtureSegment() AtomizerSegment {
	return AtomizerSegment{
		SegmentID: "seg-atom-1", StartSeq: 1, EndSeq: 3,
		MemoryTypeAtEvent: "graph", GraphProjectionEligible: true,
		CloseActionKind: "message", ChannelID: "11111111-1111-1111-1111-111111111111",
	}
}

// TestGraphMemoryAtomizer_PublishesAllClosedKinds pins the spec §4 kind
// vocabulary at the publication boundary: every proposer-selectable kind
// survives validation with its kind intact (fallback stays server-only and
// is covered by TestGraphMemoryAtomizer_FallbackAtomOnExtractorFailure).
func TestGraphMemoryAtomizer_PublishesAllClosedKinds(t *testing.T) {
	candidates := make([]AtomCandidate, 0, 6)
	for _, kind := range []string{
		"fact", "event", "instruction", "preference", "decision", "constraint",
	} {
		candidates = append(candidates, AtomCandidate{
			Body: kind + " recorded for the NIMBUS launch",
			Kind: kind, SourceMessageSeqs: []int32{1},
		})
	}
	atomizer := NewGraphMemoryAtomizer(&stubAtomProposer{candidates: candidates})
	atoms, err := atomizer.ExtractAtoms(context.Background(), atomizerFixtureSegment(), atomizerFixturePayload())
	require.NoError(t, err)
	require.Len(t, atoms, len(candidates))

	published := map[string]bool{}
	for _, atom := range atoms {
		published[atom.Kind] = true
		assert.True(t, memorygraph.ValidAtomKind(atom.Kind), "published kind %q must be in the closed set", atom.Kind)
		assert.Regexp(t, `^atom-[0-9a-f]{24,}$`, atom.AtomID)
	}
	for _, kind := range []string{"fact", "event", "instruction", "preference", "decision", "constraint"} {
		assert.True(t, published[kind], "kind %q was not published", kind)
	}
}

// TestGraphMemoryAtomizer_RejectsLegacyRuleAndProcedureKinds: the retired
// labels must not be silently mapped onto current kinds at any publication
// boundary (spec §4, plan Slice 1.1).
func TestGraphMemoryAtomizer_RejectsLegacyRuleAndProcedureKinds(t *testing.T) {
	atomizer := NewGraphMemoryAtomizer(&stubAtomProposer{candidates: []AtomCandidate{
		{Body: "legacy rule body", Kind: "rule", SourceMessageSeqs: []int32{1}},
		{Body: "legacy procedure body", Kind: "procedure", SourceMessageSeqs: []int32{1}},
		{Body: "current fact body", Kind: "fact", SourceMessageSeqs: []int32{1}},
	}})
	atoms, err := atomizer.ExtractAtoms(context.Background(), atomizerFixtureSegment(), atomizerFixturePayload())
	require.NoError(t, err)
	require.Len(t, atoms, 1, "only the current-kind candidate may publish")
	assert.Equal(t, "fact", atoms[0].Kind)

	for _, legacy := range []string{"rule", "procedure"} {
		disposition, ok := memorygraph.LegacyAtomKindDispositionFor(legacy)
		require.True(t, ok)
		assert.False(t, memorygraph.ValidAtomKind(legacy),
			"the legacy label %q must stay unpublishable", legacy)
		assert.NotEmpty(t, disposition.AllowedTargets,
			"the legacy label %q must route through an explicit disposition, never a silent map", legacy)
	}
}

func TestGraphMemoryAtomizer_ExtractsMultipleFactsDeterministically(t *testing.T) {
	atomizer := NewGraphMemoryAtomizer(nil)
	segment := atomizerFixtureSegment()

	first, err := atomizer.ExtractAtoms(context.Background(), segment, atomizerFixturePayload())
	require.NoError(t, err)
	require.NotEmpty(t, first, "a multi-fact segment must yield atoms")
	require.LessOrEqual(t, len(first), interactionDAGMaxAtomsPerSegment)

	second, err := atomizer.ExtractAtoms(context.Background(), segment, atomizerFixturePayload())
	require.NoError(t, err)
	require.Equal(t, atomIDs(first), atomIDs(second), "atom identity must be retry-stable")

	ids := map[string]bool{}
	for _, atom := range first {
		// Scope is inherited from the Segment, never proposed (AC 11).
		assert.Equal(t, segment.SegmentID, atom.SegmentID)
		assert.Equal(t, "channel", atom.Visibility)
		assert.Equal(t, segment.ChannelID, atom.ChannelID)
		assert.Empty(t, atom.ProjectID)
		assert.Equal(t, "fact", atom.Kind)
		assert.NotEmpty(t, atom.ContentHash)
		assert.Regexp(t, `^atom-[0-9a-f]{24,}$`, atom.AtomID)
		assert.NotEmpty(t, atom.Body)
		for _, seq := range atom.SourceMessageSeqs {
			assert.GreaterOrEqual(t, seq, segment.StartSeq)
			assert.LessOrEqual(t, seq, segment.EndSeq)
		}
		assert.False(t, ids[atom.AtomID], "atom ids are unique within a segment")
		ids[atom.AtomID] = true
	}
}

func TestGraphMemoryAtomizer_StampsToolTrustFromCitedMessages(t *testing.T) {
	atomizer := NewGraphMemoryAtomizer(&stubAtomProposer{candidates: []AtomCandidate{
		{Body: "codename preference recorded", Kind: "fact", SourceMessageSeqs: []int32{2}},
		{Body: "deploy completed", Kind: "fact", SourceMessageSeqs: []int32{3}},
		{Body: "mixed evidence", Kind: "fact", SourceMessageSeqs: []int32{2, 3}},
	}})
	atoms, err := atomizer.ExtractAtoms(context.Background(), atomizerFixtureSegment(), atomizerFixturePayload())
	require.NoError(t, err)
	require.Len(t, atoms, 3)

	byBody := map[string]memorygraph.Atom{}
	for _, atom := range atoms {
		byBody[atom.Body] = atom
	}
	assert.Equal(t, string(memorygraph.AtomTrustReadOnly), byBody["codename preference recorded"].ToolTrustClass)
	assert.Equal(t, "read_file", byBody["codename preference recorded"].SourceTool)
	assert.Equal(t, string(memorygraph.AtomTrustMutation), byBody["deploy completed"].ToolTrustClass)
	assert.Equal(t, "bash", byBody["deploy completed"].SourceTool)
	// A mixed citation fails safe to the weakest class.
	assert.Equal(t, string(memorygraph.AtomTrustMutation), byBody["mixed evidence"].ToolTrustClass)
}

func TestGraphMemoryAtomizer_RejectsInvalidSeqRefsAndUnknownKinds(t *testing.T) {
	atomizer := NewGraphMemoryAtomizer(&stubAtomProposer{candidates: []AtomCandidate{
		{Body: "in range", Kind: "fact", SourceMessageSeqs: []int32{1}},
		{Body: "outside range", Kind: "fact", SourceMessageSeqs: []int32{9}},
		{Body: "dangling", Kind: "fact", SourceMessageSeqs: []int32{2, 7}},
		{Body: "wrong kind", Kind: "summary", SourceMessageSeqs: []int32{1}},
		{Body: "empty refs", Kind: "fact", SourceMessageSeqs: nil},
	}})
	atoms, err := atomizer.ExtractAtoms(context.Background(), atomizerFixtureSegment(), atomizerFixturePayload())
	require.NoError(t, err)
	require.Len(t, atoms, 1, "only the fully valid candidate survives")
	assert.Equal(t, "in range", atoms[0].Body)
}

func TestGraphMemoryAtomizer_EnforcesBudgets(t *testing.T) {
	many := make([]AtomCandidate, interactionDAGMaxAtomsPerSegment+5)
	for i := range many {
		many[i] = AtomCandidate{Body: fmt.Sprintf("fact number %d", i), Kind: "fact", SourceMessageSeqs: []int32{1}}
	}
	long := strings.Repeat("x", interactionDAGMaxAtomBodyRunes+100)

	atomizer := NewGraphMemoryAtomizer(&stubAtomProposer{candidates: append(many,
		AtomCandidate{Body: long, Kind: "fact", SourceMessageSeqs: []int32{1}})})
	atoms, err := atomizer.ExtractAtoms(context.Background(), atomizerFixtureSegment(), atomizerFixturePayload())
	require.NoError(t, err)
	require.Len(t, atoms, interactionDAGMaxAtomsPerSegment, "the atom count budget caps the batch")
	for _, atom := range atoms {
		assert.LessOrEqual(t, len([]rune(atom.Body)), interactionDAGMaxAtomBodyRunes, "the body budget caps length")
	}
}

func TestGraphMemoryAtomizer_ZeroAtomsForIneligibleSegments(t *testing.T) {
	atomizer := NewGraphMemoryAtomizer(nil)
	payload := atomizerFixturePayload()
	base := atomizerFixtureSegment()

	cases := map[string]AtomizerSegment{
		"legacy memory type":    func() AtomizerSegment { s := base; s.MemoryTypeAtEvent = "legacy"; return s }(),
		"graph without scope":   func() AtomizerSegment { s := base; s.ChannelID = ""; s.ProjectID = ""; return s }(),
		"derivative":            func() AtomizerSegment { s := base; s.Derivative = true; return s }(),
		"not event-eligible":    func() AtomizerSegment { s := base; s.GraphProjectionEligible = false; return s }(),
		"metadata-only closure": func() AtomizerSegment { s := base; s.CloseActionKind = "metadata_only"; return s }(),
		"project-only scope": func() AtomizerSegment {
			s := base
			s.ChannelID = ""
			s.ProjectID = "22222222-2222-2222-2222-222222222222"
			return s
		}(),
	}
	for name, segment := range cases {
		atoms, err := atomizer.ExtractAtoms(context.Background(), segment, payload)
		require.NoError(t, err, name)
		if name == "project-only scope" {
			// Project-only graph segments DO atomize, under project visibility.
			require.NotEmpty(t, atoms, name)
			assert.Equal(t, "project", atoms[0].Visibility)
			assert.Equal(t, segment.ProjectID, atoms[0].ProjectID)
			continue
		}
		assert.Empty(t, atoms, name)
	}

	// Trivial content has no memory value: zero atoms, no error.
	trivial := SanitizedTrajectory{Messages: []SanitizedTaskMessage{
		{Sequence: 1, Type: "user", Content: "ok"},
	}}
	atoms, err := atomizer.ExtractAtoms(context.Background(), base, trivial)
	require.NoError(t, err)
	assert.Empty(t, atoms, "no-value content yields zero atoms")
}

func TestGraphMemoryAtomizer_CanonicalTextMessagesYieldAtoms(t *testing.T) {
	// Post-universal-DAG canonical rows: readable channel turns are typed
	// "text" and tool rows carry their substance outside Content. The
	// deterministic proposer must extract from the text rows.
	atomizer := NewGraphMemoryAtomizer(nil)
	payload := SanitizedTrajectory{
		SanitizerVersion: interactionDAGSanitizerVersion,
		Messages: []SanitizedTaskMessage{
			{Sequence: 1, Type: "tool_use", Tool: "channel_context", Content: ""},
			{Sequence: 2, Type: "tool_result", Tool: "channel_context", Content: ""},
			{Sequence: 3, Type: "text", Content: "The deployment owner for HD-PG02 is Dana and the freeze window ends Friday."},
			{Sequence: 4, Type: "text", Content: "."},
		},
	}
	segment := AtomizerSegment{
		SegmentID: "seg-atom-text", StartSeq: 1, EndSeq: 4,
		MemoryTypeAtEvent: "graph", GraphProjectionEligible: true,
		CloseActionKind: "terminal", ChannelID: "11111111-1111-1111-1111-111111111111",
	}
	atoms, err := atomizer.ExtractAtoms(context.Background(), segment, payload)
	require.NoError(t, err)
	require.NotEmpty(t, atoms, "canonical text turns must yield atoms")
	for _, atom := range atoms {
		assert.Equal(t, "fact", atom.Kind)
		assert.Contains(t, atom.SourceMessageSeqs, int32(3))
	}
}

func TestGraphMemoryAtomizer_FallbackAtomOnExtractorFailure(t *testing.T) {
	atomizer := NewGraphMemoryAtomizer(&stubAtomProposer{err: errors.New("extractor exploded")})
	atoms, err := atomizer.ExtractAtoms(context.Background(), atomizerFixtureSegment(), atomizerFixturePayload())
	require.NoError(t, err, "an extractor failure degrades to a fallback atom, not an error")
	require.Len(t, atoms, 1)

	fallback := atoms[0]
	assert.Equal(t, "fallback", fallback.Kind)
	assert.Empty(t, fallback.SourceMessageSeqs, "a fallback must not claim structured coverage")
	assert.Empty(t, fallback.SourceTool)
	assert.Equal(t, string(memorygraph.AtomTrustNone), fallback.ToolTrustClass)
	assert.NotContains(t, fallback.Body, "NIMBUS", "the fallback body is content-free")
	assert.Equal(t, "channel", fallback.Visibility)
}

func TestGraphMemoryAtomizer_PolicyFailureDegradesToFallbackAtom(t *testing.T) {
	degraded := &MemoryProviderPolicyError{Purpose: ProviderAtomize, Kind: MemoryProviderDisabled}
	degraded.Degradation = DegradeFallbackAtom
	atomizer := NewGraphMemoryAtomizer(&stubAtomProposer{err: degraded})
	atoms, err := atomizer.ExtractAtoms(context.Background(), atomizerFixtureSegment(), atomizerFixturePayload())
	require.NoError(t, err)
	require.Len(t, atoms, 1)
	assert.Equal(t, "fallback", atoms[0].Kind)
}

func atomIDs(atoms []memorygraph.Atom) []string {
	ids := make([]string, 0, len(atoms))
	for _, atom := range atoms {
		ids = append(ids, atom.AtomID)
	}
	return ids
}

type stubAtomProposer struct {
	candidates []AtomCandidate
	err        error
}

func (s *stubAtomProposer) ProposeAtoms(context.Context, AtomizerSegment, SanitizedTrajectory) ([]AtomCandidate, error) {
	return s.candidates, s.err
}

// --- publisher integration: atoms + projection request in the publish tx ---

func atomRowsForSegment(t *testing.T, h *universalDAGPublisherHarness, segmentID string) []memorygraph.Atom {
	t.Helper()
	rows, err := h.conn.Query(h.ctx, `
		SELECT atom_id, segment_id, body, kind, array_to_json(source_message_seqs)::text, source_tool,
		       tool_trust_class, content_hash, COALESCE(artifact_ref,''), visibility,
		       COALESCE(channel_id::text,''), COALESCE(project_id::text,''), publish_seq
		FROM graph_memory_atom WHERE segment_id=$1 ORDER BY atom_id`, segmentID)
	require.NoError(t, err)
	defer rows.Close()
	atoms := make([]memorygraph.Atom, 0)
	for rows.Next() {
		var atom memorygraph.Atom
		var seqs string
		require.NoError(t, rows.Scan(&atom.AtomID, &atom.SegmentID, &atom.Body, &atom.Kind, &seqs,
			&atom.SourceTool, &atom.ToolTrustClass, &atom.ContentHash, &atom.ArtifactRef,
			&atom.Visibility, &atom.ChannelID, &atom.ProjectID, &atom.PublishSeq))
		var parsed []int32
		require.NoError(t, json.Unmarshal([]byte(seqs), &parsed))
		atom.SourceMessageSeqs = parsed
		atoms = append(atoms, atom)
	}
	return atoms
}

func projectionRowForSegment(t *testing.T, h *universalDAGPublisherHarness, segmentID string) (status, requestHash string, attempts int32, found bool) {
	t.Helper()
	err := h.conn.QueryRow(h.ctx, `
		SELECT status, request_hash, attempts FROM graph_memory_projection_outbox
		WHERE segment_id=$1`, segmentID).Scan(&status, &requestHash, &attempts)
	if err != nil {
		require.ErrorIs(t, err, pgx.ErrNoRows, "projection row read")
		return "", "", 0, false
	}
	return status, requestHash, attempts, true
}

func TestInteractionDAGPublisher_PublishPersistsAtomsAndProjectionRequest(t *testing.T) {
	h := newUniversalDAGPublisherHarness(t)
	defer h.Close()
	task := h.createTask(t, h.ctx, 1)
	setTaskMessageContent(t, h, task,
		"My project codename is NIMBUS and the launch date is March 3rd.", `{"a":1}`, "")
	segmentID := h.recordMessageSegment(task, 1, "atom-keystone")

	published, err := NewInteractionDAGPublisher(h.pubPool).PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, published)

	segment := h.segmentRow(segmentID)
	atoms := atomRowsForSegment(t, h, segmentID)
	require.NotEmpty(t, atoms, "a graph channel segment with facts must produce atoms")
	for _, atom := range atoms {
		assert.Equal(t, segmentID, atom.SegmentID)
		assert.Equal(t, segment.publishSeq, atom.PublishSeq, "atoms commit with the segment publish_seq")
		assert.Equal(t, "channel", atom.Visibility)
		assert.Equal(t, h.channel.String(), atom.ChannelID)
		assert.Equal(t, "fact", atom.Kind)
		assert.Equal(t, []int32{1}, atom.SourceMessageSeqs)
		assert.Contains(t, atom.Body, "NIMBUS")
	}

	status, requestHash, attempts, found := projectionRowForSegment(t, h, segmentID)
	require.True(t, found, "the durable graph projection request is written in the publish transaction")
	assert.Equal(t, "pending", status)
	assert.NotEmpty(t, requestHash)
	assert.Zero(t, attempts)
}

func TestInteractionDAGPublisher_IneligibleSegmentsProduceNoAtomsOrProjection(t *testing.T) {
	h := newUniversalDAGPublisherHarness(t)
	defer h.Close()
	// A derivative memory-agent segment is ineligible for graph atoms (AC 14).
	task := h.createTask(t, h.ctx, 1)
	setTaskMessageContent(t, h, task, "memory agent derived fact about NIMBUS", `{"a":1}`, "")
	segmentID := h.recordBoundarySegment(t, task, universalDAGBoundaryFixture{
		kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 1,
		actionKey: "atom-ineligible", derivative: true,
	})

	published, err := NewInteractionDAGPublisher(h.pubPool).PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, published)

	assert.Empty(t, atomRowsForSegment(t, h, segmentID), "derivative segments produce no atoms")
	_, _, _, found := projectionRowForSegment(t, h, segmentID)
	assert.False(t, found, "no projection request without atoms")
	segment := h.segmentRow(segmentID)
	assert.Equal(t, string(SegmentPublished), segment.publishStatus, "the segment itself still publishes")
}

func TestInteractionDAGPublisher_FailedPublishPersistsNoAtoms(t *testing.T) {
	h := newUniversalDAGPublisherHarness(t)
	defer h.Close()
	task := h.createTask(t, h.ctx, 1)
	setTaskMessageContent(t, h, task, "fact about NIMBUS that never lands", `{"a":1}`, "")
	segmentID := h.recordMessageSegment(task, 1, "atom-rollback")

	sink := &classifyingSink{errFor: map[string]error{
		segmentID: fmt.Errorf("storage: %w", ErrDAGPublishTransient),
	}}
	_, err := newPublisherWithSink(t, h, sink).PublishClaim(h.ctx, 10)
	require.NoError(t, err)

	assert.Empty(t, atomRowsForSegment(t, h, segmentID), "a failed publish leaves no atoms behind")
	_, _, _, found := projectionRowForSegment(t, h, segmentID)
	assert.False(t, found)
	segment := h.segmentRow(segmentID)
	assert.Equal(t, string(SegmentRetry), segment.publishStatus)
}

// recordBoundarySegment closes one boundary with the raw fixture (for shapes
// recordMessageSegment cannot express, e.g. derivative segments).
func (h *universalDAGPublisherHarness) recordBoundarySegment(t *testing.T, task db.AgentInboxEvent, fixture universalDAGBoundaryFixture) string {
	t.Helper()
	result, err := h.recordBoundary(h.ctx, h.boundaryInput(task, fixture))
	require.NoError(t, err, "record boundary %s", fixture)
	return result.SegmentID
}
