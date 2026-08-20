package memorygraph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

// ExtractionInput is one extractor attempt against a published source node.
// Status is completed|failed|unsupported. Failed/unsupported attempts are
// still recorded; they are not error returns.
type ExtractionInput struct {
	Kind         string
	Extractor    string
	Provider     string
	Model        string
	ModelVersion string
	Language     string
	Coverage     string
	Output       string
	Status       string
}

// ExtractionIndexEntry is one complete generation in a source's index.jsonl.
type ExtractionIndexEntry struct {
	Gen      int    `json:"gen"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	Artifact string `json:"artifact"`
}

// ExtractionArtifact is the immutable per-generation extractor output.
type ExtractionArtifact struct {
	Kind         string    `json:"kind"`
	KindKnown    bool      `json:"kind_known"`
	Extractor    string    `json:"extractor"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model"`
	ModelVersion string    `json:"model_version"`
	Language     string    `json:"language"`
	Coverage     string    `json:"coverage"`
	Status       string    `json:"status"`
	Output       string    `json:"output"`
	TS           time.Time `json:"ts"`
}

// NormalizeDescriptionKind trims and lowercases raw for matching. Known
// kinds return their canonical form and true. Anything else returns the
// original string unchanged and false — unknown kinds are retained and
// never remapped onto a canonical kind (spec D19).
func NormalizeDescriptionKind(raw string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch lower {
	case DescriptionKindCaption, DescriptionKindOCR, DescriptionKindTranscript, DescriptionKindExtractedText:
		return lower, true
	default:
		return raw, false
	}
}

func (s *Store) extractionIndexPath(sourceNodeID string) string {
	return filepath.Join(s.Root, "shared", "sources", "artifacts", sourceNodeID, "index.jsonl")
}

func (s *Store) extractionArtifactRel(sourceNodeID, safeKind string, gen int) string {
	return filepath.Join("shared", "sources", "artifacts", sourceNodeID, safeKind, fmt.Sprintf("gen_%d.json", gen))
}

// RecordExtraction appends one immutable generation for a published source.
// The generation number is assigned by the store; existing gen files are
// never overwritten. Status failed/unsupported still writes a record.
func (s *Store) RecordExtraction(sourceNodeID string, in ExtractionInput) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recordExtractionLocked(sourceNodeID, in)
}

func (s *Store) recordExtractionLocked(sourceNodeID string, in ExtractionInput) (int, error) {
	if err := validateFileID("node_id", sourceNodeID); err != nil {
		return 0, err
	}
	if strings.TrimSpace(in.Kind) == "" {
		return 0, fmt.Errorf("extraction kind is empty")
	}
	if strings.TrimSpace(in.Extractor) == "" {
		return 0, fmt.Errorf("extraction extractor is empty")
	}
	if err := validateExtractionRecordStatus(in.Status); err != nil {
		return 0, err
	}
	published, err := s.sourcePublishedLocked(sourceNodeID)
	if err != nil {
		return 0, err
	}
	if !published {
		return 0, fmt.Errorf("source node %s not found", sourceNodeID)
	}

	kind, known := NormalizeDescriptionKind(in.Kind)
	if !known {
		kind = in.Kind
	}
	safeKind := safeDescriptionKind(kind, known)

	entries, err := s.readExtractionIndexLocked(sourceNodeID)
	if err != nil {
		return 0, err
	}
	indexed := map[int]bool{}
	max := 0
	for _, e := range entries {
		indexed[e.Gen] = true
		if e.Gen > max {
			max = e.Gen
		}
	}
	gen := max + 1
	rel := s.extractionArtifactRel(sourceNodeID, safeKind, gen)
	dest := filepath.Join(s.Root, rel)
	if _, err := os.Stat(dest); err == nil {
		if indexed[gen] {
			return 0, fmt.Errorf("extraction gen %d already exists for %s", gen, sourceNodeID)
		}
		if err := os.Remove(dest); err != nil {
			return 0, fmt.Errorf("remove orphan extraction artifact %s: %w", rel, err)
		}
	} else if !os.IsNotExist(err) {
		return 0, err
	}

	art := ExtractionArtifact{
		Kind:         kind,
		KindKnown:    known,
		Extractor:    in.Extractor,
		Provider:     in.Provider,
		Model:        in.Model,
		ModelVersion: in.ModelVersion,
		Language:     in.Language,
		Coverage:     in.Coverage,
		Status:       in.Status,
		Output:       in.Output,
		TS:           time.Now().UTC(),
	}
	payload, err := json.Marshal(art)
	if err != nil {
		return 0, fmt.Errorf("marshal extraction artifact: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, err
	}
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return 0, fmt.Errorf("write pending extraction artifact: %w", err)
	}
	b, err := os.ReadFile(tmp)
	if err != nil {
		return 0, fmt.Errorf("re-read pending extraction artifact: %w", err)
	}
	var parsed ExtractionArtifact
	if err := json.Unmarshal(b, &parsed); err != nil {
		return 0, fmt.Errorf("validate pending extraction artifact: %w", err)
	}
	if parsed.Kind != art.Kind || parsed.Status != art.Status {
		return 0, fmt.Errorf("validate pending extraction artifact: kind/status mismatch")
	}
	if err := os.Rename(tmp, dest); err != nil {
		return 0, fmt.Errorf("commit extraction artifact: %w", err)
	}
	if hook := s.testHookBeforeExtractionIndex; hook != nil {
		hook()
	}
	entry := ExtractionIndexEntry{
		Gen:      gen,
		Kind:     kind,
		Status:   in.Status,
		Artifact: rel,
	}
	if err := appendJSONL(s.extractionIndexPath(sourceNodeID), entry); err != nil {
		return 0, fmt.Errorf("append extraction index: %w", err)
	}
	return gen, nil
}

