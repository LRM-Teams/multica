package memorygraph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Store implements the versioned directory storage for the graph memory
// reviewer (design §4.1). The layout under Root is:
//
//	current                    small file containing the current version number
//	versions/v<N>/             manifest.json + nodes/ + edges/
//	shared/embeddings/         cross-version embedding cache (<content_hash>.vec)
//	shared/sources/            append-only source layer (nodes/, edges.jsonl, journal.jsonl)
//	staging/segments/          immutable source segment summaries
//	query_log/                 per-window query logs (<window_id>.jsonl)
//	op_log/                    per-version consolidation audit logs
//	regression_set.jsonl       permanent regression query set
//
// A Store is safe for concurrent use within one process. GC also takes a
// store-root gc.lock so concurrent processes fail busy or reclaim a stale lock.
type Store struct {
	// Root is the memory_graph/ directory.
	Root string

	mu sync.Mutex

	// testHookBeforeJournal, if set, runs after a source node is prepared
	// under pending/ and before the atomic rename + journal commit. Tests
	// use it to inject a crash between the two publish phases.
	testHookBeforeJournal func()

	// testHookBeforeExtractionIndex, if set, runs after an extraction
	// artifact is renamed into place and before its index.jsonl append.
	// Tests use it to inject a crash between the two durability phases.
	testHookBeforeExtractionIndex func()
}

// NewStore returns a Store rooted at root. Call Init before use.
func NewStore(root string) *Store {
	return &Store{Root: root}
}

// Init creates the directory layout, the initial empty version v1 (manifest
// CreatedBy "init") and the current pointer file. It is idempotent: an
// existing current pointer is left untouched.
func (s *Store) Init() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, dir := range []string{
		s.versionsDir(),
		s.embeddingsDir(),
		s.sourcesDir(),
		s.sourceNodesDir(),
		s.sourcePendingDir(),
		s.stagingDir(),
		s.queryLogDir(),
		s.opLogDir(),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("init memory graph dir %s: %w", dir, err)
		}
	}
	if _, err := s.currentVersionLocked(); err == nil {
		return nil // already initialized
	} else if !os.IsNotExist(err) {
		return err
	}
	versions, err := s.listVersionsLocked()
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		if err := s.createVersionDirsLocked(1); err != nil {
			return err
		}
		m := &Manifest{
			Version:   1,
			CreatedAt: time.Now().UTC(),
			CreatedBy: "init",
		}
		if err := s.saveManifestLocked(1, m); err != nil {
			return err
		}
		versions = []int{1}
	}
	return s.writeCurrentLocked(versions[len(versions)-1])
}

// CurrentVersion returns the version number stored in the current pointer.
func (s *Store) CurrentVersion() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentVersionLocked()
}

// VersionDir returns the directory of version v (versions/v<N>).
func (s *Store) VersionDir(v int) string {
	return filepath.Join(s.versionsDir(), fmt.Sprintf("v%d", v))
}

// CreateVersionFrom deep-copies nodes/ and edges/ from parentV into a new
// version and writes a fresh manifest with ParentVersion set. The new version
// is fully written before it returns; the caller must SwitchCurrent to make
// it visible (create-then-switch ordering).
func (s *Store) CreateVersionFrom(parentV int, createdBy string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := os.Stat(s.VersionDir(parentV)); err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("parent version v%d does not exist", parentV)
		}
		return 0, fmt.Errorf("stat parent version v%d: %w", parentV, err)
	}
	versions, err := s.listVersionsLocked()
	if err != nil {
		return 0, err
	}
	newV := 1
	if len(versions) > 0 {
		newV = versions[len(versions)-1] + 1
	}
	if err := s.createVersionDirsLocked(newV); err != nil {
		return 0, err
	}
	nodeCount, err := copyDirFiles(s.nodesDir(parentV), s.nodesDir(newV))
	if err != nil {
		return 0, fmt.Errorf("copy nodes v%d -> v%d: %w", parentV, newV, err)
	}
	if err := copyEdgeFile(s.hierarchyPath(parentV), s.hierarchyPath(newV)); err != nil {
		return 0, err
	}
	if err := copyEdgeFile(s.relationsPath(parentV), s.relationsPath(newV)); err != nil {
		return 0, err
	}
	hier, rel, err := s.loadEdgesLocked(newV)
	if err != nil {
		return 0, err
	}
	watermark, err := s.currentSourceSeqLocked()
	if err != nil {
		return 0, err
	}
	m := &Manifest{
		Version:         newV,
		ParentVersion:   parentV,
		CreatedAt:       time.Now().UTC(),
		CreatedBy:       createdBy,
		NodeCount:       nodeCount,
		HierEdgeCount:   len(hier),
		RelEdgeCount:    len(rel),
		SourceWatermark: watermark,
	}
	if err := s.saveManifestLocked(newV, m); err != nil {
		return 0, err
	}
	return newV, nil
}

