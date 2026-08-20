package memorygraph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSourcePublishTwoPhaseHappyPath(t *testing.T) {
	store := newTestStore(t)

	seq, err := store.AppendSourceSegment("src-pub", "published body")
	if err != nil {
		t.Fatalf("AppendSourceSegment: %v", err)
	}
	if seq != 1 {
		t.Fatalf("seq = %d, want 1", seq)
	}

	nodePath := filepath.Join(store.Root, "shared", "sources", "nodes", "src-pub.md")
	if _, err := os.Stat(nodePath); err != nil {
		t.Fatalf("node file missing from nodes/: %v", err)
	}
	pendingPath := filepath.Join(store.Root, "shared", "sources", "pending", "src-pub.md")
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatalf("pending file still present after publish: %v", err)
	}
	journal := readSourceJournal(t, store)
	if len(journal) != 1 {
		t.Fatalf("journal entries = %d, want 1", len(journal))
	}
	if journal[0]["source_id"] != "src-pub" {
		t.Fatalf("journal source_id = %v, want src-pub", journal[0]["source_id"])
	}

	nodes, _, err := store.LoadSources(1)
	if err != nil {
		t.Fatalf("LoadSources: %v", err)
	}
	if len(nodes) != 1 || nodes[0].NodeID != "src-pub" || nodes[0].Body != "published body" {
		t.Fatalf("LoadSources = %+v, want src-pub", nodes)
	}
}

func TestSourcePublishCrashBeforeJournal(t *testing.T) {
	store := newTestStore(t)
	root := store.Root
	store.testHookBeforeJournal = func() {
		panic("simulated crash before journal")
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("AppendSourceSegment: want panic from crash hook")
			}
		}()
		if _, err := store.AppendSourceSegment("src-crash", "half published"); err != nil {
			t.Fatalf("AppendSourceSegment returned error instead of panicking: %v", err)
		}
		t.Fatal("AppendSourceSegment completed; want crash before journal")
	}()

	fresh := NewStore(root)
	nodes, edges, err := fresh.LoadSources(10)
	if err != nil {
		t.Fatalf("LoadSources after crash: %v", err)
	}
	if len(nodes) != 0 || len(edges) != 0 {
		t.Fatalf("half-published source visible: nodes=%v edges=%v", nodes, edges)
	}
	seq, err := fresh.CurrentSourceSeq()
	if err != nil {
		t.Fatalf("CurrentSourceSeq: %v", err)
	}
	if seq != 0 {
		t.Fatalf("journal seq after crash = %d, want 0", seq)
	}
	if journalExistsWithEntries(t, fresh) {
		t.Fatal("journal must stay empty after crash before commit")
	}

	seq2, err := fresh.AppendSourceSegment("src-next", "next body")
	if err != nil {
		t.Fatalf("append after crash: %v", err)
	}
	if seq2 != 1 {
		t.Fatalf("next seq = %d, want 1", seq2)
	}
	pendingDir := filepath.Join(root, "shared", "sources", "pending")
	entries, err := os.ReadDir(pendingDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read pending: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("pending leftover after next publish: %v", entryNames(entries))
	}

	nodes, _, err = fresh.LoadSources(1)
	if err != nil {
		t.Fatalf("LoadSources after recovery publish: %v", err)
	}
	if got := sourceIDs(nodes); len(got) != 1 || got[0] != "src-next" {
		t.Fatalf("LoadSources after recovery = %v, want [src-next]", got)
	}
}

