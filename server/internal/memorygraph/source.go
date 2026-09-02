package memorygraph

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

// SourceFileInput is the ingest payload for one file source node. Identity
// is graph-scope (this store) + AttachmentID; BlobSHA256 records bytes only
// and never merges node identity across scopes (spec §10, A17/D17).
type SourceFileInput struct {
	AttachmentID     string
	Body             string
	BlobSHA256       string
	MIME             string
	SizeBytes        int64
	ExtractionStatus string
	Visibility       string
	ChannelID        string
	AgentID          string
	TaskID           string
}

// SourceSegmentInput is the ingest payload for one segment source node.
// AppendSourceSegment(id, body) is the empty-provenance form.
type SourceSegmentInput struct {
	ID         string
	Body       string
	Visibility string
	ChannelID  string
	AgentID    string
	TaskID     string
}

// Source-layer audit classifications (spec §15, A27).
const (
	SourceAuditOrphanNode   = "orphan_node"
	SourceAuditMissingNode  = "missing_node"
	SourceAuditCorruptNode  = "corrupt_node"
	SourceAuditInvalidScope = "invalid_scope"
	SourceAuditDanglingEdge = "dangling_edge"
)

// SourceAuditFinding is one quarantined source-layer defect.
type SourceAuditFinding struct {
	Kind     string `json:"kind"`
	SourceID string `json:"source_id"`
	Detail   string `json:"detail,omitempty"`
}

type sourceProvenance struct {
	Visibility string
	ChannelID  string
	AgentID    string
	TaskID     string
}

func (p sourceProvenance) anySet() bool {
	return strings.TrimSpace(p.Visibility) != "" ||
		strings.TrimSpace(p.ChannelID) != "" ||
		strings.TrimSpace(p.AgentID) != "" ||
		strings.TrimSpace(p.TaskID) != ""
}

type sourceQuarantineEntry struct {
	TS       time.Time `json:"ts"`
	SourceID string    `json:"source_id"`
	Kind     string    `json:"kind"`
	Detail   string    `json:"detail"`
}

// sourceJournalEntry is one append-only publication record. Seq is a
// per-store monotonically increasing integer starting at 1.
type sourceJournalEntry struct {
	Seq      int       `json:"seq"`
	SourceID string    `json:"source_id"`
	Kind     string    `json:"kind"`
	TS       time.Time `json:"ts"`
}

func (s *Store) sourcesDir() string     { return filepath.Join(s.Root, "shared", "sources") }
func (s *Store) sourceNodesDir() string { return filepath.Join(s.sourcesDir(), "nodes") }
func (s *Store) sourcePendingDir() string {
	return filepath.Join(s.sourcesDir(), "pending")
}
func (s *Store) sourceEdgesPath() string {
	return filepath.Join(s.sourcesDir(), "edges.jsonl")
}
func (s *Store) sourceJournalPath() string {
	return filepath.Join(s.sourcesDir(), "journal.jsonl")
}
func (s *Store) sourceQuarantinePath() string {
	return filepath.Join(s.sourcesDir(), "quarantine.jsonl")
}
func (s *Store) sourceAuditPath() string {
	return filepath.Join(s.sourcesDir(), "audit.jsonl")
}

// AppendSourceSegment publishes one immutable segment source node and
// returns its journal seq.
func (s *Store) AppendSourceSegment(id, body string) (int, error) {
	return s.AppendSourceSegmentInput(SourceSegmentInput{ID: id, Body: body})
}

