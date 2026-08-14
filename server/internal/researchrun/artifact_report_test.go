package researchrun

import (
	"encoding/json"
	"testing"
)

func TestReportRevisionArtifactContentCanonicalizesStructuredAndBindsAuthor(t *testing.T) {
	t.Parallel()

	first, err := ArtifactContentHash(
		ArtifactKindReportRevision,
		reportRevisionArtifactContent(json.RawMessage(`{
			"revision":2,"content_md":"report","structured":{"b":2,"a":1},
			"author_agent_id":"agent-a"
		}`)),
	)
	if err != nil {
		t.Fatal(err)
	}
	equivalent, err := ArtifactContentHash(
		ArtifactKindReportRevision,
		reportRevisionArtifactContent(json.RawMessage(`{
			"author_agent_id":"agent-a","structured":{"a":1,"b":2},
			"content_md":"report","revision":2
		}`)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first != equivalent {
		t.Fatalf("object key order changed hash: %q != %q", first, equivalent)
	}

	otherAuthor, err := ArtifactContentHash(
		ArtifactKindReportRevision,
		reportRevisionArtifactContent(json.RawMessage(`{
			"author_agent_id":"agent-b","structured":{"a":1,"b":2},
			"content_md":"report","revision":2
		}`)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first == otherAuthor {
		t.Fatal("author change must change content hash")
	}
}