// SwitchCurrent atomically points current at version v (temp file + rename).
// The version directory and its manifest must exist, so a version being
// switched to is always fully written first.
func (s *Store) SwitchCurrent(v int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := os.Stat(s.manifestPath(v)); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("version v%d does not exist or is incomplete", v)
		}
		return fmt.Errorf("stat version v%d manifest: %w", v, err)
	}
	return s.writeCurrentLocked(v)
}

// ListVersions returns all version numbers in ascending order.
func (s *Store) ListVersions() ([]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listVersionsLocked()
}

// LoadManifest reads versions/v<N>/manifest.json.
func (s *Store) LoadManifest(v int) (*Manifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadManifestLocked(v)
}

// SaveManifest writes versions/v<N>/manifest.json.
func (s *Store) SaveManifest(v int, m *Manifest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveManifestLocked(v, m)
}

// ComputeContentHash returns the canonical "sha256:<hex>" hash of a node body.
// Only the body is hashed; metadata changes do not invalidate embeddings.
func ComputeContentHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// SaveNode writes n as versions/v<N>/nodes/<node_id>.md: yaml frontmatter
// between --- lines followed by the markdown body. ContentHash is recomputed
// from the body on every save.
func (s *Store) SaveNode(v int, n *Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateFileID("node_id", n.NodeID); err != nil {
		return err
	}
	n.ContentHash = ComputeContentHash(n.Body)
	fm, err := yaml.Marshal(n)
	if err != nil {
		return fmt.Errorf("marshal node %s frontmatter: %w", n.NodeID, err)
	}
	content := "---\n" + string(fm) + "---\n\n" + n.Body
	if err := os.MkdirAll(s.nodesDir(v), 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.nodesDir(v), n.NodeID+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write node %s: %w", n.NodeID, err)
	}
	return nil
}

// LoadNodes reads all nodes of version v, parsing yaml frontmatter plus body.
func (s *Store) LoadNodes(v int) ([]*Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadNodesLocked(v)
}

// SaveEdges rewrites edges/hierarchy.jsonl and edges/relations.jsonl of
// version v, one JSON object per line.
func (s *Store) SaveEdges(v int, hier, rel []*Edge) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.edgesDir(v), 0o755); err != nil {
		return err
	}
	if err := writeJSONL(s.hierarchyPath(v), hier); err != nil {
		return fmt.Errorf("write hierarchy edges v%d: %w", v, err)
	}
	if err := writeJSONL(s.relationsPath(v), rel); err != nil {
		return fmt.Errorf("write relation edges v%d: %w", v, err)
	}
	return nil
}

// LoadEdges reads hierarchy.jsonl and relations.jsonl of version v. Missing
// files yield empty edge lists.
func (s *Store) LoadEdges(v int) (hier, rel []*Edge, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadEdgesLocked(v)
}

// GC deletes old version directories, keeping the keep most recent versions.
// The current version is never deleted, even when it falls outside the
// keep window (design §5.5).
func (s *Store) GC(keep int) error {
	return s.GCWithPinned(keep, nil)
}

// EmbeddingPath returns shared/embeddings/<hash>.vec for a content hash,
// ensuring the parent directory exists.
func (s *Store) EmbeddingPath(contentHash string) string {
	_ = os.MkdirAll(s.embeddingsDir(), 0o755)
	return filepath.Join(s.embeddingsDir(), contentHash+".vec")
}

