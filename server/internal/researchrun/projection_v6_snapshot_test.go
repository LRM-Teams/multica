package researchrun

import (
	"reflect"
	"testing"
)

func TestCompactV6ProjectionEdgesDropsDerivedFromWhenAbsorptionCoversPair(t *testing.T) {
	t.Parallel()

	edges := []V6ProjectionEdge{
		{ID: "absorb", Kind: "absorbed_into", FromNodeID: "input", ToNodeID: "successor"},
		{ID: "derive-dup", Kind: "derived_from", FromNodeID: "input", ToNodeID: "successor"},
		{ID: "derive-other", Kind: "derived_from", FromNodeID: "input", ToNodeID: "peer"},
		{ID: "belongs", Kind: "belongs_to", FromNodeID: "work", ToNodeID: "goal"},
	}

	got := compactV6ProjectionEdges(edges)
	want := []V6ProjectionEdge{
		{ID: "absorb", Kind: "absorbed_into", FromNodeID: "input", ToNodeID: "successor"},
		{ID: "derive-other", Kind: "derived_from", FromNodeID: "input", ToNodeID: "peer"},
		{ID: "belongs", Kind: "belongs_to", FromNodeID: "work", ToNodeID: "goal"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("compactV6ProjectionEdges() = %#v, want %#v", got, want)
	}
}
