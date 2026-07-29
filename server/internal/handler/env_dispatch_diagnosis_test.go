package handler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/service"
)

func TestDiagnosisTopologicalSegmentIDs_RespectsEdges(t *testing.T) {
	ordered, err := diagnosisTopologicalSegmentIDs(service.AssembledDag{
		Segments: []service.AssembledSegment{
			{SegmentID: "root"},
			{SegmentID: "child"},
			{SegmentID: "leaf"},
		},
		Edges: []service.AssembledEdge{
			{SrcSegmentID: "root", DstSegmentID: "child"},
			{SrcSegmentID: "child", DstSegmentID: "leaf"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"root", "child", "leaf"}, ordered)
}
