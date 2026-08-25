// Package memorygraph implements the graph memory reviewer subsystem:
// a hierarchical DAG memory with versioned storage, hybrid retrieval,
// explore agents, judge feedback and TTT consolidation.
//
// Design authority: docs/superpowers/specs/2026-08-14-graph-memory-reviewer-design.zh-CN.md
package memorygraph

import (
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// Epistemic status values for nodes (design §4.2).
const (
	StatusProposed   = "proposed"
	StatusSupported  = "supported"
	StatusAccepted   = "accepted"
	StatusContested  = "contested"
	StatusRejected   = "rejected"
	StatusSuperseded = "superseded"
)

// Temporal status values for nodes.
const (
	TemporalCurrent = "current"
	TemporalStale   = "stale"
	TemporalExpired = "expired"
	TemporalUnknown = "unknown"
)

// Edge types.
const (
	EdgeTypeSummarizes = "summarizes" // strict hierarchy DAG, participates in level computation

	// Typed relation edges (may form cycles, may cross levels).
	EdgeTypeCauses           = "causes"
	EdgeTypeEnables          = "enables"
	EdgeTypePrevents         = "prevents"
	EdgeTypeContradicts      = "contradicts"
	EdgeTypeIncompatibleWith = "incompatible_with"
	EdgeTypeCounterexampleTo = "counterexample_to"
	EdgeTypeViolates         = "violates"
	EdgeTypeSupersedes       = "supersedes"
	EdgeTypeRefines          = "refines"
	EdgeTypeInvalidates      = "invalidates"
	EdgeTypeSupports         = "supports"
	EdgeTypeEvidenceFor      = "evidence_for" // cross-level edges of this type are NOT downweighted
	EdgeTypeDerivedFrom      = "derived_from"

	// EdgeTypeHasAttachment is the ingest-owned provenance edge from a
	// segment source node to a file source node (spec §10). It lives in
	// the shared source store, not in version snapshots.
	EdgeTypeHasAttachment = "has_attachment"
)

// Source-layer kinds and extraction status (spec §10). Nodes at
// SourceLayerLevel live in the shared source store, outside versions.
const (
	SourceLayerLevel = -1

	SourceKindSegment = "segment"
	SourceKindFile    = "file"

	ExtractionPending     = "pending"
	ExtractionUnsupported = "unsupported"
	ExtractionFailed      = "failed"
	ExtractionCompleted   = "completed"

	DescriptionKindCaption       = "caption"
	DescriptionKindOCR           = "ocr"
	DescriptionKindTranscript    = "transcript"
	DescriptionKindExtractedText = "extracted_text"
)

// Edge epistemic markers (Q4): everything is a revocable hypothesis unless
// asserted directly by a raw segment.
const (
	EpistemicAsserted = "asserted"
	EpistemicInferred = "inferred"
)

// Creator markers for nodes/edges.
const (
	CreatorIngester     = "ingester"
	CreatorConsolidator = "consolidator"
	CreatorMigration    = "migration"
)

// RelationEdgeTypes is the accepted set for relations.jsonl entries.
var RelationEdgeTypes = map[string]bool{
	EdgeTypeCauses:           true,
	EdgeTypeEnables:          true,
	EdgeTypePrevents:         true,
	EdgeTypeContradicts:      true,
	EdgeTypeIncompatibleWith: true,
	EdgeTypeCounterexampleTo: true,
	EdgeTypeViolates:         true,
	EdgeTypeSupersedes:       true,
	EdgeTypeRefines:          true,
	EdgeTypeInvalidates:      true,
	EdgeTypeSupports:         true,
	EdgeTypeEvidenceFor:      true,
	EdgeTypeDerivedFrom:      true,
}

// Node is one graph node: one .md file = one embedding chunk (design §4.2).
// The frontmatter fields map 1:1 to the yaml block; Body is the chunk text.
type Node struct {
	NodeID         string     `yaml:"node_id" json:"node_id"`
	ContentHash    string     `yaml:"content_hash" json:"content_hash"` // sha256 of Body only
	SegmentRefs    []string   `yaml:"segment_refs,omitempty" json:"segment_refs,omitempty"`
	Level          int        `yaml:"level" json:"level"` // -1 = source layer; 0 = most specific statement layer
	Epistemic      string     `yaml:"epistemic_status" json:"epistemic_status"`
	EntityRefs     []string   `yaml:"entity_refs,omitempty" json:"entity_refs,omitempty"`
	ObservedAt     time.Time  `yaml:"observed_at" json:"observed_at"`
	ValidFrom      *time.Time `yaml:"valid_from" json:"valid_from"`
	ValidTo        *time.Time `yaml:"valid_to" json:"valid_to"`
	RefreshAfter   *time.Time `yaml:"refresh_after" json:"refresh_after"`
	TemporalStatus string     `yaml:"temporal_status" json:"temporal_status"`
	Tags           []string   `yaml:"tags,omitempty" json:"tags,omitempty"`
	CreatedBy      string     `yaml:"created_by" json:"created_by"`
	CreatedVersion int        `yaml:"created_version" json:"created_version"`
	UpdatedVersion int        `yaml:"updated_version" json:"updated_version"`

	// Scope and provenance (spec §5). Empty Visibility reads as "project"
	// for pre-scope graphs. Provenance is monotonic: consolidation may merge
	// source IDs but never remove them.
	Visibility       string   `yaml:"visibility,omitempty" json:"visibility,omitempty"`
	ChannelID        string   `yaml:"channel_id,omitempty" json:"channel_id,omitempty"`
	SourceAgentIDs   []string `yaml:"source_agent_ids,omitempty" json:"source_agent_ids,omitempty"`
	SourceChannelIDs []string `yaml:"source_channel_ids,omitempty" json:"source_channel_ids,omitempty"`
	SourceTaskIDs    []string `yaml:"source_task_ids,omitempty" json:"source_task_ids,omitempty"`

	// Daily-node lifecycle (spec §6). SealedAt is set once by the seal pass
	// (compare-and-swap: an already-sealed daily is immutable); LateForDate
	// records the original local date of events that arrived after their
	// own daily was sealed and landed in this open daily instead.
	SealedAt    *time.Time `yaml:"sealed_at,omitempty" json:"sealed_at,omitempty"`
	LateForDate string     `yaml:"late_for_date,omitempty" json:"late_for_date,omitempty"`

	// Source-layer frontmatter (spec §10). Populated only on level -1
	// nodes in the shared source store; omitempty keeps version nodes unchanged.
	SourceKind       string `yaml:"source_kind,omitempty" json:"source_kind,omitempty"`     // "segment" | "file"
	AttachmentID     string `yaml:"attachment_id,omitempty" json:"attachment_id,omitempty"` // file sources
	BlobSHA256       string `yaml:"blob_sha256,omitempty" json:"blob_sha256,omitempty"`     // file blob identity; never node identity
	MIME             string `yaml:"mime,omitempty" json:"mime,omitempty"`
	SizeBytes        int64  `yaml:"size_bytes,omitempty" json:"size_bytes,omitempty"`
	ExtractionStatus string `yaml:"extraction_status,omitempty" json:"extraction_status,omitempty"` // pending|unsupported|failed|""
	// PromotedFromChannelID is set only by PromoteFileSourceToProject when a
	// channel-graph file source is authorized to become project-visible.
	PromotedFromChannelID string `yaml:"promoted_from_channel_id,omitempty" json:"promoted_from_channel_id,omitempty"`

	// Extraction is the immutable extraction identity on a level-0
	// description node (spec §11). omitempty keeps ordinary statement
	// nodes unchanged.
	Extraction *ExtractionMeta `yaml:"extraction,omitempty" json:"extraction,omitempty"`

	Body string `yaml:"-" json:"body"` // markdown body = embedding chunk
}

// ExtractionMeta is the nested frontmatter on a description node. Artifact
// identity (source_ref, generation, artifact_ref) is immutable under
// management edits; body/language/coverage remain versioned.
type ExtractionMeta struct {
	SourceRef    string `yaml:"source_ref" json:"source_ref"`
	Kind         string `yaml:"kind" json:"kind"`
	KindKnown    bool   `yaml:"kind_known" json:"kind_known"`
	Extractor    string `yaml:"extractor,omitempty" json:"extractor,omitempty"`
	Provider     string `yaml:"provider,omitempty" json:"provider,omitempty"`
	Model        string `yaml:"model,omitempty" json:"model,omitempty"`
	ModelVersion string `yaml:"model_version,omitempty" json:"model_version,omitempty"`
	Language     string `yaml:"language,omitempty" json:"language,omitempty"`
	Coverage     string `yaml:"coverage,omitempty" json:"coverage,omitempty"`
	Generation   int    `yaml:"generation" json:"generation"`
	ArtifactRef  string `yaml:"artifact_ref" json:"artifact_ref"`
}

// GraphView is the caller's visibility scope, reapplied at every retrieval
// and traversal step so edges can never bypass scope (spec §5). The zero
// value is inactive (no filtering), preserving legacy behavior for existing
// callers.
type GraphView struct {
	AllowProject bool
	ChannelID    string // exact-channel visibility allowed; "" = none
}

// Allows reports whether n is visible under v. Empty Visibility reads as
// "project"; unknown visibility values fail closed.
func (v GraphView) Allows(n *Node) bool {
	vis := n.Visibility
	if vis == "" {
		vis = "project"
	}
	switch vis {
	case "project":
		return v.AllowProject
	case "channel":
		return v.ChannelID != "" && n.ChannelID == v.ChannelID
	default:
		return false
	}
}

// SegmentMeta is the scope/provenance sidecar for one staged segment
// (staging/segments/<id>.scope.json), written by the scoped ingest writer
// (spec §5).
type SegmentMeta struct {
	WorkspaceID       string `json:"workspace_id"`
	Visibility        string `json:"visibility"` // "project" | "channel"
	ChannelID         string `json:"channel_id,omitempty"`
	ProjectID         string `json:"project_id,omitempty"`
	AgentID           string `json:"agent_id,omitempty"`
	TaskID            string `json:"task_id,omitempty"`
	LineageGeneration int64  `json:"lineage_generation,omitempty"`
}

// Edge is a first-class object (Q6/7). For hierarchy edges only EdgeID,
// Type=summarizes, From, To, CreatedBy, CreatedVersion are meaningful.
// Relation edges additionally carry status/epistemic/confidence and may
// point at another edge via To="edge:<edge_id>".
type Edge struct {
	EdgeID         string   `json:"edge_id"`
	Type           string   `json:"type"`
	From           string   `json:"from"` // node_id; for summarizes: the parent
	To             string   `json:"to"`   // node_id or "edge:<edge_id>"
	Status         string   `json:"status,omitempty"`
	Epistemic      string   `json:"epistemic,omitempty"`
	Confidence     float64  `json:"confidence,omitempty"`
	Reason         string   `json:"reason,omitempty"`
	SourceRefs     []string `json:"source_refs,omitempty"`
	SourceLevel    int      `json:"source_level,omitempty"`
	TargetLevel    int      `json:"target_level,omitempty"`
	LevelDelta     int      `json:"level_delta,omitempty"`
	CreatedBy      string   `json:"created_by"`
	CreatedVersion int      `json:"created_version"`
}

// IsEdgeRef reports whether the edge target is another edge ("edge:<id>").
func (e *Edge) IsEdgeRef() bool { return len(e.To) > 5 && e.To[:5] == "edge:" }

// Manifest is versions/<vN>/manifest.json metadata.
type Manifest struct {
	Version       int       `json:"version"`
	ParentVersion int       `json:"parent_version"`
	CreatedAt     time.Time `json:"created_at"`
	CreatedBy     string    `json:"created_by"` // "consolidator" | "ttt" | "init" | "migration"
	NodeCount     int       `json:"node_count"`
	HierEdgeCount int       `json:"hier_edge_count"`
	RelEdgeCount  int       `json:"rel_edge_count"`
	Notes         string    `json:"notes,omitempty"`
	// SourceWatermark is the shared source-store journal seq this version
	// may observe (spec §10, D16). Zero (omitempty) means no sources are
	// visible, including pre-source-era manifests.
	SourceWatermark int `json:"source_watermark,omitempty"`
}

// OpLogEntry is one audited consolidation operation (design §5.4, Q16/Q20).
type OpLogEntry struct {
	Seq       int            `json:"seq"`
	Version   int            `json:"version"`
	Timestamp time.Time      `json:"ts"`
	Actor     string         `json:"actor"` // "consolidator" | "ttt-<trajectory>" | "ingester"
	Op        string         `json:"op"`    // add_node|update_node|delete_node|add_edge|delete_edge|update_edge|prune_edge|relevel|switch_version|gc
	Target    string         `json:"target"`
	Detail    map[string]any `json:"detail,omitempty"`
}

// QueryLogEntry records one dispatch query against the graph (design §5.2/5.3).
// Written at recall time; judge fields are filled asynchronously.
type QueryLogEntry struct {
	TraceID   string    `json:"trace_id"`
	Query     string    `json:"query"`
	Timestamp time.Time `json:"ts"`
	Version   int       `json:"version"`
	NodeIDs   []string  `json:"node_ids"` // cited nodes of the adopted path
	Rounds    int       `json:"rounds"`
	AgentRuns int       `json:"agent_runs"` // K for TTT
	Found     bool      `json:"found"`
	PriorUsed bool      `json:"prior_used,omitempty"`

	// Judge write-back (async):
	JudgeDone     bool     `json:"judge_done"`
	JudgeScore    float64  `json:"judge_score"`
	RelevantNodes []string `json:"relevant_nodes,omitempty"` // ground truth set
	// BaselineCovered/BaselineTopK record the judge-time baseline coverage
	// signal (design Q13/A2, review R10): whether the ground truth set lay
	// within the n-hop neighborhood of the hybrid top-k hits on the version
	// current at judge time, and those hit ids. BaselineDist is retained for
	// backward compatibility with pre-A2 files but is no longer written or
	// relied on.
	BaselineCovered bool     `json:"baseline_covered,omitempty"`
	BaselineTopK    []string `json:"baseline_top_k,omitempty"`
	BaselineDist    float64  `json:"baseline_dist,omitempty"` // deprecated: superseded by BaselineCovered/BaselineTopK

	// InfoItems and the ledger/trajectory identities identify Dive-era
	// records. They are absent from flat pre-Dive judge records.
	InfoItems    []BacktestItem `json:"info_items,omitempty"`
	LedgerID     string         `json:"ledger_id,omitempty"`
	TrajectoryID string         `json:"trajectory_id,omitempty"`
	// LegacyNonAuthoritative keeps a migrated pre-Dive record readable for
	// audit while excluding it from any authoritative evaluation path.
	LegacyNonAuthoritative bool `json:"legacy_non_authoritative,omitempty"`
}

// ExploreRun is one explore-agent trajectory (K of them in TTT mode, Q17).
type ExploreRun struct {
	RunID   string `json:"run_id"`
	Seed    int    `json:"seed"`
	Found   bool   `json:"found"`
	Summary string `json:"summary"`
	// NodeIDs are the node ids the tool-server submission cited (A4:
	// persisted separately from the viewed set).
	NodeIDs []string `json:"node_ids"`
	// ViewedNodeIDs are the node ids the trajectory actually viewed in full
	// (A4); the submitted ids are a subset of these.
	ViewedNodeIDs []string `json:"viewed_node_ids,omitempty"`
	Rounds        int      `json:"rounds"`
	Error         string   `json:"error,omitempty"`
	// Messages is the run's drained message stream, captured for every run
	// and cleared on all runs after adoption (only the sanitized adopted
	// transcript travels on). Transport for the prior record, never
	// persisted.
	Messages []agent.Message `json:"-"`
}

// Citation is a qualified adopted-node reference (spec §3 step 8): the
// node id plus its level and epistemic status read from the pinned graph
// version. Level is -1 for non-graph ids (staging segments) or when the
// pinned graph could not be read.
type Citation struct {
	NodeID         string    `json:"node_id"`
	GraphVersion   int       `json:"graph_version,omitempty"`
	Level          int       `json:"level"`
	Epistemic      string    `json:"epistemic_status,omitempty"`
	Tags           []string  `json:"tags,omitempty"`
	Title          string    `json:"title,omitempty"`
	FirstParagraph string    `json:"first_paragraph,omitempty"`
	Excerpt        string    `json:"excerpt,omitempty"`
	ContentHash    string    `json:"content_hash,omitempty"`
	CapturedAt     time.Time `json:"captured_at,omitempty"`
}

// RecallResult is what Retrieve returns to the downstream agent (Q25).
type RecallResult struct {
	Summary   string       `json:"summary"`
	NodeIDs   []string     `json:"node_ids"`
	Citations []Citation   `json:"citations,omitempty"` // qualified form of NodeIDs
	TraceID   string       `json:"trace_id"`            // query_id: judge write-back & reward composition key
	Rounds    int          `json:"rounds"`
	AgentRuns []ExploreRun `json:"agent_runs,omitempty"`
	Found     bool         `json:"found"`
	// AdoptedIndex points into AgentRuns at the adopted trajectory (-1 on
	// miss). AdoptedTranscript is the adopted run's message stream
	// sanitized to the allowlisted TraceMessage shape (Phase 2 prior
	// record input).
	AdoptedIndex      int            `json:"adopted_index,omitempty"`
	AdoptedTranscript []TraceMessage `json:"adopted_transcript,omitempty"`
	// Version is the graph version the explore was pinned to for the whole
	// call (design Q13/R5: a mid-explore version switch never swaps the
	// graph under an in-flight trajectory).
	Version int `json:"version"`
}