// AppendSourceSegmentInput publishes one segment source node with optional
// provenance. Empty provenance on a legacy bare-root store is accepted;
// any provenance field requires a readable graph identity.
func (s *Store) AppendSourceSegmentInput(in SourceSegmentInput) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateFileID("node_id", in.ID); err != nil {
		return 0, err
	}
	vis, channelID, agentIDs, taskIDs, err := s.resolveSourceProvenanceLocked(sourceProvenance{
		Visibility: in.Visibility,
		ChannelID:  in.ChannelID,
		AgentID:    in.AgentID,
		TaskID:     in.TaskID,
	})
	if err != nil {
		return 0, err
	}
	published, err := s.sourcePublishedLocked(in.ID)
	if err != nil {
		return 0, err
	}
	if published {
		return 0, fmt.Errorf("source segment %s already exists", in.ID)
	}
	n := &Node{
		NodeID:         in.ID,
		Body:           in.Body,
		Level:          SourceLayerLevel,
		SourceKind:     SourceKindSegment,
		CreatedBy:      CreatorIngester,
		TemporalStatus: TemporalCurrent,
		ObservedAt:     time.Now().UTC(),
		Visibility:     vis,
		ChannelID:      channelID,
		SourceAgentIDs: agentIDs,
		SourceTaskIDs:  taskIDs,
	}
	return s.publishSourceNodeLocked(n, SourceKindSegment)
}

// AppendSourceFile publishes one file source node. Re-appending the same
// attachment ID in this graph scope returns the existing record and does
// not write a second journal entry.
func (s *Store) AppendSourceFile(in SourceFileInput) (seq int, nodeID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateFileID("attachment_id", in.AttachmentID); err != nil {
		return 0, "", err
	}
	if in.SizeBytes < 0 {
		return 0, "", fmt.Errorf("size_bytes must be >= 0")
	}
	if err := validateExtractionStatus(in.ExtractionStatus); err != nil {
		return 0, "", err
	}
	vis, channelID, agentIDs, taskIDs, err := s.resolveSourceProvenanceLocked(sourceProvenance{
		Visibility: in.Visibility,
		ChannelID:  in.ChannelID,
		AgentID:    in.AgentID,
		TaskID:     in.TaskID,
	})
	if err != nil {
		return 0, "", err
	}
	if existing, existingSeq, err := s.findFileSourceByAttachmentLocked(in.AttachmentID); err != nil {
		return 0, "", err
	} else if existing != nil {
		return existingSeq, existing.NodeID, nil
	}
	n := &Node{
		NodeID:           uuid.NewString(),
		Body:             in.Body,
		Level:            SourceLayerLevel,
		SourceKind:       SourceKindFile,
		AttachmentID:     in.AttachmentID,
		BlobSHA256:       in.BlobSHA256,
		MIME:             in.MIME,
		SizeBytes:        in.SizeBytes,
		ExtractionStatus: in.ExtractionStatus,
		CreatedBy:        CreatorIngester,
		TemporalStatus:   TemporalCurrent,
		ObservedAt:       time.Now().UTC(),
		Visibility:       vis,
		ChannelID:        channelID,
		SourceAgentIDs:   agentIDs,
		SourceTaskIDs:    taskIDs,
	}
	seq, err = s.publishSourceNodeLocked(n, SourceKindFile)
	if err != nil {
		return 0, "", err
	}
	return seq, n.NodeID, nil
}

// AppendSourceHasAttachment records an ingest-owned provenance edge from a
// segment source node to a file source node.
func (s *Store) AppendSourceHasAttachment(segmentID, fileNodeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	seg, err := s.loadSourceNodeLocked(segmentID)
	if err != nil {
		return err
	}
	if seg == nil || seg.SourceKind != SourceKindSegment {
		return fmt.Errorf("has_attachment: segment source %q not found", segmentID)
	}
	file, err := s.loadSourceNodeLocked(fileNodeID)
	if err != nil {
		return err
	}
	if file == nil || file.SourceKind != SourceKindFile {
		return fmt.Errorf("has_attachment: file source %q not found", fileNodeID)
	}
	edges, err := s.loadSourceEdgesLocked()
	if err != nil {
		return err
	}
	for _, e := range edges {
		if e.Type == EdgeTypeHasAttachment && e.From == segmentID && e.To == fileNodeID {
			return nil
		}
	}
	e := &Edge{
		EdgeID:    uuid.NewString(),
		Type:      EdgeTypeHasAttachment,
		From:      segmentID,
		To:        fileNodeID,
		CreatedBy: CreatorIngester,
	}
	return appendJSONL(s.sourceEdgesPath(), e)
}

