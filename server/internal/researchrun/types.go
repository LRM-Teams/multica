package researchrun

import (
	"encoding/json"
	"time"
)

const (
	OrchestratorVersionV1 = "research-run-v1"
	OrchestratorVersionV2 = "research-run-v2"
	OrchestratorVersionV3 = "research-run-v3"
	OrchestratorVersionV4 = "research-run-v4"
	OrchestratorVersionV5 = "research-run-v5"
	OrchestratorVersion   = OrchestratorVersionV5
)

type RunStatus string

const (
	RunStatusDrafting            RunStatus = "drafting"
	RunStatusRunning             RunStatus = "running"
	RunStatusAwaitingUserConfirm RunStatus = "awaiting_user_confirm"
	RunStatusCompleted           RunStatus = "completed"
	RunStatusPaused              RunStatus = "paused"
	RunStatusFailed              RunStatus = "failed"
	RunStatusCancelled           RunStatus = "cancelled"
	RunStatusArchived            RunStatus = "archived"
)

type TaskKind string

const (
	TaskKindPlan          TaskKind = "plan"
	TaskKindDiscover      TaskKind = "discover"
	TaskKindDeepRead      TaskKind = "deep_read"
	TaskKindVerify        TaskKind = "verify"
	TaskKindCounterSearch TaskKind = "counter_search"
	TaskKindReplan        TaskKind = "replan"
	TaskKindSynthesize    TaskKind = "synthesize"
	TaskKindQualityGate   TaskKind = "quality_gate"
	TaskKindCitationAudit TaskKind = "citation_audit"
)

type TaskStatus string

const (
	TaskStatusPending     TaskStatus = "pending"
	TaskStatusReady       TaskStatus = "ready"
	TaskStatusDispatching TaskStatus = "dispatching"
	TaskStatusRunning     TaskStatus = "running"
	TaskStatusSucceeded   TaskStatus = "succeeded"
	TaskStatusFailed      TaskStatus = "failed"
	TaskStatusBlocked     TaskStatus = "blocked"
	TaskStatusObsolete    TaskStatus = "obsolete"
	TaskStatusCancelled   TaskStatus = "cancelled"
)

type AttemptStatus string

const (
	AttemptStatusDispatching AttemptStatus = "dispatching"
	AttemptStatusRunning     AttemptStatus = "running"
	AttemptStatusSucceeded   AttemptStatus = "succeeded"
	AttemptStatusFailed      AttemptStatus = "failed"
	AttemptStatusCancelled   AttemptStatus = "cancelled"
	AttemptStatusLost        AttemptStatus = "lost"
)

type QuestionKind string

const (
	QuestionKindDimension     QuestionKind = "dimension"
	QuestionKindHypothesis    QuestionKind = "hypothesis"
	QuestionKindContradiction QuestionKind = "contradiction"
	QuestionKindGap           QuestionKind = "gap"
	QuestionKindFollowUp      QuestionKind = "follow_up"
)

type QuestionStatus string

const (
	QuestionStatusOpen       QuestionStatus = "open"
	QuestionStatusInProgress QuestionStatus = "in_progress"
	QuestionStatusAnswered   QuestionStatus = "answered"
	QuestionStatusUnresolved QuestionStatus = "unresolved"
	QuestionStatusObsolete   QuestionStatus = "obsolete"
)

type ClaimStatus string

const (
	ClaimStatusProposed   ClaimStatus = "proposed"
	ClaimStatusSupported  ClaimStatus = "supported"
	ClaimStatusDisputed   ClaimStatus = "disputed"
	ClaimStatusRefuted    ClaimStatus = "refuted"
	ClaimStatusSuperseded ClaimStatus = "superseded"
	ClaimStatusUnresolved ClaimStatus = "unresolved"
)