func TestSourceQuarantineOrphanNode(t *testing.T) {
	store := newTestStore(t)
	orphanID := "src-orphan"
	if err := os.MkdirAll(store.sourceNodesDir(), 0o755); err != nil {
		t.Fatalf("mkdir nodes: %v", err)
	}
	content := "---\nnode_id: " + orphanID + "\nlevel: -1\nsource_kind: segment\ncreated_by: ingester\ntemporal_status: current\n---\n\norphan body\n"
	if err := os.WriteFile(filepath.Join(store.sourceNodesDir(), orphanID+".md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write orphan: %v", err)
	}

	nodes, edges, err := store.LoadSources(10)
	if err != nil {
		t.Fatalf("LoadSources: %v", err)
	}
	if len(nodes) != 0 || len(edges) != 0 {
		t.Fatalf("orphan visible via LoadSources: nodes=%v edges=%v", nodes, edges)
	}

	findings, err := store.AuditSources()
	if err != nil {
		t.Fatalf("AuditSources: %v", err)
	}
	if !hasAuditKind(findings, "orphan_node", orphanID) {
		t.Fatalf("AuditSources = %+v, want orphan_node %s", findings, orphanID)
	}

	again, err := store.AuditSources()
	if err != nil {
		t.Fatalf("AuditSources rerun: %v", err)
	}
	if !hasAuditKind(again, "orphan_node", orphanID) {
		t.Fatalf("rerun AuditSources = %+v, want orphan_node %s", again, orphanID)
	}
	if n := countQuarantineFindings(t, store, "orphan_node", orphanID); n != 1 {
		t.Fatalf("quarantine.jsonl lines for orphan_node/%s = %d, want 1", orphanID, n)
	}
}

func TestSourceAuditCorruptNode(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.AppendSourceSegment("src-ok", "ok body"); err != nil {
		t.Fatalf("append: %v", err)
	}
	path := filepath.Join(store.sourceNodesDir(), "src-ok.md")
	if err := os.WriteFile(path, []byte("this is not a node file\n"), 0o644); err != nil {
		t.Fatalf("corrupt node: %v", err)
	}

	nodes, edges, err := store.LoadSources(10)
	if err == nil {
		t.Fatal("LoadSources on corrupt journal-referenced node: want error")
	}
	if nodes != nil || edges != nil {
		t.Fatalf("corrupt node returned partial results: nodes=%v edges=%v", nodes, edges)
	}

	findings, err := store.AuditSources()
	if err != nil {
		t.Fatalf("AuditSources: %v", err)
	}
	if !hasAuditKind(findings, "corrupt_node", "src-ok") {
		t.Fatalf("AuditSources = %+v, want corrupt_node src-ok", findings)
	}
}

func TestSourceAuditDanglingEdge(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.AppendSourceSegment("src-seg-1", "segment"); err != nil {
		t.Fatalf("append segment: %v", err)
	}
	dangling := &Edge{
		EdgeID:    "edge-dangling",
		Type:      EdgeTypeHasAttachment,
		From:      "src-seg-1",
		To:        "never-journaled-file",
		CreatedBy: CreatorIngester,
	}
	if err := appendJSONL(store.sourceEdgesPath(), dangling); err != nil {
		t.Fatalf("append dangling edge: %v", err)
	}

	nodes, edges, err := store.LoadSources(1)
	if err != nil {
		t.Fatalf("LoadSources: %v", err)
	}
	if len(nodes) != 1 || nodes[0].NodeID != "src-seg-1" {
		t.Fatalf("LoadSources nodes = %+v, want src-seg-1", nodes)
	}
	if len(edges) != 0 {
		t.Fatalf("LoadSources returned dangling edge: %+v", edges)
	}

	findings, err := store.AuditSources()
	if err != nil {
		t.Fatalf("AuditSources: %v", err)
	}
	found := false
	for _, f := range findings {
		if f.Kind == "dangling_edge" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("AuditSources = %+v, want dangling_edge", findings)
	}
}