// WriteStagingSegment writes an immutable source segment summary to
// staging/segments/<segment_id>.md. It fails if the segment already exists.
func (s *Store) WriteStagingSegment(segmentID string, content []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateFileID("segment_id", segmentID); err != nil {
		return err
	}
	if err := os.MkdirAll(s.stagingDir(), 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.stagingDir(), segmentID+".md")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("staging segment %s already exists", segmentID)
		}
		return fmt.Errorf("write staging segment %s: %w", segmentID, err)
	}
	defer f.Close()
	if _, err := f.Write(content); err != nil {
		return fmt.Errorf("write staging segment %s: %w", segmentID, err)
	}
	return nil
}

// WriteStagingSegmentMeta persists the scope/provenance sidecar
// staging/segments/<segment_id>.scope.json. Like the segment body it
// accompanies, the sidecar is immutable: it fails if the meta already
// exists.
func (s *Store) WriteStagingSegmentMeta(segmentID string, meta *SegmentMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateFileID("segment_id", segmentID); err != nil {
		return err
	}
	body, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal staging segment meta %s: %w", segmentID, err)
	}
	if err := os.MkdirAll(s.stagingDir(), 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.stagingDir(), segmentID+".scope.json")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("staging segment meta %s already exists", segmentID)
		}
		return fmt.Errorf("write staging segment meta %s: %w", segmentID, err)
	}
	defer f.Close()
	if _, err := f.Write(body); err != nil {
		return fmt.Errorf("write staging segment meta %s: %w", segmentID, err)
	}
	return nil
}

// ReadStagingSegmentMeta reads staging/segments/<segment_id>.scope.json.
func (s *Store) ReadStagingSegmentMeta(segmentID string) (*SegmentMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateFileID("segment_id", segmentID); err != nil {
		return nil, err
	}
	body, err := os.ReadFile(filepath.Join(s.stagingDir(), segmentID+".scope.json"))
	if err != nil {
		return nil, fmt.Errorf("read staging segment meta %s: %w", segmentID, err)
	}
	var meta SegmentMeta
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, fmt.Errorf("parse staging segment meta %s: %w", segmentID, err)
	}
	return &meta, nil
}

// ReadStagingSegment reads staging/segments/<segment_id>.md.
func (s *Store) ReadStagingSegment(segmentID string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateFileID("segment_id", segmentID); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(s.stagingDir(), segmentID+".md"))
	if err != nil {
		return nil, fmt.Errorf("read staging segment %s: %w", segmentID, err)
	}
	return b, nil
}

// ListStagingSegments returns the ids of all staged segments, sorted.
func (s *Store) ListStagingSegments() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return listIDFiles(s.stagingDir(), ".md")
}

// DeleteStagingSegment removes a staged segment summary.
func (s *Store) DeleteStagingSegment(segmentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateFileID("segment_id", segmentID); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(s.stagingDir(), segmentID+".md")); err != nil {
		return fmt.Errorf("delete staging segment %s: %w", segmentID, err)
	}
	return nil
}

// AppendQueryLog appends one entry to query_log/<window_id>.jsonl.
func (s *Store) AppendQueryLog(windowID string, e *QueryLogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateFileID("window_id", windowID); err != nil {
		return err
	}
	if err := os.MkdirAll(s.queryLogDir(), 0o755); err != nil {
		return err
	}
	return appendJSONL(s.queryLogPath(windowID), e)
}

// ReadQueryLog reads all entries of query_log/<window_id>.jsonl. A missing
// window yields an empty list.
func (s *Store) ReadQueryLog(windowID string) ([]*QueryLogEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateFileID("window_id", windowID); err != nil {
		return nil, err
	}
	var entries []*QueryLogEntry
	if err := readJSONL(s.queryLogPath(windowID), &entries); err != nil {
		return nil, fmt.Errorf("read query log %s: %w", windowID, err)
	}
	return entries, nil
}

// ListQueryLogWindows returns the ids of all query log windows, sorted.
func (s *Store) ListQueryLogWindows() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return listIDFiles(s.queryLogDir(), ".jsonl")
}