type RunConfig struct {
	MaxTasks              int     `json:"max_tasks"`
	MaxParallelTasks      int     `json:"max_parallel_tasks"`
	MaxAttemptsPerTask    int     `json:"max_attempts_per_task"`
	MaxSnapshotBytes      int     `json:"max_snapshot_bytes"`
	MaxResultBytes        int     `json:"max_result_bytes"`
	MaxRunSeconds         int     `json:"max_run_seconds"`
	TaskTimeoutSeconds    int     `json:"task_timeout_seconds"`
	StaleAfterSeconds     int     `json:"stale_after_seconds"`
	MarginalGainThreshold float64 `json:"marginal_gain_threshold"`
	MarginalGainRounds    int     `json:"marginal_gain_rounds"`
}

func DefaultRunConfig(depthTier string) RunConfig {
	switch depthTier {
	case "shallow":
		return RunConfig{MaxTasks: 20, MaxParallelTasks: 2, MaxAttemptsPerTask: 3, MaxSnapshotBytes: 64 << 10, MaxResultBytes: 512 << 10, MaxRunSeconds: 2 * 60 * 60, TaskTimeoutSeconds: 1200, StaleAfterSeconds: 600, MarginalGainThreshold: 0.04, MarginalGainRounds: 2}
	case "deep":
		return RunConfig{MaxTasks: 180, MaxParallelTasks: 10, MaxAttemptsPerTask: 4, MaxSnapshotBytes: 128 << 10, MaxResultBytes: 1 << 20, MaxRunSeconds: 24 * 60 * 60, TaskTimeoutSeconds: 3600, StaleAfterSeconds: 1200, MarginalGainThreshold: 0.02, MarginalGainRounds: 3}
	default:
		return RunConfig{MaxTasks: 60, MaxParallelTasks: 5, MaxAttemptsPerTask: 3, MaxSnapshotBytes: 64 << 10, MaxResultBytes: 512 << 10, MaxRunSeconds: 8 * 60 * 60, TaskTimeoutSeconds: 1800, StaleAfterSeconds: 900, MarginalGainThreshold: 0.03, MarginalGainRounds: 2}
	}
}

type RunStats struct {
	AcceptedResults       int     `json:"accepted_results"`
	EvidenceBatches       int     `json:"evidence_batches"`
	LowGainStreak         int     `json:"low_gain_streak"`
	LastCoverageDelta     float64 `json:"last_coverage_delta"`
	LastMeasuredGain      float64 `json:"last_measured_gain"`
	LastConfidence        float64 `json:"last_confidence"`
	SourcesCreated        int     `json:"sources_created"`
	ObservationsCreated   int     `json:"observations_created"`
	ClaimsCreated         int     `json:"claims_created"`
	BudgetExhaustionCount int     `json:"budget_exhaustion_count"`
}

type ResearchContract struct {
	GoalVersion  int             `json:"goal_version"`
	Goal         string          `json:"goal"`
	Scope        json.RawMessage `json:"scope"`
	Audience     string          `json:"audience"`
	Freshness    string          `json:"freshness"`
	Language     string          `json:"language"`
	SourcePolicy json.RawMessage `json:"source_policy"`
	RunLimits    json.RawMessage `json:"run_limits"`
	Reason       string          `json:"reason"`
	CreatedAt    time.Time       `json:"created_at"`
}

