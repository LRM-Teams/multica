package researcheval

import "context"

const CorpusSchemaVersion = "research-eval-corpus-v1"

type ResearchMode string

const (
	ModeExplorationMap       ResearchMode = "exploration_map"
	ModeComparativeDecision  ResearchMode = "comparative_decision"
	ModeFactCheck            ResearchMode = "fact_check"
	ModeSystematicReview     ResearchMode = "systematic_review"
	ModeCausalInvestigation  ResearchMode = "causal_investigation"
	ModeRiskDueDiligence     ResearchMode = "risk_due_diligence"
	ModeTechnicalFeasibility ResearchMode = "technical_feasibility"
	ModeTemporalMonitoring   ResearchMode = "temporal_monitoring"
)

var AllResearchModes = []ResearchMode{
	ModeExplorationMap,
	ModeComparativeDecision,
	ModeFactCheck,
	ModeSystematicReview,
	ModeCausalInvestigation,
	ModeRiskDueDiligence,
	ModeTechnicalFeasibility,
	ModeTemporalMonitoring,
}

// RequiredProjectionDetailFields is the minimum stable detail contract for an
// observable research graph node. A value such as "not_applicable" is still
// required when a field does not apply, so the UI never has to infer omission.
var RequiredProjectionDetailFields = []string{
	"purpose",
	"objective",
	"entry_condition",
	"method",
	"input_artifacts",
	"actions_taken",
	"actor",
	"result",
	"evidence",
	"decision",
	"failure",
	"recovery",
	"upstream",
	"downstream",
}

// RequiredProjectionNodeKinds freezes the V6 node coverage expected from the
// production projection adapter. Future kinds may use generic rendering, but
// removing one of these kinds is a regression.
var RequiredProjectionNodeKinds = []string{
	"task",
	"attempt",
	"result_artifact",
	"search_plan",
	"query_execution",
	"source_candidate",
	"screening_decision",
	"source_snapshot",
	"observation",
	"claim",
	"question",
	"hypothesis",
	"branch",
	"insight",
	"insight_derivation",
	"integration_round",
	"integration_contribution",
	"dispute",
	"dispute_position",
	"deliberation",
	"deliberation_turn",
	"decision",
	"team_formation",
	"team_membership",
	"divergence_pass",
	"capability_observation",
	"report_revision",
	"evaluation_defect",
	"monitoring_cycle",
	"episode",
}

type Corpus struct {
	SchemaVersion string `json:"schema_version"`
	Version       string `json:"version"`
	Cases         []Case `json:"cases"`
}

type Case struct {
	Task        TaskSpec    `json:"task"`
	Environment Environment `json:"environment"`
	Oracle      Oracle      `json:"oracle"`
}

type TaskSpec struct {
	ID           string       `json:"id"`
	Mode         ResearchMode `json:"mode"`
	Goal         string       `json:"goal"`
	Language     string       `json:"language"`
	AllowedTools []string     `json:"allowed_tools"`
	Tags         []string     `json:"tags"`
}

type Environment struct {
	Documents []Document `json:"documents"`
	Faults    []Fault    `json:"faults,omitempty"`
}

type Document struct {
	ID        string   `json:"id"`
	Family    string   `json:"family"`
	Title     string   `json:"title"`
	Version   string   `json:"version,omitempty"`
	Published string   `json:"published,omitempty"`
	Traits    []string `json:"traits,omitempty"`
	Content   string   `json:"content"`
}

type Fault struct {
	Kind     string `json:"kind"`
	TargetID string `json:"target_id,omitempty"`
	Trigger  string `json:"trigger,omitempty"`
}

type Oracle struct {
	RequiredFacts        []ExpectedFact        `json:"required_facts"`
	ForbiddenFactKeys    []string              `json:"forbidden_fact_keys,omitempty"`
	RequiredConflicts    []ExpectedConflict    `json:"required_conflicts,omitempty"`
	ForbiddenDocumentIDs []string              `json:"forbidden_document_ids,omitempty"`
	MaxAcceptedPerFamily map[string]int        `json:"max_accepted_per_family,omitempty"`
	RequiredReportClaims []ExpectedReportClaim `json:"required_report_claims,omitempty"`
	Autonomy             *AutonomyOracle       `json:"autonomy,omitempty"`
}

type ExpectedFact struct {
	Key               string   `json:"key"`
	Value             string   `json:"value"`
	RequiredSourceIDs []string `json:"required_source_ids"`
}

type ExpectedConflict struct {
	Key  string `json:"key"`
	Type string `json:"type"`
}

type ExpectedReportClaim struct {
	Key              string   `json:"key"`
	RequiredFactKeys []string `json:"required_fact_keys"`
}

type AutonomyOracle struct {
	RequiredActions  []ExpectedAction    `json:"required_actions,omitempty"`
	ForbiddenActions []ExpectedAction    `json:"forbidden_actions,omitempty"`
	RequiredNodes    []ExpectedGraphNode `json:"required_nodes,omitempty"`
	RequiredEdges    []ExpectedGraphEdge `json:"required_edges,omitempty"`
	Projection       *ExpectedProjection `json:"projection,omitempty"`
}

type ExpectedAction struct {
	Kind    string `json:"kind"`
	Actor   string `json:"actor,omitempty"`
	Target  string `json:"target,omitempty"`
	Outcome string `json:"outcome,omitempty"`
}

type ExpectedGraphNode struct {
	Key             string `json:"key"`
	Kind            string `json:"kind"`
	Status          string `json:"status"`
	Level           int    `json:"level,omitempty"`
	DetailsComplete bool   `json:"details_complete"`
}

type ExpectedGraphEdge struct {
	FromKey string `json:"from_key"`
	ToKey   string `json:"to_key"`
	Type    string `json:"type"`
}