// UpdateQueryLogEntry finds the entry with the given trace id in the window
// log, applies mutate and atomically rewrites the window file. It reports
// whether a matching entry was found. Used for async judge write-back
// (design §5.3).
func (s *Store) UpdateQueryLogEntry(windowID, traceID string, mutate func(*QueryLogEntry)) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateFileID("window_id", windowID); err != nil {
		return false, err
	}
	path := s.queryLogPath(windowID)
	var entries []*QueryLogEntry
	if err := readJSONL(path, &entries); err != nil {
		return false, fmt.Errorf("read query log %s: %w", windowID, err)
	}
	found := false
	for _, e := range entries {
		if e.TraceID == traceID {
			mutate(e)
			found = true
			break
		}
	}
	if !found {
		return false, nil
	}
	tmp := path + ".tmp"
	if err := writeJSONL(tmp, entries); err != nil {
		return false, fmt.Errorf("rewrite query log %s: %w", windowID, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return false, fmt.Errorf("rewrite query log %s: %w", windowID, err)
	}
	return true, nil
}

// AppendRegression appends one entry to regression_set.jsonl (design Q26).
func (s *Store) AppendRegression(e *RegressionEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return appendJSONL(filepath.Join(s.Root, "regression_set.jsonl"), e)
}

// ReadRegression reads the permanent regression set. A missing file yields
// an empty list.
func (s *Store) ReadRegression() ([]*RegressionEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var entries []*RegressionEntry
	if err := readJSONL(filepath.Join(s.Root, "regression_set.jsonl"), &entries); err != nil {
		return nil, fmt.Errorf("read regression set: %w", err)
	}
	return entries, nil
}

// ---------------------------------------------------------------------------
// path helpers
// ---------------------------------------------------------------------------

func (s *Store) versionsDir() string   { return filepath.Join(s.Root, "versions") }
func (s *Store) embeddingsDir() string { return filepath.Join(s.Root, "shared", "embeddings") }
func (s *Store) stagingDir() string    { return filepath.Join(s.Root, "staging", "segments") }
func (s *Store) queryLogDir() string   { return filepath.Join(s.Root, "query_log") }
func (s *Store) opLogDir() string      { return filepath.Join(s.Root, "op_log") }
func (s *Store) currentPath() string   { return filepath.Join(s.Root, "current") }
func (s *Store) nodesDir(v int) string {
	return filepath.Join(s.VersionDir(v), "nodes")
}
func (s *Store) edgesDir(v int) string {
	return filepath.Join(s.VersionDir(v), "edges")
}
func (s *Store) manifestPath(v int) string {
	return filepath.Join(s.VersionDir(v), "manifest.json")
}
func (s *Store) hierarchyPath(v int) string {
	return filepath.Join(s.edgesDir(v), "hierarchy.jsonl")
}
func (s *Store) relationsPath(v int) string {
	return filepath.Join(s.edgesDir(v), "relations.jsonl")
}
func (s *Store) queryLogPath(windowID string) string {
	return filepath.Join(s.queryLogDir(), windowID+".jsonl")
}

// OpLogPath returns op_log/<version>.jsonl for the OpLogger.
func (s *Store) OpLogPath(version int) string {
	return filepath.Join(s.opLogDir(), fmt.Sprintf("%d.jsonl", version))
}

// ---------------------------------------------------------------------------
// locked internals
// ---------------------------------------------------------------------------

func (s *Store) createVersionDirsLocked(v int) error {
	for _, dir := range []string{s.nodesDir(v), s.edgesDir(v)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create version v%d: %w", v, err)
		}
	}
	return nil
}

func (s *Store) currentVersionLocked() (int, error) {
	b, err := os.ReadFile(s.currentPath())
	if err != nil {
		return 0, err
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("parse current pointer %q: %w", strings.TrimSpace(string(b)), err)
	}
	return v, nil
}

// writeCurrentLocked atomically replaces the current pointer via temp file +
// rename, so readers never observe a partially written pointer.
func (s *Store) writeCurrentLocked(v int) error {
	tmp := s.currentPath() + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(v)+"\n"), 0o644); err != nil {
		return fmt.Errorf("write current pointer: %w", err)
	}
	if err := os.Rename(tmp, s.currentPath()); err != nil {
		return fmt.Errorf("switch current pointer: %w", err)
	}
	return nil
}