// LoadSources returns source nodes and has_attachment edges published at
// journal seq <= watermark. Corrupt or missing journal-referenced records
// fail closed with an error and no partial results. Watermark 0 yields
// an empty snapshot.
func (s *Store) LoadSources(watermark int) ([]*Node, []*Edge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadSourcesLocked(watermark)
}

// CurrentSourceSeq returns the store's current max journal seq (0 when
// no sources have been published).
func (s *Store) CurrentSourceSeq() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentSourceSeqLocked()
}

func (s *Store) loadSourcesLocked(watermark int) ([]*Node, []*Edge, error) {
	entries, err := s.readSourceJournalLocked()
	if err != nil {
		return nil, nil, err
	}
	visible := map[string]*Node{}
	for _, e := range entries {
		if e.Seq <= 0 {
			return nil, nil, fmt.Errorf("source journal: invalid seq %d for %s", e.Seq, e.SourceID)
		}
		if e.Seq > watermark {
			continue
		}
		n, err := s.loadSourceNodeLocked(e.SourceID)
		if err != nil {
			return nil, nil, err
		}
		if n == nil {
			return nil, nil, fmt.Errorf("source journal seq %d: missing node %s", e.Seq, e.SourceID)
		}
		visible[n.NodeID] = n
	}
	nodes := make([]*Node, 0, len(visible))
	for _, n := range visible {
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })

	allEdges, err := s.loadSourceEdgesLocked()
	if err != nil {
		return nil, nil, err
	}
	var edges []*Edge
	for _, e := range allEdges {
		if _, ok := visible[e.From]; !ok {
			continue
		}
		if _, ok := visible[e.To]; !ok {
			continue
		}
		edges = append(edges, e)
	}
	return nodes, edges, nil
}

func (s *Store) currentSourceSeqLocked() (int, error) {
	entries, err := s.readSourceJournalLocked()
	if err != nil {
		return 0, err
	}
	max := 0
	for _, e := range entries {
		if e.Seq > max {
			max = e.Seq
		}
	}
	return max, nil
}

func (s *Store) readSourceJournalLocked() ([]*sourceJournalEntry, error) {
	var entries []*sourceJournalEntry
	if err := readJSONL(s.sourceJournalPath(), &entries); err != nil {
		return nil, fmt.Errorf("read source journal: %w", err)
	}
	return entries, nil
}

func (s *Store) appendSourceJournalLocked(sourceID, kind string) (int, error) {
	seq, err := s.currentSourceSeqLocked()
	if err != nil {
		return 0, err
	}
	seq++
	entry := sourceJournalEntry{
		Seq:      seq,
		SourceID: sourceID,
		Kind:     kind,
		TS:       time.Now().UTC(),
	}
	if err := appendJSONL(s.sourceJournalPath(), entry); err != nil {
		return 0, fmt.Errorf("append source journal: %w", err)
	}
	return seq, nil
}

func (s *Store) publishSourceNodeLocked(n *Node, kind string) (int, error) {
	if err := s.prepareSourceNodeLocked(n); err != nil {
		return 0, err
	}
	if hook := s.testHookBeforeJournal; hook != nil {
		hook()
	}
	if err := s.commitSourceNodeLocked(n.NodeID); err != nil {
		return 0, err
	}
	return s.appendSourceJournalLocked(n.NodeID, kind)
}

func marshalSourceNode(n *Node) ([]byte, error) {
	n.ContentHash = ComputeContentHash(n.Body)
	fm, err := yaml.Marshal(n)
	if err != nil {
		return nil, fmt.Errorf("marshal source node %s: %w", n.NodeID, err)
	}
	return []byte("---\n" + string(fm) + "---\n\n" + n.Body), nil
}

