package memorygraph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSourceAppendSegmentFileAndHasAttachment(t *testing.T) {
	store := newTestStore(t)
	attachmentID := uuid.NewString()

	segSeq, err := store.AppendSourceSegment("src-seg-1", "raw segment body")
	if err != nil {
		t.Fatalf("AppendSourceSegment: %v", err)
	}
	if segSeq != 1 {
		t.Fatalf("segment seq = %d, want 1", segSeq)
	}

	fileSeq, fileID, err := store.AppendSourceFile(SourceFileInput{
		AttachmentID:     attachmentID,
		Body:             "file source body",
		BlobSHA256:       "abc123",
		MIME:             "text/plain",
		SizeBytes:        12,
		ExtractionStatus: ExtractionPending,
	})
	if err != nil {
		t.Fatalf("AppendSourceFile: %v", err)
	}
	if fileSeq != 2 {
		t.Fatalf("file seq = %d, want 2", fileSeq)
	}
	if fileID == "" {
		t.Fatal("file source node id is empty")
	}

	if err := store.AppendSourceHasAttachment("src-seg-1", fileID); err != nil {
		t.Fatalf("AppendSourceHasAttachment: %v", err)
	}

	nodes, edges, err := store.LoadSources(2)
	if err != nil {
		t.Fatalf("LoadSources: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(nodes))
	}

	var seg, file *Node
	for _, n := range nodes {
		if n.Level != SourceLayerLevel {
			t.Fatalf("node %s level = %d, want %d", n.NodeID, n.Level, SourceLayerLevel)
		}
		switch n.SourceKind {
		case SourceKindSegment:
			seg = n
		case SourceKindFile:
			file = n
		default:
			t.Fatalf("unexpected source_kind %q on %s", n.SourceKind, n.NodeID)
		}
	}
	if seg == nil || seg.NodeID != "src-seg-1" || seg.Body != "raw segment body" {
		t.Fatalf("segment node = %+v", seg)
	}
	if file == nil || file.NodeID != fileID || file.AttachmentID != attachmentID || file.BlobSHA256 != "abc123" {
		t.Fatalf("file node = %+v", file)
	}

	if len(edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(edges))
	}
	e := edges[0]
	if e.Type != EdgeTypeHasAttachment || e.From != "src-seg-1" || e.To != fileID {
		t.Fatalf("has_attachment edge = %+v, want from src-seg-1 to %s", e, fileID)
	}
	if e.CreatedBy != CreatorIngester {
		t.Fatalf("edge CreatedBy = %q, want %q", e.CreatedBy, CreatorIngester)
	}
}

func TestSourceWatermarkVisibility(t *testing.T) {
	store := newTestStore(t)

	seqA, err := store.AppendSourceSegment("src-a", "source A")
	if err != nil {
		t.Fatalf("append A: %v", err)
	}
	if seqA != 1 {
		t.Fatalf("seq A = %d, want 1", seqA)
	}

	vAfterA, err := store.CreateVersionFrom(1, "test")
	if err != nil {
		t.Fatalf("CreateVersionFrom after A: %v", err)
	}
	mAfterA, err := store.LoadManifest(vAfterA)
	if err != nil {
		t.Fatalf("LoadManifest after A: %v", err)
	}
	if mAfterA.SourceWatermark != 1 {
		t.Fatalf("watermark after A = %d, want 1", mAfterA.SourceWatermark)
	}

	seqB, err := store.AppendSourceSegment("src-b", "source B")
	if err != nil {
		t.Fatalf("append B: %v", err)
	}
	if seqB != 2 {
		t.Fatalf("seq B = %d, want 2", seqB)
	}

	nodes1, _, err := store.LoadSources(1)
	if err != nil {
		t.Fatalf("LoadSources(1): %v", err)
	}
	if got := sourceIDs(nodes1); len(got) != 1 || got[0] != "src-a" {
		t.Fatalf("LoadSources(1) = %v, want [src-a]", got)
	}

	nodes2, _, err := store.LoadSources(2)
	if err != nil {
		t.Fatalf("LoadSources(2): %v", err)
	}
	got2 := sourceIDs(nodes2)
	if len(got2) != 2 || got2[0] != "src-a" || got2[1] != "src-b" {
		t.Fatalf("LoadSources(2) = %v, want [src-a src-b]", got2)
	}

	vAfterBoth, err := store.CreateVersionFrom(vAfterA, "test")
	if err != nil {
		t.Fatalf("CreateVersionFrom after both: %v", err)
	}
	mAfterBoth, err := store.LoadManifest(vAfterBoth)
	if err != nil {
		t.Fatalf("LoadManifest after both: %v", err)
	}
	if mAfterBoth.SourceWatermark != 2 {
		t.Fatalf("watermark after both = %d, want 2", mAfterBoth.SourceWatermark)
	}
}