// ResearchMethod is the accepted, versioned method shared by every task in a
// plan. It is derived from a v3 or v4 plan result and stored in the append-only
// decision ledger; Agents cannot supply its attribution or version fields.
type ResearchMethod struct {
	GoalVersion             int                `json:"goal_version"`
	PlanVersion             int                `json:"plan_version"`
	DecisionQuestion        string             `json:"decision_question"`
	MethodRationale         string             `json:"method_rationale"`
	AnalysisMethods         []string           `json:"analysis_methods"`
	EvidenceRequirements    []string           `json:"evidence_requirements"`
	EvidenceStandards       []EvidenceStandard `json:"evidence_standards,omitempty"`
	InclusionCriteria       []string           `json:"inclusion_criteria"`
	ExclusionCriteria       []string           `json:"exclusion_criteria"`
	SourceStrategy          []string           `json:"source_strategy"`
	CounterevidenceStrategy []string           `json:"counterevidence_strategy"`
	StoppingConditions      []string           `json:"stopping_conditions"`
	Uncertainties           []string           `json:"uncertainties"`
	PlanningRisks           []string           `json:"planning_risks"`
	CreatedByTaskID         string             `json:"created_by_task_id"`
	CreatedByAgentID        string             `json:"created_by_agent_id"`
	CreatedAt               time.Time          `json:"created_at"`
}

// EvidenceStandard is a method-owned, machine-checkable evidence requirement
// referenced by Claims. RequiredSourceTraits are satisfied across the set of
// eligible independent sources; one source does not need every trait.
type EvidenceStandard struct {
	ClientKey                 string   `json:"client_key"`
	Purpose                   string   `json:"purpose"`
	MinimumIndependentSources int      `json:"minimum_independent_sources"`
	RequiredSourceTraits      []string `json:"required_source_traits"`
	MinimumStrength           float64  `json:"minimum_strength"`
	MinimumDirectness         float64  `json:"minimum_directness"`
	MinimumMethodFit          float64  `json:"minimum_method_fit"`
	CounterevidenceRequired   bool     `json:"counterevidence_required"`
}

