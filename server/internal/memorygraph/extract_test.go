package memorygraph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestExtractionRecordRequiresPublishedSource(t *testing.T) {
	store := newTestStore(t)
	missing := uuid.NewString()

	if _, err := store.RecordExtraction(missing, sampleExtraction("caption")); err == nil {
		t.Fatal("RecordExtraction on missing source: want error")
	}
	listed, err := store.ListExtractions(missing)
	if err != nil {
		t.Fatalf("ListExtractions missing source: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("ListExtractions after failed record = %+v, want empty", listed)
	}

	fileID := publishFileSource(t, store)
	gen, err := store.RecordExtraction(fileID, sampleExtraction("caption"))
	if err != nil {
		t.Fatalf("RecordExtraction after publish: %v", err)
	}
	if gen != 1 {
		t.Fatalf("gen = %d, want 1", gen)
	}
}

func TestExtractionPendingFileSourceNeverBlocks(t *testing.T) {
	store := newTestStore(t)
	attachmentID := uuid.NewString()

	seq, fileID, err := store.AppendSourceFile(SourceFileInput{
		AttachmentID:     attachmentID,
		Body:             "pending file body",
		BlobSHA256:       "abc123",
		MIME:             "image/png",
		SizeBytes:        12,
		ExtractionStatus: ExtractionPending,
	})
	if err != nil {
		t.Fatalf("AppendSourceFile: %v", err)
	}
	if seq != 1 || fileID == "" {
		t.Fatalf("seq=%d fileID=%q, want seq 1 and a node id", seq, fileID)
	}

	nodes, _, err := store.LoadSources(seq)
	if err != nil {
		t.Fatalf("LoadSources: %v", err)
	}
	var file *Node
	for _, n := range nodes {
		if n.NodeID == fileID {
			file = n
			break
		}
	}
	if file == nil {
		t.Fatal("pending file source not returned by LoadSources")
	}
	if file.ExtractionStatus != ExtractionPending {
		t.Fatalf("ExtractionStatus = %q, want %q", file.ExtractionStatus, ExtractionPending)
	}
}

func TestNormalizeDescriptionKind(t *testing.T) {
	cases := []struct {
		raw       string
		canonical string
		known     bool
	}{
		{"caption", DescriptionKindCaption, true},
		{" Caption ", DescriptionKindCaption, true},
		{"OCR", DescriptionKindOCR, true},
		{"  ocr  ", DescriptionKindOCR, true},
		{"TRANSCRIPT", DescriptionKindTranscript, true},
		{"transcript", DescriptionKindTranscript, true},
		{"  Extracted_Text  ", DescriptionKindExtractedText, true},
		{"extracted_text", DescriptionKindExtractedText, true},
		{"vision_caption_v9", "vision_caption_v9", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, known := NormalizeDescriptionKind(tc.raw)
		if got != tc.canonical || known != tc.known {
			t.Errorf("NormalizeDescriptionKind(%q) = (%q, %v), want (%q, %v)",
				tc.raw, got, known, tc.canonical, tc.known)
		}
	}
	// Unknown kinds must be retained verbatim, not lowercased or remapped.
	raw := "Vision_Caption_V9"
	got, known := NormalizeDescriptionKind(raw)
	if known {
		t.Fatalf("NormalizeDescriptionKind(%q) known=true, want false", raw)
	}
	if got != raw {
		t.Fatalf("unknown kind remapped: got %q, want verbatim %q", got, raw)
	}
}

func TestExtractionGenerationsImmutable(t *testing.T) {
	store := newTestStore(t)
	fileID := publishFileSource(t, store)

	in1 := sampleExtraction("ocr")
	in1.ModelVersion = "v1"
	in1.Output = "first generation output"
	gen1, err := store.RecordExtraction(fileID, in1)
	if err != nil {
		t.Fatalf("record gen1: %v", err)
	}
	if gen1 != 1 {
		t.Fatalf("gen1 = %d, want 1", gen1)
	}

	listed, err := store.ListExtractions(fileID)
	if err != nil {
		t.Fatalf("ListExtractions after gen1: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed = %d, want 1", len(listed))
	}
	artPath := filepath.Join(store.Root, listed[0].Artifact)
	bytes1, err := os.ReadFile(artPath)
	if err != nil {
		t.Fatalf("read gen1 artifact: %v", err)
	}

	in2 := sampleExtraction("ocr")
	in2.ModelVersion = "v2"
	in2.Output = "second generation output"
	gen2, err := store.RecordExtraction(fileID, in2)
	if err != nil {
		t.Fatalf("record gen2: %v", err)
	}
	if gen2 != 2 {
		t.Fatalf("gen2 = %d, want 2", gen2)
	}

	bytes1After, err := os.ReadFile(artPath)
	if err != nil {
		t.Fatalf("re-read gen1 artifact: %v", err)
	}
	if string(bytes1After) != string(bytes1) {
		t.Fatalf("gen1 artifact mutated after gen2 write")
	}

	listed, err = store.ListExtractions(fileID)
	if err != nil {
		t.Fatalf("ListExtractions after gen2: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed = %d, want 2", len(listed))
	}
	if listed[0].Gen != 1 || listed[1].Gen != 2 {
		t.Fatalf("gen order = [%d, %d], want [1, 2]", listed[0].Gen, listed[1].Gen)
	}

	art1, err := store.LoadExtractionArtifact(fileID, 1)
	if err != nil {
		t.Fatalf("LoadExtractionArtifact gen1: %v", err)
	}
	if art1.Output != in1.Output || art1.ModelVersion != "v1" {
		t.Fatalf("gen1 artifact = %+v, want output/model v1", art1)
	}
	art2, err := store.LoadExtractionArtifact(fileID, 2)
	if err != nil {
		t.Fatalf("LoadExtractionArtifact gen2: %v", err)
	}
	if art2.Output != in2.Output || art2.ModelVersion != "v2" {
		t.Fatalf("gen2 artifact = %+v, want output/model v2", art2)
	}
}

func TestExtractionDurabilityCrashBeforeIndex(t *testing.T) {
	store := newTestStore(t)
	fileID := publishFileSource(t, store)
	root := store.Root

	store.testHookBeforeExtractionIndex = func() {
		panic("simulated crash before extraction index")
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("RecordExtraction: want panic from crash hook")
			}
		}()
		if _, err := store.RecordExtraction(fileID, sampleExtractionWithOutput("caption", "crash-output")); err != nil {
			t.Fatalf("RecordExtraction returned error instead of panicking: %v", err)
		}
		t.Fatal("RecordExtraction completed; want crash before index")
	}()

	fresh := NewStore(root)
	listed, err := fresh.ListExtractions(fileID)
	if err != nil {
		t.Fatalf("ListExtractions after crash: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("incomplete generation visible after crash: %+v", listed)
	}

	gen, err := fresh.RecordExtraction(fileID, sampleExtractionWithOutput("caption", "retry-output"))
	if err != nil {
		t.Fatalf("RecordExtraction after crash: %v", err)
	}
	if gen < 1 {
		t.Fatalf("retry gen = %d, want >= 1", gen)
	}

	listed, err = fresh.ListExtractions(fileID)
	if err != nil {
		t.Fatalf("ListExtractions after retry: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed after retry = %d, want 1 (orphan must not be adopted)", len(listed))
	}
	art, err := fresh.LoadExtractionArtifact(fileID, listed[0].Gen)
	if err != nil {
		t.Fatalf("LoadExtractionArtifact retry gen: %v", err)
	}
	if art.Output != "retry-output" {
		t.Fatalf("adopted output = %q, want retry-output (orphan must not be adopted)", art.Output)
	}
}

func TestExtractionCorruptIndexedArtifactFailsClosed(t *testing.T) {
	store := newTestStore(t)
	fileID := publishFileSource(t, store)
	if _, err := store.RecordExtraction(fileID, sampleExtraction("transcript")); err != nil {
		t.Fatalf("RecordExtraction: %v", err)
	}
	listed, err := store.ListExtractions(fileID)
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListExtractions = %+v, %v", listed, err)
	}

	artPath := filepath.Join(store.Root, listed[0].Artifact)
	if err := os.WriteFile(artPath, []byte("{not-json"), 0o644); err != nil {
		t.Fatalf("corrupt artifact: %v", err)
	}
	if _, err := store.LoadExtractionArtifact(fileID, listed[0].Gen); err == nil {
		t.Fatal("LoadExtractionArtifact on corrupt file: want error")
	}

	if err := os.Remove(artPath); err != nil {
		t.Fatalf("remove artifact: %v", err)
	}
	if _, err := store.LoadExtractionArtifact(fileID, listed[0].Gen); err == nil {
		t.Fatal("LoadExtractionArtifact on missing file: want error")
	}
}