func TestSourceFileAppendIdempotent(t *testing.T) {
	store := newTestStore(t)
	in := SourceFileInput{
		AttachmentID: uuid.NewString(),
		Body:         "same file",
		BlobSHA256:   "deadbeef",
		MIME:         "application/pdf",
		SizeBytes:    4,
	}

	seq1, id1, err := store.AppendSourceFile(in)
	if err != nil {
		t.Fatalf("first append: %v", err)
	}
	seq2, id2, err := store.AppendSourceFile(in)
	if err != nil {
		t.Fatalf("second append: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("node id changed on re-append: %s -> %s", id1, id2)
	}
	if seq1 != seq2 {
		t.Fatalf("seq changed on re-append: %d -> %d", seq1, seq2)
	}

	journal := readSourceJournal(t, store)
	if len(journal) != 1 {
		t.Fatalf("journal entries = %d, want 1 (no duplicate on re-append)", len(journal))
	}

	nodes, _, err := store.LoadSources(seq1)
	if err != nil {
		t.Fatalf("LoadSources: %v", err)
	}
	if len(nodes) != 1 || nodes[0].NodeID != id1 {
		t.Fatalf("LoadSources after re-append = %+v, want one node %s", nodes, id1)
	}
}

func TestSourceCrossScopeBlobDedupDoesNotMergeIdentity(t *testing.T) {
	const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	attachmentID := uuid.NewString()

	storeA := newTestStore(t)
	storeB := newTestStore(t)

	_, idA, err := storeA.AppendSourceFile(SourceFileInput{
		AttachmentID: attachmentID,
		Body:         "shared bytes in scope A",
		BlobSHA256:   sha,
		MIME:         "image/png",
		SizeBytes:    8,
	})
	if err != nil {
		t.Fatalf("append A: %v", err)
	}
	_, idB, err := storeB.AppendSourceFile(SourceFileInput{
		AttachmentID: attachmentID,
		Body:         "shared bytes in scope B",
		BlobSHA256:   sha,
		MIME:         "image/png",
		SizeBytes:    8,
	})
	if err != nil {
		t.Fatalf("append B: %v", err)
	}
	if idA == idB {
		t.Fatalf("cross-scope file source ids merged: both %s", idA)
	}

	nodesA, _, err := storeA.LoadSources(1)
	if err != nil || len(nodesA) != 1 {
		t.Fatalf("scope A sources = %v, %v", nodesA, err)
	}
	nodesB, _, err := storeB.LoadSources(1)
	if err != nil || len(nodesB) != 1 {
		t.Fatalf("scope B sources = %v, %v", nodesB, err)
	}
	if nodesA[0].BlobSHA256 != sha || nodesB[0].BlobSHA256 != sha {
		t.Fatalf("blob sha mismatch: A=%q B=%q want %q", nodesA[0].BlobSHA256, nodesB[0].BlobSHA256, sha)
	}
	if nodesA[0].NodeID == nodesB[0].NodeID {
		t.Fatalf("node identity merged across scopes: %s", nodesA[0].NodeID)
	}
}

func TestSourceLayerImmutableUnderManagement(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "stmt-1", "ordinary statement")

	if _, err := store.AppendSourceSegment("src-seg-imm", "immutable segment"); err != nil {
		t.Fatalf("AppendSourceSegment: %v", err)
	}
	_, fileID, err := store.AppendSourceFile(SourceFileInput{
		AttachmentID: uuid.NewString(),
		Body:         "immutable file",
		BlobSHA256:   "bbb",
		MIME:         "text/plain",
		SizeBytes:    1,
	})
	if err != nil {
		t.Fatalf("AppendSourceFile: %v", err)
	}
	if err := store.AppendSourceHasAttachment("src-seg-imm", fileID); err != nil {
		t.Fatalf("AppendSourceHasAttachment: %v", err)
	}
	_, srcEdges, err := store.LoadSources(2)
	if err != nil || len(srcEdges) != 1 {
		t.Fatalf("LoadSources edges = %v, %v", srcEdges, err)
	}
	attachID := srcEdges[0].EdgeID

	g, err := LoadGraph(store, 1)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	beforeNodes := nodeBodies(g)
	beforeHier := len(g.HierarchyEdges())
	beforeRel := len(g.RelationEdges())

	cfg := DefaultConsolidateConfig()
	cfg.TTVTrajectories = 1
	c := NewConsolidator(store, nil, cfg, testConsolidateScope(), nil, nil)
	ops := []ConsolidateOp{
		{Op: OpUpdateNode, NodeID: "src-seg-imm", Node: &Node{NodeID: "src-seg-imm", Body: "mutated"}},
		{Op: OpDeleteNode, NodeID: "src-seg-imm"},
		{Op: OpDeleteEdge, EdgeID: attachID},
		{Op: OpUpdateEdge, EdgeID: attachID},
		{Op: OpAddRelationEdge, Edge: &Edge{EdgeID: "mgmt-ha", Type: EdgeTypeHasAttachment, From: "src-seg-imm", To: fileID}},
		{Op: OpDeleteNode, NodeID: fileID},
		{Op: OpUpdateNode, NodeID: fileID, Node: &Node{NodeID: fileID, Body: "mutated file"}},
	}
	applied, rejected, err := c.applyOperations(g, 1, CreatorConsolidator, ops)
	if err != nil {
		t.Fatalf("applyOperations: %v", err)
	}
	if applied != 0 {
		t.Fatalf("OpsApplied = %d, want 0", applied)
	}
	if len(rejected) != len(ops) {
		t.Fatalf("rejected = %d, want %d: %+v", len(rejected), len(ops), rejected)
	}
	for i, r := range rejected {
		if r.Op != ops[i].Op {
			t.Fatalf("rejected[%d].Op = %q, want %q", i, r.Op, ops[i].Op)
		}
		if !strings.Contains(r.Reason, "source_layer_immutable") {
			t.Fatalf("rejected[%d] reason = %q, want source_layer_immutable", i, r.Reason)
		}
	}

	if got := nodeBodies(g); len(got) != len(beforeNodes) {
		t.Fatalf("graph node count changed: %v -> %v", beforeNodes, got)
	}
	for id, body := range beforeNodes {
		if got := g.Node(id); got == nil || got.Body != body {
			t.Fatalf("node %s changed: %+v", id, got)
		}
	}
	if len(g.HierarchyEdges()) != beforeHier || len(g.RelationEdges()) != beforeRel {
		t.Fatalf("graph edges changed: hier %d rel %d", len(g.HierarchyEdges()), len(g.RelationEdges()))
	}
	if g.Node("stmt-1") == nil || g.Node("stmt-1").Body != "ordinary statement" {
		t.Fatal("ordinary statement node was mutated")
	}

	srcNodes, srcEdgesAfter, err := store.LoadSources(2)
	if err != nil {
		t.Fatalf("LoadSources after management: %v", err)
	}
	if len(srcNodes) != 2 || len(srcEdgesAfter) != 1 {
		t.Fatalf("source store mutated: nodes=%d edges=%d", len(srcNodes), len(srcEdgesAfter))
	}
}