type Run struct {
	SessionID           string     `json:"session_id"`
	WorkspaceID         string     `json:"workspace_id"`
	FleetID             string     `json:"fleet_id"`
	CreatedBy           string     `json:"created_by"`
	Title               string     `json:"title"`
	Goal                string     `json:"goal"`
	Status              RunStatus  `json:"status"`
	CurrentStage        string     `json:"current_stage"`
	DepthTier           string     `json:"depth_tier"`
	GoalVersion         int        `json:"goal_version"`
	PlanVersion         int        `json:"plan_version"`
	StateVersion        int64      `json:"state_version"`
	OrchestratorVersion string     `json:"orchestrator_version"`
	Config              RunConfig  `json:"config"`
	Stats               RunStats   `json:"stats"`
	InitializedAt       *time.Time `json:"initialized_at,omitempty"`
	LastProgressAt      time.Time  `json:"last_progress_at"`
	NextReconcileAt     time.Time  `json:"next_reconcile_at"`
	StopReason          string     `json:"stop_reason,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
}

type Question struct {
	ID                  string         `json:"id"`
	SessionID           string         `json:"session_id"`
	ParentQuestionID    string         `json:"parent_question_id,omitempty"`
	CreatedByTaskID     string         `json:"created_by_task_id,omitempty"`
	ClientKey           string         `json:"client_key"`
	Kind                QuestionKind   `json:"kind"`
	Question            string         `json:"question"`
	Required            bool           `json:"required"`
	Status              QuestionStatus `json:"status"`
	Priority            float64        `json:"priority"`
	Impact              float64        `json:"impact"`
	Uncertainty         float64        `json:"uncertainty"`
	Novelty             float64        `json:"novelty"`
	Coverage            float64        `json:"coverage"`
	GoalVersion         int            `json:"goal_version"`
	PlanVersion         int            `json:"plan_version"`
	AnswerClaimID       string         `json:"answer_claim_id,omitempty"`
	TerminalExplanation string         `json:"terminal_explanation,omitempty"`
}

type Task struct {
	ID                 string          `json:"id"`
	SessionID          string          `json:"session_id"`
	WorkspaceID        string          `json:"workspace_id"`
	QuestionID         string          `json:"question_id,omitempty"`
	ParentTaskID       string          `json:"parent_task_id,omitempty"`
	ClientKey          string          `json:"client_key"`
	Kind               TaskKind        `json:"kind"`
	Objective          string          `json:"objective"`
	RequiredCapability string          `json:"required_capability"`
	ExpectedResult     string          `json:"expected_result"`
	AcceptanceCriteria json.RawMessage `json:"acceptance_criteria"`
	Priority           float64         `json:"priority"`
	Status             TaskStatus      `json:"status"`
	AssignedAgentID    string          `json:"assigned_agent_id,omitempty"`
	GoalVersion        int             `json:"goal_version"`
	PlanVersion        int             `json:"plan_version"`
	MaxAttempts        int             `json:"max_attempts"`
	TimeoutSeconds     int             `json:"timeout_seconds"`
	AttemptCount       int             `json:"attempt_count"`
	ReadyAt            *time.Time      `json:"ready_at,omitempty"`
	StartedAt          *time.Time      `json:"started_at,omitempty"`
	CompletedAt        *time.Time      `json:"completed_at,omitempty"`
	TerminalReason     string          `json:"terminal_reason,omitempty"`
}

type Attempt struct {
	ID                string        `json:"id"`
	SessionID         string        `json:"session_id"`
	WorkspaceID       string        `json:"workspace_id"`
	TaskID            string        `json:"task_id"`
	AttemptNumber     int           `json:"attempt_number"`
	AssignedAgentID   string        `json:"assigned_agent_id"`
	InboxTaskID       string        `json:"inbox_task_id,omitempty"`
	DispatchKey       string        `json:"dispatch_key"`
	ClientRequestID   string        `json:"client_request_id,omitempty"`
	Status            AttemptStatus `json:"status"`
	ResultHash        string        `json:"result_hash,omitempty"`
	FailureClass      string        `json:"failure_class,omitempty"`
	Diagnostics       string        `json:"diagnostics,omitempty"`
	DispatchedAt      time.Time     `json:"dispatched_at"`
	StartedAt         *time.Time    `json:"started_at,omitempty"`
	ResultSubmittedAt *time.Time    `json:"result_submitted_at,omitempty"`
	CompletedAt       *time.Time    `json:"completed_at,omitempty"`
}

type PendingCancellation struct {
	AttemptID    string
	InboxTaskID  string
	DispatchKey  string
	DispatchedAt time.Time
}

type SourceSnapshotView struct {
	ID                 string          `json:"id"`
	ProducedByTaskID   string          `json:"produced_by_task_id,omitempty"`
	CanonicalURL       string          `json:"canonical_url"`
	Title              string          `json:"title"`
	Publisher          string          `json:"publisher"`
	SourceClass        string          `json:"source_class"`
	EvidenceTraits     []string        `json:"evidence_traits,omitempty"`
	IndependenceKey    string          `json:"independence_key"`
	RetrievedAt        time.Time       `json:"retrieved_at"`
	ContentHash        string          `json:"content_hash"`
	SnapshotExcerpt    string          `json:"snapshot_excerpt"`
	Metadata           json.RawMessage `json:"metadata"`
	VerificationStatus string          `json:"verification_status"`
	CreatedAt          time.Time       `json:"created_at"`
}

type Observation struct {
	ID                 string          `json:"id"`
	SourceSnapshotID   string          `json:"source_snapshot_id"`
	ProducedByTaskID   string          `json:"produced_by_task_id,omitempty"`
	Quote              string          `json:"quote,omitempty"`
	Datum              json.RawMessage `json:"datum"`
	Locator            string          `json:"locator,omitempty"`
	Interpretation     string          `json:"interpretation,omitempty"`
	VerificationStatus string          `json:"verification_status"`
	CreatedAt          time.Time       `json:"created_at"`
}

type ClaimEvidence struct {
	ObservationID      string  `json:"observation_id"`
	Relation           string  `json:"relation"`
	Strength           float64 `json:"strength"`
	Directness         float64 `json:"directness,omitempty"`
	MethodFit          float64 `json:"method_fit,omitempty"`
	VerificationStatus string  `json:"verification_status"`
	VerifiedByTaskID   string  `json:"verified_by_task_id,omitempty"`
	Rationale          string  `json:"rationale,omitempty"`
}

type Claim struct {
	ID                  string          `json:"id"`
	ProducedByTaskID    string          `json:"produced_by_task_id,omitempty"`
	ClientKey           string          `json:"client_key"`
	EvidenceStandardKey string          `json:"evidence_standard_key,omitempty"`
	Text                string          `json:"text"`
	Significance        string          `json:"significance"`
	Confidence          float64         `json:"confidence"`
	Status              ClaimStatus     `json:"status"`
	GoalVersion         int             `json:"goal_version"`
	PlanVersion         int             `json:"plan_version"`
	Resolution          string          `json:"resolution,omitempty"`
	Evidence            []ClaimEvidence `json:"evidence"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

type FleetMember struct {
	AgentID string
	Role    string
	Status  string
	IsLead  bool
}

type StartInput struct {
	SessionID          string
	WorkspaceID        string
	FleetID            string
	CreatedBy          string
	LeadAgentID        string
	Goal               string
	Title              string
	DepthTier          string
	Language           string
	SourcePolicy       json.RawMessage
	ProductRound       int32
	ProductRoundBudget int32
}

type SteerInput struct {
	SessionID          string
	WorkspaceID        string
	UserID             string
	Goal               string
	Reason             string
	AllowRunningFinish bool
	Scope              json.RawMessage
	Audience           *string
	Freshness          *string
	Language           *string
	SourcePolicy       json.RawMessage
	RunLimits          json.RawMessage
}

type DispatchRequest struct {
	Run         Run    `json:"run"`
	Task        Task   `json:"task"`
	AttemptID   string `json:"attempt_id"`
	AgentID     string `json:"agent_id"`
	Prompt      string `json:"prompt"`
	Key         string `json:"key"`
	RequestHash string `json:"request_hash"`
}

type CreateDispatchIntentInput struct {
	AttemptID            string
	SessionID            string
	TaskID               string
	AgentID              string
	ExpectedStateVersion int64
	Request              DispatchRequest
}

type DispatchIntent struct {
	ID               string
	AttemptID        string
	SessionID        string
	Request          DispatchRequest
	DeliveryAttempts int
}

type DispatchResult struct {
	InboxTaskID string
}

type InboxTaskState struct {
	ID            string
	Status        string
	FailureReason string
	Retryable     bool
	CompletedAt   *time.Time
}

type RunEvent struct {
	ID                 string          `json:"id"`
	WorkspaceID        string          `json:"workspace_id"`
	SessionID          string          `json:"session_id"`
	Sequence           int64           `json:"sequence"`
	Type               string          `json:"type"`
	IdempotencyKey     string          `json:"idempotency_key"`
	ActorType          string          `json:"actor_type"`
	ActorID            string          `json:"actor_id"`
	Payload            json.RawMessage `json:"payload"`
	ProjectionAttempts int             `json:"projection_attempts"`
	CreatedAt          time.Time       `json:"created_at"`
}

type GateFinding struct {
	Code     string         `json:"code"`
	Severity string         `json:"severity"`
	Message  string         `json:"message"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type GateResult struct {
	Passed   bool          `json:"passed"`
	Findings []GateFinding `json:"findings"`
}

type RunSnapshot struct {
	Run          Run                  `json:"run"`
	Contract     ResearchContract     `json:"contract"`
	Method       *ResearchMethod      `json:"method,omitempty"`
	Questions    []Question           `json:"questions"`
	Tasks        []Task               `json:"tasks"`
	Attempts     []Attempt            `json:"attempts"`
	Sources      []SourceSnapshotView `json:"sources"`
	Observations []Observation        `json:"observations"`
	Claims       []Claim              `json:"claims"`
	Gate         GateResult           `json:"gate"`
}