func (s *Store) cleanSourcePendingLocked() error {
	dir := s.sourcePendingDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) prepareSourceNodeLocked(n *Node) error {
	if err := validateFileID("node_id", n.NodeID); err != nil {
		return err
	}
	if err := s.cleanSourcePendingLocked(); err != nil {
		return err
	}
	content, err := marshalSourceNode(n)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.sourcePendingDir(), 0o755); err != nil {
		return err
	}
	pending := filepath.Join(s.sourcePendingDir(), n.NodeID+".md")
	if err := os.WriteFile(pending, content, 0o644); err != nil {
		return fmt.Errorf("write pending source node %s: %w", n.NodeID, err)
	}
	b, err := os.ReadFile(pending)
	if err != nil {
		return fmt.Errorf("re-read pending source node %s: %w", n.NodeID, err)
	}
	parsed, err := parseNodeFile(b)
	if err != nil {
		return fmt.Errorf("validate pending source node %s: %w", n.NodeID, err)
	}
	if parsed.NodeID != n.NodeID {
		return fmt.Errorf("validate pending source node %s: node_id mismatch %q", n.NodeID, parsed.NodeID)
	}
	return nil
}

func (s *Store) commitSourceNodeLocked(id string) error {
	if err := os.MkdirAll(s.sourceNodesDir(), 0o755); err != nil {
		return err
	}
	pending := filepath.Join(s.sourcePendingDir(), id+".md")
	dest := filepath.Join(s.sourceNodesDir(), id+".md")
	if err := os.Rename(pending, dest); err != nil {
		return fmt.Errorf("commit source node %s: %w", id, err)
	}
	return nil
}

func (s *Store) loadSourceNodeLocked(id string) (*Node, error) {
	if err := validateFileID("node_id", id); err != nil {
		return nil, nil
	}
	path := filepath.Join(s.sourceNodesDir(), id+".md")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read source node %s: %w", id, err)
	}
	n, err := parseNodeFile(b)
	if err != nil {
		return nil, fmt.Errorf("parse source node %s: %w", id, err)
	}
	return n, nil
}

func (s *Store) loadSourceEdgesLocked() ([]*Edge, error) {
	var edges []*Edge
	if err := readJSONL(s.sourceEdgesPath(), &edges); err != nil {
		return nil, fmt.Errorf("read source edges: %w", err)
	}
	return edges, nil
}

func (s *Store) findFileSourceByAttachmentLocked(attachmentID string) (*Node, int, error) {
	entries, err := s.readSourceJournalLocked()
	if err != nil {
		return nil, 0, err
	}
	for _, e := range entries {
		if e.Kind != SourceKindFile {
			continue
		}
		n, err := s.loadSourceNodeLocked(e.SourceID)
		if err != nil {
			return nil, 0, err
		}
		if n == nil || n.SourceKind != SourceKindFile || n.AttachmentID != attachmentID {
			continue
		}
		return n, e.Seq, nil
	}
	return nil, 0, nil
}

func (s *Store) seqForSourceLocked(sourceID string) (int, error) {
	entries, err := s.readSourceJournalLocked()
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		if e.SourceID == sourceID {
			return e.Seq, nil
		}
	}
	return 0, nil
}

func (s *Store) lookupSourceNode(id string) (*Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadSourceNodeLocked(id)
}

func (s *Store) lookupSourceEdge(id string) (*Edge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	edges, err := s.loadSourceEdgesLocked()
	if err != nil {
		return nil, err
	}
	for _, e := range edges {
		if e.EdgeID == id {
			return e, nil
		}
	}
	return nil, nil
}

func (s *Store) sourcePublishedLocked(id string) (bool, error) {
	seq, err := s.seqForSourceLocked(id)
	if err != nil {
		return false, err
	}
	return seq > 0, nil
}