func TestSourceQuotaExemptionHelpers(t *testing.T) {
	seg := &Node{NodeID: "src-seg", Level: SourceLayerLevel, SourceKind: SourceKindSegment, CreatedBy: CreatorIngester}
	file := &Node{NodeID: "src-file", Level: SourceLayerLevel, SourceKind: SourceKindFile, CreatedBy: CreatorIngester}
	stmt := &Node{NodeID: "stmt", Level: 0, CreatedBy: CreatorConsolidator}
	parent := &Node{NodeID: "parent", Level: 1, CreatedBy: CreatorConsolidator}

	if !IsSourceLayerNode(seg) || !IsSourceLayerNode(file) {
		t.Fatal("source nodes should be identified as source layer")
	}
	if IsSourceLayerNode(stmt) || IsSourceLayerNode(parent) || IsSourceLayerNode(nil) {
		t.Fatal("ordinary nodes must not be source layer")
	}

	ha := &Edge{EdgeID: "ha1", Type: EdgeTypeHasAttachment, From: "src-seg", To: "src-file", CreatedBy: CreatorIngester}
	sum := &Edge{EdgeID: "h1", Type: EdgeTypeSummarizes, From: "parent", To: "stmt", CreatedBy: CreatorConsolidator}
	rel := &Edge{EdgeID: "r1", Type: EdgeTypeSupports, From: "parent", To: "stmt", CreatedBy: CreatorConsolidator}
	if !IsSourceProvenanceEdge(ha) {
		t.Fatal("has_attachment from ingester should be provenance")
	}
	if IsSourceProvenanceEdge(sum) || IsSourceProvenanceEdge(rel) || IsSourceProvenanceEdge(nil) {
		t.Fatal("ordinary edges must not be source provenance")
	}

	g := newGraph()
	for _, n := range []*Node{seg, file, stmt, parent} {
		if err := g.AddNode(n); err != nil {
			t.Fatalf("AddNode %s: %v", n.NodeID, err)
		}
	}
	g.hier = []*Edge{sum}
	g.rel = []*Edge{ha, rel}
	g.rebuild()

	if got := CountableHierarchyFanout(g, "parent"); got != 1 {
		t.Fatalf("CountableHierarchyFanout(parent) = %d, want 1 (source layer skipped)", got)
	}
	if got := CountableRelationDegree(g, "parent"); got != 1 {
		t.Fatalf("CountableRelationDegree(parent) = %d, want 1 (has_attachment skipped)", got)
	}
	if got := CountableRelationDegree(g, "src-seg"); got != 0 {
		t.Fatalf("CountableRelationDegree(src-seg) = %d, want 0", got)
	}
}

