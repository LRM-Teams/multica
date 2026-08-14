package researchrun

import (
	"encoding/json"
	"testing"
)

func TestGraphArtifactContentCanonicalizesPersistedJSON(t *testing.T) {
	t.Parallel()

	left, err := ArtifactContentHash(ArtifactKindGraphNode, graphArtifactContent(json.RawMessage(`{
		"title":"finding","payload":{"b":2,"a":1},"merged_from":["n-2","n-1"]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	right, err := ArtifactContentHash(ArtifactKindGraphNode, graphArtifactContent(json.RawMessage(`{
		"merged_from":["n-2","n-1"],"payload":{"a":1,"b":2},"title":"finding"
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("object key order changed hash: %q != %q", left, right)
	}

	reorderedLineage, err := ArtifactContentHash(ArtifactKindGraphNode, graphArtifactContent(json.RawMessage(`{
		"merged_from":["n-1","n-2"],"payload":{"a":1,"b":2},"title":"finding"
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	if left == reorderedLineage {
		t.Fatal("semantic lineage order must remain hash-bound")
	}
}

func TestGraphEdgeArtifactContentBindsEndpoints(t *testing.T) {
	t.Parallel()

	hash, err := ArtifactContentHash(ArtifactKindGraphEdge, graphArtifactContent(json.RawMessage(`{
		"from_node_id":"node-a","to_node_id":"node-b","edge_type":"supports"
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	changed, err := ArtifactContentHash(ArtifactKindGraphEdge, graphArtifactContent(json.RawMessage(`{
		"from_node_id":"node-a","to_node_id":"node-c","edge_type":"supports"
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	if hash == changed {
		t.Fatal("edge endpoint change must change content hash")
	}
}