func TestSourceScopeInvalidChannelMismatch(t *testing.T) {
	store := newTestStore(t)
	id := stampGraphIdentity(t, store, GraphDirKindChannel)
	otherChannel := uuid.NewString()

	_, _, err := store.AppendSourceFile(SourceFileInput{
		AttachmentID: uuid.NewString(),
		Body:         "channel mismatch",
		Visibility:   "channel",
		ChannelID:    otherChannel,
	})
	if err == nil {
		t.Fatal("AppendSourceFile: want error on ChannelID mismatch")
	}
	assertNothingPublished(t, store)

	_, err = store.AppendSourceSegmentInput(SourceSegmentInput{
		ID:         "src-mismatch",
		Body:       "segment mismatch",
		Visibility: "channel",
		ChannelID:  otherChannel,
	})
	if err == nil {
		t.Fatal("AppendSourceSegmentInput: want error on ChannelID mismatch")
	}
	assertNothingPublished(t, store)
	if id.OwnerID == otherChannel {
		t.Fatal("test setup: otherChannel collided with identity owner")
	}
}

func TestSourceScopeProjectRejectsChannelVisibility(t *testing.T) {
	store := newTestStore(t)
	stampGraphIdentity(t, store, GraphDirKindProject)

	_, _, err := store.AppendSourceFile(SourceFileInput{
		AttachmentID: uuid.NewString(),
		Body:         "channel on project graph",
		Visibility:   "channel",
		ChannelID:    uuid.NewString(),
	})
	if err == nil {
		t.Fatal("AppendSourceFile: want error for visibility=channel on project graph")
	}
	assertNothingPublished(t, store)
}

func TestSourceScopeUnknownVisibility(t *testing.T) {
	store := newTestStore(t)
	stampGraphIdentity(t, store, GraphDirKindProject)

	_, _, err := store.AppendSourceFile(SourceFileInput{
		AttachmentID: uuid.NewString(),
		Body:         "unknown vis",
		Visibility:   "workspace",
	})
	if err == nil {
		t.Fatal("AppendSourceFile: want error for unknown visibility")
	}
	assertNothingPublished(t, store)
}

func TestSourceScopeBareRootRejectsProvenanceWithoutIdentity(t *testing.T) {
	store := newTestStore(t)

	_, _, err := store.AppendSourceFile(SourceFileInput{
		AttachmentID: uuid.NewString(),
		Body:         "claimed channel",
		Visibility:   "channel",
		ChannelID:    uuid.NewString(),
	})
	if err == nil {
		t.Fatal("AppendSourceFile: want fail-closed when identity is missing and provenance is set")
	}
	assertNothingPublished(t, store)
}

func TestSourcePromotionRequiresAuthorizationAndIsOneShot(t *testing.T) {
	store := newTestStore(t)
	id := stampGraphIdentity(t, store, GraphDirKindChannel)
	attachmentID := uuid.NewString()

	_, nodeID, err := store.AppendSourceFile(SourceFileInput{
		AttachmentID: attachmentID,
		Body:         "channel file",
		BlobSHA256:   "abc",
		MIME:         "text/plain",
		SizeBytes:    4,
	})
	if err != nil {
		t.Fatalf("AppendSourceFile: %v", err)
	}

	nodes, _, err := store.LoadSources(1)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("LoadSources before promote: %v %+v", err, nodes)
	}
	if nodes[0].Visibility != "channel" || nodes[0].ChannelID != id.OwnerID {
		t.Fatalf("pre-promote node = %+v, want visibility=channel channel_id=%s", nodes[0], id.OwnerID)
	}

	if err := store.PromoteFileSourceToProject(attachmentID, false); err == nil {
		t.Fatal("PromoteFileSourceToProject(authorized=false): want error")
	}
	nodes, _, err = store.LoadSources(1)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("LoadSources after unauthorized promote: %v %+v", err, nodes)
	}
	if nodes[0].Visibility != "channel" || nodes[0].PromotedFromChannelID != "" {
		t.Fatalf("unauthorized promote mutated node: %+v", nodes[0])
	}

	if err := store.PromoteFileSourceToProject(attachmentID, true); err != nil {
		t.Fatalf("PromoteFileSourceToProject(authorized=true): %v", err)
	}
	nodes, _, err = store.LoadSources(1)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("LoadSources after promote: %v %+v", err, nodes)
	}
	got := nodes[0]
	if got.NodeID != nodeID {
		t.Fatalf("promoted node id = %s, want %s", got.NodeID, nodeID)
	}
	if got.Visibility != "project" {
		t.Fatalf("promoted visibility = %q, want project", got.Visibility)
	}
	if got.PromotedFromChannelID != id.OwnerID {
		t.Fatalf("promoted_from_channel_id = %q, want %s", got.PromotedFromChannelID, id.OwnerID)
	}
	if !promotionAuditRecorded(t, store, nodeID) {
		t.Fatal("expected promotion audit line")
	}

	if err := store.PromoteFileSourceToProject(attachmentID, true); err == nil {
		t.Fatal("second PromoteFileSourceToProject: want error")
	}
	nodes, _, err = store.LoadSources(1)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("LoadSources after second promote: %v %+v", err, nodes)
	}
	if nodes[0].Visibility != "project" || nodes[0].PromotedFromChannelID != id.OwnerID {
		t.Fatalf("second promote mutated node: %+v", nodes[0])
	}
}