func TestSourceCorruptJournalFailsClosed(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.AppendSourceSegment("src-ok", "ok"); err != nil {
		t.Fatalf("append: %v", err)
	}
	path := filepath.Join(store.Root, "shared", "sources", "journal.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	// Truncate the published line mid-JSON and append garbage so a parser
	// that skipped bad lines could still return the earlier record.
	corrupt := strings.TrimRight(string(b), "\n")
	if len(corrupt) > 8 {
		corrupt = corrupt[:len(corrupt)/2]
	}
	corrupt += "\n{not-json\n"
	if err := os.WriteFile(path, []byte(corrupt), 0o644); err != nil {
		t.Fatalf("write corrupt journal: %v", err)
	}

	nodes, edges, err := store.LoadSources(10)
	if err == nil {
		t.Fatal("LoadSources on corrupt journal: want error")
	}
	if nodes != nil || edges != nil {
		t.Fatalf("corrupt journal returned partial results: nodes=%v edges=%v", nodes, edges)
	}
}

func TestSourceOldManifestMissingWatermarkLoadsZero(t *testing.T) {
	store := newTestStore(t)
	legacy := map[string]any{
		"version":         1,
		"parent_version":  0,
		"created_at":      time.Now().UTC().Format(time.RFC3339Nano),
		"created_by":      "init",
		"node_count":      0,
		"hier_edge_count": 0,
		"rel_edge_count":  0,
	}
	body, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatalf("marshal legacy manifest: %v", err)
	}
	path := filepath.Join(store.VersionDir(1), "manifest.json")
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatalf("write legacy manifest: %v", err)
	}

	m, err := store.LoadManifest(1)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.SourceWatermark != 0 {
		t.Fatalf("SourceWatermark = %d, want 0", m.SourceWatermark)
	}
	if m.Version != 1 || m.CreatedBy != "init" {
		t.Fatalf("legacy manifest fields lost: %+v", m)
	}
}

func sourceIDs(nodes []*Node) []string {
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.NodeID)
	}
	return ids
}

func nodeBodies(g *Graph) map[string]string {
	out := map[string]string{}
	for _, n := range g.Nodes() {
		out[n.NodeID] = n.Body
	}
	return out
}

func readSourceJournal(t *testing.T, store *Store) []map[string]any {
	t.Helper()
	path := filepath.Join(store.Root, "shared", "sources", "journal.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	var out []map[string]any
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			t.Fatalf("parse journal line %q: %v", line, err)
		}
		out = append(out, item)
	}
	return out
}