func (s *Store) resolveSourceProvenanceLocked(p sourceProvenance) (visibility, channelID string, agentIDs, taskIDs []string, err error) {
	id, idErr := ReadGraphIdentity(s.Root)
	if idErr != nil {
		if !p.anySet() {
			return "", "", nil, nil, nil
		}
		return "", "", nil, nil, fmt.Errorf("source provenance requires graph identity: %w", idErr)
	}
	vis := strings.TrimSpace(p.Visibility)
	if vis == "" {
		if id.Kind == string(GraphDirKindChannel) {
			vis = "channel"
		} else {
			vis = "project"
		}
	}
	switch vis {
	case "project":
		if id.Kind == string(GraphDirKindChannel) {
			return "", "", nil, nil, fmt.Errorf("invalid source scope: channel graph cannot publish project-visible source")
		}
		if strings.TrimSpace(p.ChannelID) != "" {
			return "", "", nil, nil, fmt.Errorf("invalid source scope: project graph cannot carry channel_id")
		}
	case "channel":
		if id.Kind != string(GraphDirKindChannel) {
			return "", "", nil, nil, fmt.Errorf("invalid source scope: project graph cannot publish channel-visible source")
		}
		ch := strings.TrimSpace(p.ChannelID)
		if ch == "" {
			ch = id.OwnerID
		}
		if ch != id.OwnerID {
			return "", "", nil, nil, fmt.Errorf("invalid source scope: channel_id %q does not match graph owner %q", ch, id.OwnerID)
		}
		channelID = ch
	default:
		return "", "", nil, nil, fmt.Errorf("invalid source visibility %q", vis)
	}
	if a := strings.TrimSpace(p.AgentID); a != "" {
		agentIDs = []string{a}
	}
	if t := strings.TrimSpace(p.TaskID); t != "" {
		taskIDs = []string{t}
	}
	return vis, channelID, agentIDs, taskIDs, nil
}

func (s *Store) sourceScopeErrorLocked(n *Node) string {
	if n == nil {
		return ""
	}
	id, err := ReadGraphIdentity(s.Root)
	if err != nil {
		return ""
	}
	vis := strings.TrimSpace(n.Visibility)
	if vis == "" {
		vis = "project"
	}
	switch vis {
	case "project":
		if id.Kind == string(GraphDirKindChannel) && n.PromotedFromChannelID == "" {
			return "project visibility on channel graph without promotion"
		}
	case "channel":
		if id.Kind != string(GraphDirKindChannel) {
			return "channel visibility is not valid on a project graph"
		}
		if n.ChannelID != "" && n.ChannelID != id.OwnerID {
			return fmt.Sprintf("channel_id %q does not match graph owner %q", n.ChannelID, id.OwnerID)
		}
	default:
		return fmt.Sprintf("unknown visibility %q", vis)
	}
	return ""
}

// AuditSources walks the source journal, node files, and provenance edges
// and quarantines partial/missing/corrupt/identity-invalid records. Orphan
// node files stay invisible to LoadSources; journal-referenced missing or
// corrupt nodes keep failing LoadSources closed.
func (s *Store) AuditSources() ([]SourceAuditFinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.auditSourcesLocked()
}

func (s *Store) auditSourcesLocked() ([]SourceAuditFinding, error) {
	entries, err := s.readSourceJournalLocked()
	if err != nil {
		return nil, err
	}
	journaled := map[string]*sourceJournalEntry{}
	for _, e := range entries {
		if e.SourceID == "" {
			continue
		}
		journaled[e.SourceID] = e
	}

	var findings []SourceAuditFinding
	seen := map[string]bool{}
	add := func(kind, sourceID, detail string) {
		key := kind + "\x00" + sourceID
		if seen[key] {
			return
		}
		seen[key] = true
		findings = append(findings, SourceAuditFinding{Kind: kind, SourceID: sourceID, Detail: detail})
	}

	nodeEntries, err := os.ReadDir(s.sourceNodesDir())
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read source nodes: %w", err)
	}
	present := map[string]bool{}
	for _, entry := range nodeEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".md")
		present[id] = true
		n, err := s.loadSourceNodeLocked(id)
		if err != nil {
			add(SourceAuditCorruptNode, id, err.Error())
			continue
		}
		if n == nil {
			add(SourceAuditCorruptNode, id, "unreadable node file")
			continue
		}
		if _, ok := journaled[id]; !ok {
			add(SourceAuditOrphanNode, id, "node file has no journal entry")
			continue
		}
		if detail := s.sourceScopeErrorLocked(n); detail != "" {
			add(SourceAuditInvalidScope, id, detail)
		}
	}
	for id, e := range journaled {
		if !present[id] {
			add(SourceAuditMissingNode, id, fmt.Sprintf("journal seq %d references missing node", e.Seq))
			continue
		}
	}

	edges, err := s.loadSourceEdgesLocked()
	if err != nil {
		return nil, err
	}
	for _, e := range edges {
		if e == nil || e.Type != EdgeTypeHasAttachment {
			continue
		}
		if _, ok := journaled[e.From]; ok {
			if _, ok := journaled[e.To]; ok {
				continue
			}
		}
		sourceID := e.EdgeID
		if sourceID == "" {
			sourceID = e.From + "->" + e.To
		}
		add(SourceAuditDanglingEdge, sourceID, fmt.Sprintf("has_attachment %s -> %s is not fully journaled", e.From, e.To))
	}

	if err := s.recordQuarantineLocked(findings); err != nil {
		return nil, err
	}
	return findings, nil
}