// ListExtractions returns complete generations for sourceNodeID in gen order.
// A generation is complete only when both its artifact and index line exist.
func (s *Store) ListExtractions(sourceNodeID string) ([]ExtractionIndexEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateFileID("node_id", sourceNodeID); err != nil {
		return nil, err
	}
	entries, err := s.readExtractionIndexLocked(sourceNodeID)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Gen < entries[j].Gen })
	return entries, nil
}

func (s *Store) readExtractionIndexLocked(sourceNodeID string) ([]ExtractionIndexEntry, error) {
	var entries []ExtractionIndexEntry
	if err := readJSONL(s.extractionIndexPath(sourceNodeID), &entries); err != nil {
		return nil, fmt.Errorf("read extraction index: %w", err)
	}
	return entries, nil
}

// LoadExtractionArtifact returns the immutable artifact for one indexed
// generation. Missing or corrupt artifacts fail closed.
func (s *Store) LoadExtractionArtifact(sourceNodeID string, gen int) (*ExtractionArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateFileID("node_id", sourceNodeID); err != nil {
		return nil, err
	}
	entries, err := s.readExtractionIndexLocked(sourceNodeID)
	if err != nil {
		return nil, err
	}
	var rel string
	for _, e := range entries {
		if e.Gen == gen {
			rel = e.Artifact
			break
		}
	}
	if rel == "" {
		return nil, fmt.Errorf("extraction gen %d not found for %s", gen, sourceNodeID)
	}
	path := filepath.Join(s.Root, rel)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read extraction artifact gen %d: %w", gen, err)
	}
	var art ExtractionArtifact
	if err := json.Unmarshal(b, &art); err != nil {
		return nil, fmt.Errorf("parse extraction artifact gen %d: %w", gen, err)
	}
	return &art, nil
}

// PublishDescriptionNode writes a level-0 description node in version whose
// Extraction frontmatter points at an existing immutable generation.
func (s *Store) PublishDescriptionNode(version int, sourceNodeID string, gen int, nodeID, body string) error {
	if err := validateFileID("node_id", nodeID); err != nil {
		return err
	}
	if err := validateFileID("node_id", sourceNodeID); err != nil {
		return err
	}
	src, err := s.lookupSourceNode(sourceNodeID)
	if err != nil {
		return err
	}
	if src == nil {
		return fmt.Errorf("source node %s not found", sourceNodeID)
	}
	art, err := s.LoadExtractionArtifact(sourceNodeID, gen)
	if err != nil {
		return err
	}
	listed, err := s.ListExtractions(sourceNodeID)
	if err != nil {
		return err
	}
	var rel string
	for _, e := range listed {
		if e.Gen == gen {
			rel = e.Artifact
			break
		}
	}
	if rel == "" {
		return fmt.Errorf("extraction gen %d not found for %s", gen, sourceNodeID)
	}
	n := &Node{
		NodeID:         nodeID,
		Body:           body,
		Level:          0,
		Epistemic:      StatusProposed,
		TemporalStatus: TemporalCurrent,
		CreatedBy:      CreatorIngester,
		CreatedVersion: version,
		UpdatedVersion: version,
		ObservedAt:     time.Now().UTC(),
		Extraction: &ExtractionMeta{
			SourceRef:    sourceNodeID,
			Kind:         art.Kind,
			KindKnown:    art.KindKnown,
			Extractor:    art.Extractor,
			Provider:     art.Provider,
			Model:        art.Model,
			ModelVersion: art.ModelVersion,
			Language:     art.Language,
			Coverage:     art.Coverage,
			Generation:   gen,
			ArtifactRef:  rel,
		},
	}
	return s.SaveNode(version, n)
}

func validateExtractionRecordStatus(status string) error {
	switch status {
	case ExtractionCompleted, ExtractionFailed, ExtractionUnsupported:
		return nil
	default:
		return fmt.Errorf("invalid extraction status %q", status)
	}
}

func safeDescriptionKind(kind string, known bool) string {
	if known {
		return kind
	}
	var b strings.Builder
	for _, r := range kind {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" || out == ".." || strings.Contains(out, "..") {
		return "unknown"
	}
	return out
}

// extractionIdentityReject reports when an update_node would clear or change
// the immutable extraction identity on a description node.
func extractionIdentityReject(existing, incoming *Node) string {
	if existing == nil || existing.Extraction == nil || incoming == nil || incoming.Extraction == nil {
		return ""
	}
	ex, in := existing.Extraction, incoming.Extraction
	if in.ArtifactRef != ex.ArtifactRef || in.SourceRef != ex.SourceRef || in.Generation != ex.Generation {
		return "extraction_identity_immutable: cannot clear or change extraction identity"
	}
	return ""
}