type ExpectedProjection struct {
	RequireHashMatch   bool `json:"require_hash_match"`
	RequireGapResync   bool `json:"require_gap_resync"`
	RequireUniqueNodes bool `json:"require_unique_nodes"`
	MinimumTotalNodes  int  `json:"minimum_total_nodes,omitempty"`
	MaximumPageNodes   int  `json:"maximum_page_nodes,omitempty"`
}

// SubjectInput deliberately excludes Oracle. Executors receive the task and
// controlled environment, while graders retain hidden expected outcomes.
type SubjectInput struct {
	Task        TaskSpec    `json:"task"`
	Environment Environment `json:"environment"`
}

func (evaluationCase Case) SubjectInput() SubjectInput {
	return SubjectInput{Task: evaluationCase.Task, Environment: evaluationCase.Environment}
}

type Artifact struct {
	Sources    []ArtifactSource    `json:"sources"`
	Facts      []ArtifactFact      `json:"facts"`
	Claims     []ArtifactClaim     `json:"claims"`
	Conflicts  []ArtifactConflict  `json:"conflicts"`
	Actions    []ArtifactAction    `json:"actions,omitempty"`
	GraphNodes []ArtifactGraphNode `json:"graph_nodes,omitempty"`
	GraphEdges []ArtifactGraphEdge `json:"graph_edges,omitempty"`
	Projection *ArtifactProjection `json:"projection,omitempty"`
	ReportMD   string              `json:"report_md,omitempty"`
}

type ArtifactSource struct {
	DocumentID string `json:"document_id"`
	Family     string `json:"family"`
	Accepted   bool   `json:"accepted"`
}

type ArtifactFact struct {
	Key       string   `json:"key"`
	Value     string   `json:"value"`
	SourceIDs []string `json:"source_ids"`
}

type ArtifactClaim struct {
	Key       string   `json:"key"`
	FactKeys  []string `json:"fact_keys"`
	SourceIDs []string `json:"source_ids"`
	InReport  bool     `json:"in_report"`
}

type ArtifactConflict struct {
	Key      string   `json:"key"`
	Type     string   `json:"type"`
	FactKeys []string `json:"fact_keys,omitempty"`
	Resolved bool     `json:"resolved"`
}

type ArtifactAction struct {
	Kind    string `json:"kind"`
	Actor   string `json:"actor,omitempty"`
	Target  string `json:"target,omitempty"`
	Outcome string `json:"outcome,omitempty"`
}

type ArtifactGraphNode struct {
	ID      string            `json:"id"`
	Key     string            `json:"key"`
	Kind    string            `json:"kind"`
	Status  string            `json:"status"`
	Level   int               `json:"level,omitempty"`
	Details map[string]string `json:"details,omitempty"`
}

type ArtifactGraphEdge struct {
	ID         string `json:"id"`
	FromNodeID string `json:"from_node_id"`
	ToNodeID   string `json:"to_node_id"`
	Type       string `json:"type"`
}

type ArtifactProjection struct {
	SnapshotHash     string   `json:"snapshot_hash"`
	ReplayHash       string   `json:"replay_hash"`
	ObservedNodeIDs  []string `json:"observed_node_ids"`
	TotalNodes       int      `json:"total_nodes"`
	LargestPageNodes int      `json:"largest_page_nodes"`
	GapDetected      bool     `json:"gap_detected"`
	ResyncRequested  bool     `json:"resync_requested"`
}

type Executor interface {
	Execute(context.Context, SubjectInput, int64) (Artifact, error)
}

type Grader interface {
	Name() string
	Grade(context.Context, Case, Artifact) (Grade, error)
}

type Grade struct {
	Score    float64            `json:"score"`
	Passed   bool               `json:"passed"`
	Metrics  map[string]float64 `json:"metrics,omitempty"`
	Findings []Finding          `json:"findings,omitempty"`
}

type Finding struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type RunOptions struct {
	Seeds           []int64 `json:"seeds"`
	MinimumScore    float64 `json:"minimum_score"`
	MinimumPassRate float64 `json:"minimum_pass_rate"`
}

type TrialResult struct {
	TaskID         string           `json:"task_id"`
	Seed           int64            `json:"seed"`
	ExecutionError string           `json:"execution_error,omitempty"`
	Grades         map[string]Grade `json:"grades"`
	Score          float64          `json:"score"`
	Passed         bool             `json:"passed"`
}

type Aggregate struct {
	MeanScore float64 `json:"mean_score"`
	PassRate  float64 `json:"pass_rate"`
	Trials    int     `json:"trials"`
}

type Report struct {
	SchemaVersion string               `json:"schema_version"`
	CorpusVersion string               `json:"corpus_version"`
	Seeds         []int64              `json:"seeds"`
	Options       RunOptions           `json:"options"`
	Trials        []TrialResult        `json:"trials"`
	ByGrader      map[string]Aggregate `json:"by_grader"`
	Overall       Aggregate            `json:"overall"`
	Passed        bool                 `json:"passed"`
}

type Comparison struct {
	BaselineCorpusVersion  string             `json:"baseline_corpus_version"`
	CandidateCorpusVersion string             `json:"candidate_corpus_version"`
	OverallScoreDelta      float64            `json:"overall_score_delta"`
	OverallPassRateDelta   float64            `json:"overall_pass_rate_delta"`
	GraderScoreDelta       map[string]float64 `json:"grader_score_delta"`
	MissingGraders         []string           `json:"missing_graders,omitempty"`
	IncomparableReasons    []string           `json:"incomparable_reasons,omitempty"`
	NonRegressing          bool               `json:"non_regressing"`
}