func (s *Store) recordQuarantineLocked(findings []SourceAuditFinding) error {
	var existing []*sourceQuarantineEntry
	if err := readJSONL(s.sourceQuarantinePath(), &existing); err != nil {
		return fmt.Errorf("read source quarantine: %w", err)
	}
	have := map[string]bool{}
	for _, e := range existing {
		if e == nil {
			continue
		}
		have[e.Kind+"\x00"+e.SourceID] = true
	}
	for _, f := range findings {
		key := f.Kind + "\x00" + f.SourceID
		if have[key] {
			continue
		}
		entry := sourceQuarantineEntry{
			TS:       time.Now().UTC(),
			SourceID: f.SourceID,
			Kind:     f.Kind,
			Detail:   f.Detail,
		}
		if err := appendJSONL(s.sourceQuarantinePath(), entry); err != nil {
			return fmt.Errorf("append source quarantine: %w", err)
		}
		have[key] = true
	}
	return nil
}

// PromoteFileSourceToProject is the only path that makes a channel-graph
// file source project-visible. authorized=false fails without mutation;
// a second successful call also fails. Channel identity is taken from the
// store and the existing node, never from the caller.
func (s *Store) PromoteFileSourceToProject(attachmentID string, authorized bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateFileID("attachment_id", attachmentID); err != nil {
		return err
	}
	n, _, err := s.findFileSourceByAttachmentLocked(attachmentID)
	if err != nil {
		return err
	}
	if n == nil {
		return fmt.Errorf("file source %s not found", attachmentID)
	}
	if n.Visibility == "project" || n.PromotedFromChannelID != "" {
		return fmt.Errorf("file source %s already promoted to project", attachmentID)
	}
	if !authorized {
		return fmt.Errorf("promotion not authorized")
	}
	id, err := ReadGraphIdentity(s.Root)
	if err != nil {
		return fmt.Errorf("promotion requires graph identity: %w", err)
	}
	if id.Kind != string(GraphDirKindChannel) {
		return fmt.Errorf("promotion is only valid on a channel graph")
	}
	if n.Visibility != "channel" {
		return fmt.Errorf("file source %s is not channel-visible", attachmentID)
	}
	from := n.ChannelID
	if from == "" {
		from = id.OwnerID
	}
	n.Visibility = "project"
	n.PromotedFromChannelID = from
	if err := s.prepareSourceNodeLocked(n); err != nil {
		return err
	}
	if err := s.commitSourceNodeLocked(n.NodeID); err != nil {
		return err
	}
	entry := sourceQuarantineEntry{
		TS:       time.Now().UTC(),
		SourceID: n.NodeID,
		Kind:     "promoted_to_project",
		Detail:   "promoted from channel " + from + " to project",
	}
	if err := appendJSONL(s.sourceAuditPath(), entry); err != nil {
		return fmt.Errorf("append source audit: %w", err)
	}
	return nil
}

func validateExtractionStatus(status string) error {
	switch status {
	case "", ExtractionPending, ExtractionUnsupported, ExtractionFailed:
		return nil
	default:
		return fmt.Errorf("invalid extraction_status %q", status)
	}
}

// IsSourceLayerNode reports whether n is an immutable level -1 source node.
func IsSourceLayerNode(n *Node) bool {
	return n != nil && n.Level == SourceLayerLevel
}

// IsSourceProvenanceEdge reports whether e is an ingest-owned has_attachment
// provenance edge. Management degree/fanout counting skips these.
func IsSourceProvenanceEdge(e *Edge) bool {
	return e != nil && e.Type == EdgeTypeHasAttachment && e.CreatedBy == CreatorIngester
}