func (s *Store) listVersionsLocked() ([]int, error) {
	entries, err := os.ReadDir(s.versionsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list versions: %w", err)
	}
	var versions []int
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "v") {
			continue
		}
		v, err := strconv.Atoi(entry.Name()[1:])
		if err != nil {
			continue
		}
		versions = append(versions, v)
	}
	sort.Ints(versions)
	return versions, nil
}

func (s *Store) loadNodesLocked(v int) ([]*Node, error) {
	entries, err := os.ReadDir(s.nodesDir(v))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read nodes dir v%d: %w", v, err)
	}
	var nodes []*Node
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(s.nodesDir(v), entry.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read node file %s: %w", path, err)
		}
		n, err := parseNodeFile(b)
		if err != nil {
			return nil, fmt.Errorf("parse node file %s: %w", path, err)
		}
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	return nodes, nil
}

func (s *Store) loadManifestLocked(v int) (*Manifest, error) {
	b, err := os.ReadFile(s.manifestPath(v))
	if err != nil {
		return nil, fmt.Errorf("read manifest v%d: %w", v, err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse manifest v%d: %w", v, err)
	}
	return &m, nil
}

func (s *Store) saveManifestLocked(v int, m *Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest v%d: %w", v, err)
	}
	if err := os.MkdirAll(s.VersionDir(v), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(s.manifestPath(v), append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write manifest v%d: %w", v, err)
	}
	return nil
}

func (s *Store) loadEdgesLocked(v int) (hier, rel []*Edge, err error) {
	if err := readJSONL(s.hierarchyPath(v), &hier); err != nil {
		return nil, nil, fmt.Errorf("read hierarchy edges v%d: %w", v, err)
	}
	if err := readJSONL(s.relationsPath(v), &rel); err != nil {
		return nil, nil, fmt.Errorf("read relation edges v%d: %w", v, err)
	}
	return hier, rel, nil
}

// ---------------------------------------------------------------------------
// file helpers
// ---------------------------------------------------------------------------

// validateFileID rejects ids that could escape their directory.
func validateFileID(kind, id string) error {
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return fmt.Errorf("invalid %s %q", kind, id)
	}
	return nil
}

// parseNodeFile splits a node .md file into yaml frontmatter and body.
func parseNodeFile(b []byte) (*Node, error) {
	s := string(b)
	if !strings.HasPrefix(s, "---\n") {
		return nil, fmt.Errorf("missing frontmatter delimiter")
	}
	rest := s[len("---\n"):]
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return nil, fmt.Errorf("missing closing frontmatter delimiter")
	}
	var n Node
	if err := yaml.Unmarshal([]byte(rest[:idx]), &n); err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", err)
	}
	n.Body = strings.TrimPrefix(rest[idx+len("\n---\n"):], "\n")
	return &n, nil
}

// copyDirFiles copies all regular files from src to dst and returns the
// number of files copied. A missing src copies nothing.
func copyDirFiles(src, dst string) (int, error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, entry.Name()))
		if err != nil {
			return count, err
		}
		if err := os.WriteFile(filepath.Join(dst, entry.Name()), b, 0o644); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// copyEdgeFile copies one jsonl edge file; a missing source writes nothing.
func copyEdgeFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

// listIDFiles lists <id><suffix> files in dir and returns the ids, sorted.
func listIDFiles(dir, suffix string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		ids = append(ids, strings.TrimSuffix(entry.Name(), suffix))
	}
	sort.Strings(ids)
	return ids, nil
}

func writeJSONL[T any](path string, items []T) error {
	var sb strings.Builder
	for _, item := range items {
		b, err := json.Marshal(item)
		if err != nil {
			return err
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}

func appendJSONL[T any](path string, item T) error {
	b, err := json.Marshal(item)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

func readJSONL[T any](path string, out *[]T) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item T
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return fmt.Errorf("parse %s line %q: %w", path, truncate(line, 80), err)
		}
		*out = append(*out, item)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