func TestExtractionFailedAndUnsupportedStillRecorded(t *testing.T) {
	store := newTestStore(t)
	fileID := publishFileSource(t, store)

	for _, tc := range []struct {
		status string
		output string
	}{
		{ExtractionFailed, "extractor crashed: disk full"},
		{ExtractionUnsupported, "mime not supported"},
	} {
		in := sampleExtraction("caption")
		in.Status = tc.status
		in.Output = tc.output
		gen, err := store.RecordExtraction(fileID, in)
		if err != nil {
			t.Fatalf("RecordExtraction status %q: %v", tc.status, err)
		}
		listed, err := store.ListExtractions(fileID)
		if err != nil {
			t.Fatalf("ListExtractions: %v", err)
		}
		var found *ExtractionIndexEntry
		for i := range listed {
			if listed[i].Gen == gen {
				found = &listed[i]
				break
			}
		}
		if found == nil {
			t.Fatalf("gen %d not indexed for status %q", gen, tc.status)
		}
		if found.Status != tc.status {
			t.Fatalf("index status = %q, want %q", found.Status, tc.status)
		}
		art, err := store.LoadExtractionArtifact(fileID, gen)
		if err != nil {
			t.Fatalf("LoadExtractionArtifact gen %d: %v", gen, err)
		}
		if art.Status != tc.status || art.Output != tc.output {
			t.Fatalf("artifact = %+v, want status %q output %q", art, tc.status, tc.output)
		}
	}

	before, err := store.ListExtractions(fileID)
	if err != nil {
		t.Fatalf("ListExtractions before validation: %v", err)
	}
	if _, err := store.RecordExtraction(fileID, ExtractionInput{Kind: "", Extractor: "tesseract", Status: "completed"}); err == nil {
		t.Fatal("empty kind: want validation error")
	}
	if _, err := store.RecordExtraction(fileID, ExtractionInput{Kind: "caption", Extractor: "", Status: "completed"}); err == nil {
		t.Fatal("empty extractor: want validation error")
	}
	after, err := store.ListExtractions(fileID)
	if err != nil {
		t.Fatalf("ListExtractions after validation: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("validation error wrote generations: before %d after %d", len(before), len(after))
	}
}

func TestDescriptionNodePublishAndRoundTrip(t *testing.T) {
	store := newTestStore(t)
	fileID := publishFileSource(t, store)
	in := sampleExtraction("caption")
	in.Language = "zh"
	in.Coverage = "pages 1-3"
	in.Provider = "local"
	in.Model = "cap-v1"
	in.ModelVersion = "2026-01"
	gen, err := store.RecordExtraction(fileID, in)
	if err != nil {
		t.Fatalf("RecordExtraction: %v", err)
	}

	if err := store.PublishDescriptionNode(1, fileID, 99, "desc-missing-gen", "body"); err == nil {
		t.Fatal("PublishDescriptionNode missing gen: want error")
	}

	const nodeID = "desc-caption-1"
	const body = "a photo of a whiteboard with equations"
	if err := store.PublishDescriptionNode(1, fileID, gen, nodeID, body); err != nil {
		t.Fatalf("PublishDescriptionNode: %v", err)
	}

	nodes, err := store.LoadNodes(1)
	if err != nil {
		t.Fatalf("LoadNodes: %v", err)
	}
	var n *Node
	for _, candidate := range nodes {
		if candidate.NodeID == nodeID {
			n = candidate
			break
		}
	}
	if n == nil {
		t.Fatal("description node not in LoadNodes")
	}
	if n.Level != 0 {
		t.Fatalf("level = %d, want 0", n.Level)
	}
	if n.Body != body {
		t.Fatalf("body = %q, want %q", n.Body, body)
	}
	if n.Extraction == nil {
		t.Fatal("Extraction meta is nil")
	}
	ex := n.Extraction
	if ex.SourceRef != fileID {
		t.Fatalf("source_ref = %q, want %q", ex.SourceRef, fileID)
	}
	if ex.Kind != DescriptionKindCaption || !ex.KindKnown {
		t.Fatalf("kind = %q known=%v, want caption/true", ex.Kind, ex.KindKnown)
	}
	if ex.Extractor != in.Extractor || ex.Provider != in.Provider || ex.Model != in.Model || ex.ModelVersion != in.ModelVersion {
		t.Fatalf("extractor identity = %+v", ex)
	}
	if ex.Language != in.Language || ex.Coverage != in.Coverage {
		t.Fatalf("language/coverage = %q/%q", ex.Language, ex.Coverage)
	}
	if ex.Generation != gen {
		t.Fatalf("generation = %d, want %d", ex.Generation, gen)
	}
	if ex.ArtifactRef == "" {
		t.Fatal("artifact_ref is empty")
	}
	if _, err := os.Stat(filepath.Join(store.Root, ex.ArtifactRef)); err != nil {
		t.Fatalf("artifact_ref %q not on disk: %v", ex.ArtifactRef, err)
	}
}

func TestDescriptionExtractionIdentityGuard(t *testing.T) {
	store := newTestStore(t)
	fileID := publishFileSource(t, store)
	gen, err := store.RecordExtraction(fileID, sampleExtraction("extracted_text"))
	if err != nil {
		t.Fatalf("RecordExtraction: %v", err)
	}
	const nodeID = "desc-guard"
	if err := store.PublishDescriptionNode(1, fileID, gen, nodeID, "original description"); err != nil {
		t.Fatalf("PublishDescriptionNode: %v", err)
	}

	g, err := LoadGraph(store, 1)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	existing := g.Node(nodeID)
	if existing == nil || existing.Extraction == nil {
		t.Fatal("description node missing from graph")
	}
	origRef := existing.Extraction.ArtifactRef
	origSrc := existing.Extraction.SourceRef
	origGen := existing.Extraction.Generation
	origBody := existing.Body

	cfg := DefaultConsolidateConfig()
	cfg.TTVTrajectories = 1
	c := NewConsolidator(store, nil, cfg, testConsolidateScope(), nil, nil)

	cleared := *existing
	em := *existing.Extraction
	em.ArtifactRef = ""
	cleared.Extraction = &em
	cleared.Body = "cleared identity"
	applied, rejected, err := c.applyOperations(g, 1, CreatorConsolidator, []ConsolidateOp{
		{Op: OpUpdateNode, NodeID: nodeID, Node: &cleared},
	})
	if err != nil {
		t.Fatalf("applyOperations clear: %v", err)
	}
	if applied != 0 {
		t.Fatalf("applied = %d, want 0", applied)
	}
	if len(rejected) != 1 {
		t.Fatalf("rejected = %+v, want 1", rejected)
	}
	if !strings.Contains(rejected[0].Reason, "extraction") {
		t.Fatalf("reject reason = %q, want extraction identity", rejected[0].Reason)
	}
	got := g.Node(nodeID)
	if got.Body != origBody || got.Extraction == nil || got.Extraction.ArtifactRef != origRef || got.Extraction.SourceRef != origSrc || got.Extraction.Generation != origGen {
		t.Fatalf("graph mutated on rejected identity clear: %+v", got)
	}

	changed := *existing
	em2 := *existing.Extraction
	em2.Generation = origGen + 7
	changed.Extraction = &em2
	changed.Body = "changed generation"
	applied, rejected, err = c.applyOperations(g, 1, CreatorConsolidator, []ConsolidateOp{
		{Op: OpUpdateNode, NodeID: nodeID, Node: &changed},
	})
	if err != nil {
		t.Fatalf("applyOperations generation: %v", err)
	}
	if applied != 0 || len(rejected) != 1 {
		t.Fatalf("generation change applied=%d rejected=%+v", applied, rejected)
	}
	got = g.Node(nodeID)
	if got.Body != origBody || got.Extraction.Generation != origGen {
		t.Fatalf("graph mutated on rejected generation change: %+v", got)
	}

	applied, rejected, err = c.applyOperations(g, 1, CreatorConsolidator, []ConsolidateOp{
		{Op: OpUpdateNode, NodeID: nodeID, Node: &Node{NodeID: nodeID, Body: "body only edit"}},
	})
	if err != nil {
		t.Fatalf("applyOperations body: %v", err)
	}
	if applied != 1 || len(rejected) != 0 {
		t.Fatalf("body edit applied=%d rejected=%+v, want 1/empty", applied, rejected)
	}
	got = g.Node(nodeID)
	if got.Body != "body only edit" {
		t.Fatalf("body = %q, want body-only edit", got.Body)
	}
	if got.Extraction == nil || got.Extraction.ArtifactRef != origRef || got.Extraction.SourceRef != origSrc || got.Extraction.Generation != origGen {
		t.Fatalf("extraction identity lost on body edit: %+v", got.Extraction)
	}
}

func publishFileSource(t *testing.T, store *Store) string {
	t.Helper()
	_, id, err := store.AppendSourceFile(SourceFileInput{
		AttachmentID:     uuid.NewString(),
		Body:             "file source body",
		BlobSHA256:       "abc123",
		MIME:             "text/plain",
		SizeBytes:        16,
		ExtractionStatus: ExtractionPending,
	})
	if err != nil {
		t.Fatalf("AppendSourceFile: %v", err)
	}
	return id
}

func sampleExtraction(kind string) ExtractionInput {
	return sampleExtractionWithOutput(kind, "extracted output")
}

func sampleExtractionWithOutput(kind, output string) ExtractionInput {
	return ExtractionInput{
		Kind:         kind,
		Extractor:    "tesseract",
		Provider:     "local",
		Model:        "tess",
		ModelVersion: "v1",
		Language:     "en",
		Coverage:     "pages 1-3",
		Output:       output,
		Status:       "completed",
	}
}