// CountableHierarchyFanout returns outgoing summarizes children that are
// not source-layer nodes and are not provenance edges.
func CountableHierarchyFanout(g *Graph, nodeID string) int {
	n := 0
	for _, e := range g.childrenOf[nodeID] {
		if IsSourceProvenanceEdge(e) {
			continue
		}
		if child := g.Node(e.To); IsSourceLayerNode(child) {
			continue
		}
		n++
	}
	return n
}

// CountableRelationDegree returns incident node-to-node relation edges,
// skipping source provenance / has_attachment edges and source-layer
// endpoints. applyOne uses this count to enforce MaxRelationEdges.
func CountableRelationDegree(g *Graph, nodeID string) int {
	n := 0
	for _, e := range g.rel {
		if IsSourceProvenanceEdge(e) || e.Type == EdgeTypeHasAttachment {
			continue
		}
		switch {
		case e.From == nodeID:
			if !e.IsEdgeRef() && IsSourceLayerNode(g.Node(e.To)) {
				continue
			}
			n++
		case !e.IsEdgeRef() && e.To == nodeID:
			if IsSourceLayerNode(g.Node(e.From)) {
				continue
			}
			n++
		}
	}
	return n
}

// sourceLayerReject returns a reason when a management op targets the
// immutable source layer. The graph is left unchanged by the caller.
func (c *Consolidator) sourceLayerReject(g *Graph, op ConsolidateOp) string {
	switch op.Op {
	case OpAddNode:
		if op.Node != nil && IsSourceLayerNode(op.Node) {
			return "cannot create source-layer node via management"
		}
		if op.Node != nil {
			if src, err := c.store.lookupSourceNode(op.Node.NodeID); err != nil {
				return err.Error()
			} else if src != nil {
				return "cannot add a node that collides with a source id"
			}
		}
	case OpUpdateNode, OpDeleteNode:
		id := op.NodeID
		if id == "" && op.Node != nil {
			id = op.Node.NodeID
		}
		if IsSourceLayerNode(g.Node(id)) {
			return "cannot mutate source-layer node"
		}
		if src, err := c.store.lookupSourceNode(id); err != nil {
			return err.Error()
		} else if src != nil {
			return "cannot mutate source-layer node"
		}
	case OpMergeNode:
		// A merge mutates every input and materializes the result node,
		// so both sides must stay clear of the immutable source layer.
		ids := append([]string{}, op.InputNodeIDs...)
		if op.Node != nil {
			ids = append(ids, op.Node.NodeID)
		}
		for _, id := range ids {
			if IsSourceLayerNode(g.Node(id)) {
				return "cannot merge source-layer node"
			}
			if src, err := c.store.lookupSourceNode(id); err != nil {
				return err.Error()
			} else if src != nil {
				return "cannot merge source-layer node"
			}
		}
	case OpDeleteEdge, OpPruneEdge, OpUpdateEdge:
		id := op.EdgeID
		if id == "" && op.Edge != nil {
			id = op.Edge.EdgeID
		}
		if IsSourceProvenanceEdge(graphEdge(g, id)) {
			return "cannot mutate has_attachment edge"
		}
		if e, err := c.store.lookupSourceEdge(id); err != nil {
			return err.Error()
		} else if e != nil && (IsSourceProvenanceEdge(e) || e.Type == EdgeTypeHasAttachment) {
			return "cannot mutate has_attachment edge"
		}
	case OpAddRelationEdge, OpAddHierarchyEdge:
		if op.Edge != nil && (op.Edge.Type == EdgeTypeHasAttachment || IsSourceProvenanceEdge(op.Edge)) {
			return "cannot add has_attachment edge via management"
		}
	}
	return ""
}

func graphEdge(g *Graph, id string) *Edge {
	if g == nil || id == "" {
		return nil
	}
	for _, e := range g.HierarchyEdges() {
		if e.EdgeID == id {
			return e
		}
	}
	for _, e := range g.RelationEdges() {
		if e.EdgeID == id {
			return e
		}
	}
	return nil
}