func stampGraphIdentity(t *testing.T, store *Store, kind GraphDirKind) GraphIdentity {
	t.Helper()
	id := GraphIdentity{
		WorkspaceID: uuid.NewString(),
		Kind:        string(kind),
		OwnerID:     uuid.NewString(),
	}
	body, err := json.Marshal(id)
	if err != nil {
		t.Fatalf("marshal identity: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.Root, graphIdentityFile), body, 0o644); err != nil {
		t.Fatalf("write identity: %v", err)
	}
	return id
}

func assertNothingPublished(t *testing.T, store *Store) {
	t.Helper()
	seq, err := store.CurrentSourceSeq()
	if err != nil {
		t.Fatalf("CurrentSourceSeq: %v", err)
	}
	if seq != 0 {
		t.Fatalf("journal seq = %d, want 0 (nothing published)", seq)
	}
	nodes, edges, err := store.LoadSources(10)
	if err != nil {
		t.Fatalf("LoadSources: %v", err)
	}
	if len(nodes) != 0 || len(edges) != 0 {
		t.Fatalf("published sources after rejected append: nodes=%v edges=%v", nodes, edges)
	}
	for _, dir := range []string{store.sourceNodesDir(), filepath.Join(store.sourcesDir(), "pending")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read %s: %v", dir, err)
		}
		var md []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				md = append(md, e.Name())
			}
		}
		if len(md) != 0 {
			t.Fatalf("source files left in %s after rejected append: %v", dir, md)
		}
	}
}

func hasAuditKind(findings []SourceAuditFinding, kind, sourceID string) bool {
	for _, f := range findings {
		if f.Kind == kind && (sourceID == "" || f.SourceID == sourceID) {
			return true
		}
	}
	return false
}

func countQuarantineFindings(t *testing.T, store *Store, kind, sourceID string) int {
	t.Helper()
	path := filepath.Join(store.sourcesDir(), "quarantine.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read quarantine: %v", err)
	}
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			t.Fatalf("parse quarantine line %q: %v", line, err)
		}
		if item["kind"] == kind && item["source_id"] == sourceID {
			n++
		}
	}
	return n
}

func promotionAuditRecorded(t *testing.T, store *Store, sourceID string) bool {
	t.Helper()
	for _, name := range []string{"audit.jsonl", "quarantine.jsonl"} {
		path := filepath.Join(store.sourcesDir(), name)
		b, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(b), sourceID) && strings.Contains(string(b), "project") {
			return true
		}
	}
	return false
}

func journalExistsWithEntries(t *testing.T, store *Store) bool {
	t.Helper()
	path := store.sourceJournalPath()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		t.Fatalf("read journal: %v", err)
	}
	return strings.TrimSpace(string(b)) != ""
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
