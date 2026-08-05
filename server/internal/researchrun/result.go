package researchrun

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	ErrInvalidResult         = errors.New("invalid research task result")
	ErrResultConflict        = errors.New("research task result conflicts with accepted result")
	ErrAttemptNotAssigned    = errors.New("research task attempt is not assigned to this agent")
	ErrInvalidTransition     = errors.New("invalid research run transition")
	ErrRunNotFound           = errors.New("research run not found")
	ErrLeaseUnavailable      = errors.New("research run reconciliation lease unavailable")
	ErrCapabilityUnavailable = errors.New("research run capability unavailable")
	ErrBudgetExhausted       = errors.New("research run budget exhausted")
	ErrInvalidContract       = errors.New("invalid research contract")
	ErrUnsupportedVersion    = errors.New("unsupported research orchestrator version")
)

const (
	maxClientKeyBytes     = 160
	maxTaskObjectiveBytes = 12 << 10
	maxClaimTextBytes     = 16 << 10
	maxSummaryBytes       = 64 << 10
	maxResultItems        = 256
	maxMetadataBytes      = 64 << 10
	maxReportBytes        = 2 << 20
)

type ResultEnvelope struct {
	SchemaVersion    int                   `json:"schema_version"`
	ClientRequestID  string                `json:"client_request_id"`
	Summary          string                `json:"summary"`
	Questions        []QuestionProposal    `json:"questions,omitempty"`
	Plan             *PlanProposal         `json:"plan,omitempty"`
	Sources          []SourceProposal      `json:"sources,omitempty"`
	Observations     []ObservationProposal `json:"observations,omitempty"`
	Claims           []ClaimProposal       `json:"claims,omitempty"`
	ProposedTasks    []TaskProposal        `json:"proposed_tasks,omitempty"`
	Report           *ReportProposal       `json:"report,omitempty"`
	Evaluation       *EvaluationProposal   `json:"evaluation,omitempty"`
	AnswerClaimKey   string                `json:"answer_claim_key,omitempty"`
	CoverageDelta    float64               `json:"coverage_delta"`
	Confidence       float64               `json:"confidence"`
	IncompleteReason *string               `json:"incomplete_reason,omitempty"`
}

// DecodeAndValidateResultForVersion keeps result-schema behavior pinned to the
// immutable orchestrator version stored on the run. Add a new case instead of
// changing an existing version's result contract in place.
func DecodeAndValidateResultForVersion(version string, raw json.RawMessage, task Task, config RunConfig) (ResultEnvelope, string, error) {
	switch version {
	case OrchestratorVersionV1:
		return DecodeAndValidateResult(raw, task, config)
	case OrchestratorVersionV2:
		return decodeAndValidateResult(raw, task, config, (*ResultEnvelope).validateV2)
	case OrchestratorVersionV3:
		return decodeAndValidateResult(raw, task, config, (*ResultEnvelope).validateV3)
	default:
		return ResultEnvelope{}, "", fmt.Errorf("%w: %q", ErrUnsupportedVersion, version)
	}
}

func ensureSupportedOrchestratorVersion(version string) error {
	switch version {
	case OrchestratorVersionV1, OrchestratorVersionV2, OrchestratorVersionV3:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedVersion, version)
	}
}

func usesStructuredResultContract(version string) bool {
	return version == OrchestratorVersionV2 || version == OrchestratorVersionV3
}

type PlanProposal struct {
	Questions         []QuestionProposal `json:"questions"`
	Tasks             []TaskProposal     `json:"tasks"`
	Method            *MethodProposal    `json:"method,omitempty"`
	InclusionCriteria []string           `json:"inclusion_criteria"`
	ExclusionCriteria []string           `json:"exclusion_criteria"`
	SourceStrategy    []string           `json:"source_strategy"`
	Uncertainties     []string           `json:"uncertainties"`
	PlanningRisks     []string           `json:"planning_risks"`
}

// MethodProposal contains only Agent-authored method choices. Goal/plan
// versions and attribution are assigned by the ResearchRun Module when the
// plan is accepted.
type MethodProposal struct {
	DecisionQuestion        string   `json:"decision_question"`
	MethodRationale         string   `json:"method_rationale"`
	AnalysisMethods         []string `json:"analysis_methods"`
	EvidenceRequirements    []string `json:"evidence_requirements"`
	CounterevidenceStrategy []string `json:"counterevidence_strategy"`
	StoppingConditions      []string `json:"stopping_conditions"`
}

type QuestionProposal struct {
	ClientKey       string       `json:"client_key"`
	ParentClientKey string       `json:"parent_client_key,omitempty"`
	Kind            QuestionKind `json:"kind"`
	Text            string       `json:"text"`
	Required        bool         `json:"required"`
	Priority        float64      `json:"priority"`
	Impact          float64      `json:"impact"`
	Uncertainty     float64      `json:"uncertainty"`
	Novelty         float64      `json:"novelty"`
}

type TaskProposal struct {
	ClientKey          string          `json:"client_key"`
	QuestionKey        string          `json:"question_key,omitempty"`
	Kind               TaskKind        `json:"kind"`
	Objective          string          `json:"objective"`
	RequiredCapability string          `json:"required_capability"`
	ExpectedResult     string          `json:"expected_result"`
	AcceptanceCriteria json.RawMessage `json:"acceptance_criteria,omitempty"`
	Priority           float64         `json:"priority"`
	DependsOn          []string        `json:"depends_on,omitempty"`
	MaxAttempts        int             `json:"max_attempts,omitempty"`
	TimeoutSeconds     int             `json:"timeout_seconds,omitempty"`
}

type SourceProposal struct {
	ClientKey       string          `json:"client_key"`
	URL             string          `json:"url"`
	Title           string          `json:"title"`
	Publisher       string          `json:"publisher"`
	SourceClass     string          `json:"source_class"`
	IndependenceKey string          `json:"independence_key"`
	RetrievedAt     time.Time       `json:"retrieved_at"`
	SnapshotText    string          `json:"snapshot_text"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
}

type ObservationProposal struct {
	ClientKey      string          `json:"client_key"`
	SourceKey      string          `json:"source_key"`
	Quote          string          `json:"quote,omitempty"`
	Datum          json.RawMessage `json:"datum,omitempty"`
	Locator        string          `json:"locator,omitempty"`
	Interpretation string          `json:"interpretation,omitempty"`
}

type EvidenceProposal struct {
	ObservationKey string  `json:"observation_key"`
	Relation       string  `json:"relation"`
	Strength       float64 `json:"strength"`
	Rationale      string  `json:"rationale,omitempty"`
}

type ClaimProposal struct {
	ClientKey    string             `json:"client_key"`
	Text         string             `json:"text"`
	Significance string             `json:"significance"`
	Confidence   float64            `json:"confidence"`
	Status       ClaimStatus        `json:"status,omitempty"`
	Resolution   string             `json:"resolution,omitempty"`
	Evidence     []EvidenceProposal `json:"evidence"`
}

type ReportClaimProposal struct {
	ClaimKey    string `json:"claim_key"`
	SectionID   string `json:"section_id"`
	AnchorQuote string `json:"anchor_quote,omitempty"`
}

type ReportProposal struct {
	ContentMD  string                `json:"content_md"`
	Structured json.RawMessage       `json:"structured"`
	Claims     []ReportClaimProposal `json:"claims"`
}

type EvaluationProposal struct {
	Passed                bool              `json:"passed"`
	FactualGrounding      float64           `json:"factual_grounding"`
	Coverage              float64           `json:"coverage"`
	AnalyticalDepth       float64           `json:"analytical_depth"`
	SourceQuality         float64           `json:"source_quality"`
	ContradictionHandling float64           `json:"contradiction_handling"`
	InstructionAdherence  float64           `json:"instruction_adherence"`
	Readability           float64           `json:"readability"`
	Findings              []string          `json:"findings"`
	DimensionFindings     map[string]string `json:"dimension_findings,omitempty"`
	ReviewedClaimKeys     []string          `json:"reviewed_claim_keys,omitempty"`
	ReviewedSectionIDs    []string          `json:"reviewed_section_ids,omitempty"`
	Metadata              map[string]any    `json:"metadata,omitempty"`
}

func DecodeAndValidateResult(raw []byte, task Task, cfg RunConfig) (ResultEnvelope, string, error) {
	return decodeAndValidateResult(raw, task, cfg, (*ResultEnvelope).Validate)
}

func decodeAndValidateResult(raw []byte, task Task, cfg RunConfig, validate func(*ResultEnvelope, Task, RunConfig) error) (ResultEnvelope, string, error) {
	if len(raw) == 0 || len(raw) > cfg.MaxResultBytes {
		return ResultEnvelope{}, "", fmt.Errorf("%w: result size must be between 1 and %d bytes", ErrInvalidResult, cfg.MaxResultBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var result ResultEnvelope
	if err := dec.Decode(&result); err != nil {
		return ResultEnvelope{}, "", fmt.Errorf("%w: decode: %v", ErrInvalidResult, err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return ResultEnvelope{}, "", fmt.Errorf("%w: multiple JSON values", ErrInvalidResult)
		}
		return ResultEnvelope{}, "", fmt.Errorf("%w: trailing JSON: %v", ErrInvalidResult, err)
	}
	if err := validate(&result, task, cfg); err != nil {
		return ResultEnvelope{}, "", err
	}
	canonical, err := json.Marshal(result)
	if err != nil {
		return ResultEnvelope{}, "", fmt.Errorf("%w: canonicalize: %v", ErrInvalidResult, err)
	}
	h := sha256.Sum256(canonical)
	return result, hex.EncodeToString(h[:]), nil
}

func (r *ResultEnvelope) Validate(task Task, cfg RunConfig) error {
	if r.SchemaVersion != 1 {
		return fmt.Errorf("%w: unsupported schema_version %d", ErrInvalidResult, r.SchemaVersion)
	}
	if r.AnswerClaimKey != "" {
		return fmt.Errorf("%w: answer_claim_key requires schema_version 2", ErrInvalidResult)
	}
	if r.Report != nil {
		for _, link := range r.Report.Claims {
			if link.AnchorQuote != "" {
				return fmt.Errorf("%w: report anchor_quote requires schema_version 2", ErrInvalidResult)
			}
		}
	}
	if r.Evaluation != nil && (len(r.Evaluation.DimensionFindings) > 0 || len(r.Evaluation.ReviewedClaimKeys) > 0 || len(r.Evaluation.ReviewedSectionIDs) > 0) {
		return fmt.Errorf("%w: structured review coverage requires schema_version 2", ErrInvalidResult)
	}
	if r.Plan != nil && r.Plan.Method != nil {
		return fmt.Errorf("%w: plan method requires schema_version 3", ErrInvalidResult)
	}
	if err := validateKey("client_request_id", r.ClientRequestID); err != nil {
		return err
	}
	if len(r.Summary) > maxSummaryBytes {
		return fmt.Errorf("%w: summary exceeds %d bytes", ErrInvalidResult, maxSummaryBytes)
	}
	if !unitInterval(r.CoverageDelta) || !unitInterval(r.Confidence) {
		return fmt.Errorf("%w: coverage_delta and confidence must be in [0,1]", ErrInvalidResult)
	}
	if len(r.Questions) > maxResultItems || len(r.Sources) > maxResultItems || len(r.Observations) > maxResultItems || len(r.Claims) > maxResultItems || len(r.ProposedTasks) > maxResultItems {
		return fmt.Errorf("%w: result collection exceeds %d items", ErrInvalidResult, maxResultItems)
	}
	if task.Kind == TaskKindPlan || task.Kind == TaskKindReplan {
		if r.Plan == nil {
			return fmt.Errorf("%w: %s task requires plan", ErrInvalidResult, task.Kind)
		}
	}
	if task.Kind == TaskKindSynthesize && r.Report == nil {
		return fmt.Errorf("%w: synthesize task requires report", ErrInvalidResult)
	}
	if (task.Kind == TaskKindQualityGate || task.Kind == TaskKindCitationAudit) && r.Evaluation == nil {
		return fmt.Errorf("%w: %s task requires evaluation", ErrInvalidResult, task.Kind)
	}
	questions := append([]QuestionProposal(nil), r.Questions...)
	if r.Plan != nil {
		if len(r.Plan.Questions) == 0 || len(r.Plan.Tasks) == 0 {
			return fmt.Errorf("%w: plan requires questions and tasks", ErrInvalidResult)
		}
		questions = append(questions, r.Plan.Questions...)
		if err := validateStringList("inclusion_criteria", r.Plan.InclusionCriteria); err != nil {
			return err
		}
		if err := validateStringList("exclusion_criteria", r.Plan.ExclusionCriteria); err != nil {
			return err
		}
		if err := validateStringList("source_strategy", r.Plan.SourceStrategy); err != nil {
			return err
		}
		if err := validateStringList("uncertainties", r.Plan.Uncertainties); err != nil {
			return err
		}
		if err := validateStringList("planning_risks", r.Plan.PlanningRisks); err != nil {
			return err
		}
	}
	questionKeys := map[string]struct{}{}
	for _, q := range questions {
		if err := validateQuestion(q, questionKeys); err != nil {
			return err
		}
	}
	for _, q := range questions {
		if q.ParentClientKey != "" {
			if q.ParentClientKey == q.ClientKey {
				return fmt.Errorf("%w: question %q is its own parent", ErrInvalidResult, q.ClientKey)
			}
			if err := validateKey("question.parent_client_key", q.ParentClientKey); err != nil {
				return err
			}
		}
	}
	tasks := append([]TaskProposal(nil), r.ProposedTasks...)
	if r.Plan != nil {
		tasks = append(tasks, r.Plan.Tasks...)
	}
	if len(tasks) > cfg.MaxTasks {
		return fmt.Errorf("%w: proposed tasks exceed run limit %d", ErrInvalidResult, cfg.MaxTasks)
	}
	if err := validateTaskProposals(tasks, questionKeys, cfg); err != nil {
		return err
	}
	sources := map[string]SourceProposal{}
	for _, source := range r.Sources {
		if err := validateSource(source, cfg); err != nil {
			return err
		}
		if _, dup := sources[source.ClientKey]; dup {
			return fmt.Errorf("%w: duplicate source key %q", ErrInvalidResult, source.ClientKey)
		}
		sources[source.ClientKey] = source
	}
	observations := map[string]ObservationProposal{}
	for _, obs := range r.Observations {
		if err := validateKey("observation.client_key", obs.ClientKey); err != nil {
			return err
		}
		source, ok := sources[obs.SourceKey]
		if !ok {
			return fmt.Errorf("%w: observation %q references unknown source %q", ErrInvalidResult, obs.ClientKey, obs.SourceKey)
		}
		if strings.TrimSpace(obs.Quote) == "" && isEmptyJSON(obs.Datum) {
			return fmt.Errorf("%w: observation %q requires quote or datum", ErrInvalidResult, obs.ClientKey)
		}
		if obs.Quote != "" && !strings.Contains(source.SnapshotText, obs.Quote) {
			return fmt.Errorf("%w: observation %q quote is absent from source snapshot", ErrInvalidResult, obs.ClientKey)
		}
		if _, dup := observations[obs.ClientKey]; dup {
			return fmt.Errorf("%w: duplicate observation key %q", ErrInvalidResult, obs.ClientKey)
		}
		observations[obs.ClientKey] = obs
	}
	claimKeys := map[string]struct{}{}
	for _, claim := range r.Claims {
		if err := validateClaim(claim, observations); err != nil {
			return err
		}
		if _, dup := claimKeys[claim.ClientKey]; dup {
			return fmt.Errorf("%w: duplicate claim key %q", ErrInvalidResult, claim.ClientKey)
		}
		claimKeys[claim.ClientKey] = struct{}{}
	}
	if r.Report != nil {
		if len(r.Report.ContentMD) == 0 || len(r.Report.ContentMD) > maxReportBytes {
			return fmt.Errorf("%w: report content must be between 1 and %d bytes", ErrInvalidResult, maxReportBytes)
		}
		if !json.Valid(r.Report.Structured) && len(r.Report.Structured) > 0 {
			return fmt.Errorf("%w: report.structured must be JSON", ErrInvalidResult)
		}
		if len(r.Report.Claims) == 0 {
			return fmt.Errorf("%w: report requires normalized claim links", ErrInvalidResult)
		}
		for _, link := range r.Report.Claims {
			if err := validateKey("report.claim_key", link.ClaimKey); err != nil {
				return err
			}
			if len(link.SectionID) > maxClientKeyBytes {
				return fmt.Errorf("%w: report section_id exceeds %d bytes", ErrInvalidResult, maxClientKeyBytes)
			}
		}
	}
	if r.Evaluation != nil {
		for name, score := range map[string]float64{
			"factual_grounding":      r.Evaluation.FactualGrounding,
			"coverage":               r.Evaluation.Coverage,
			"analytical_depth":       r.Evaluation.AnalyticalDepth,
			"source_quality":         r.Evaluation.SourceQuality,
			"contradiction_handling": r.Evaluation.ContradictionHandling,
			"instruction_adherence":  r.Evaluation.InstructionAdherence,
			"readability":            r.Evaluation.Readability,
		} {
			if !unitInterval(score) {
				return fmt.Errorf("%w: evaluation.%s must be in [0,1]", ErrInvalidResult, name)
			}
		}
		encoded, _ := json.Marshal(r.Evaluation.Metadata)
		if len(encoded) > maxMetadataBytes {
			return fmt.Errorf("%w: evaluation metadata exceeds %d bytes", ErrInvalidResult, maxMetadataBytes)
		}
	}
	return nil
}

func validateQuestion(q QuestionProposal, seen map[string]struct{}) error {
	if err := validateKey("question.client_key", q.ClientKey); err != nil {
		return err
	}
	if _, dup := seen[q.ClientKey]; dup {
		return fmt.Errorf("%w: duplicate question key %q", ErrInvalidResult, q.ClientKey)
	}
	seen[q.ClientKey] = struct{}{}
	switch q.Kind {
	case QuestionKindDimension, QuestionKindHypothesis, QuestionKindContradiction, QuestionKindGap, QuestionKindFollowUp:
	default:
		return fmt.Errorf("%w: invalid question kind %q", ErrInvalidResult, q.Kind)
	}
	if strings.TrimSpace(q.Text) == "" || len(q.Text) > maxTaskObjectiveBytes {
		return fmt.Errorf("%w: question %q has invalid text", ErrInvalidResult, q.ClientKey)
	}
	if !unitInterval(q.Priority) || !unitInterval(q.Impact) || !unitInterval(q.Uncertainty) || !unitInterval(q.Novelty) {
		return fmt.Errorf("%w: question %q scores must be in [0,1]", ErrInvalidResult, q.ClientKey)
	}
	return nil
}

func validateTaskProposals(tasks []TaskProposal, _ map[string]struct{}, cfg RunConfig) error {
	seen := map[string]TaskProposal{}
	for _, task := range tasks {
		if err := validateKey("task.client_key", task.ClientKey); err != nil {
			return err
		}
		if _, dup := seen[task.ClientKey]; dup {
			return fmt.Errorf("%w: duplicate task key %q", ErrInvalidResult, task.ClientKey)
		}
		if !validTaskKind(task.Kind) || task.Kind == TaskKindPlan {
			return fmt.Errorf("%w: invalid proposed task kind %q", ErrInvalidResult, task.Kind)
		}
		if strings.TrimSpace(task.Objective) == "" || len(task.Objective) > maxTaskObjectiveBytes {
			return fmt.Errorf("%w: task %q objective is invalid", ErrInvalidResult, task.ClientKey)
		}
		if strings.TrimSpace(task.RequiredCapability) == "" || strings.TrimSpace(task.ExpectedResult) == "" {
			return fmt.Errorf("%w: task %q requires capability and expected_result", ErrInvalidResult, task.ClientKey)
		}
		if !validCapability(task.RequiredCapability) {
			return fmt.Errorf("%w: task %q has unsupported capability %q", ErrInvalidResult, task.ClientKey, task.RequiredCapability)
		}
		if !unitInterval(task.Priority) {
			return fmt.Errorf("%w: task %q priority must be in [0,1]", ErrInvalidResult, task.ClientKey)
		}
		if task.QuestionKey != "" {
			if err := validateKey("task.question_key", task.QuestionKey); err != nil {
				return err
			}
		}
		if task.MaxAttempts < 0 || task.MaxAttempts > cfg.MaxAttemptsPerTask {
			return fmt.Errorf("%w: task %q max_attempts exceeds run config", ErrInvalidResult, task.ClientKey)
		}
		if task.TimeoutSeconds < 0 || task.TimeoutSeconds > 86400 {
			return fmt.Errorf("%w: task %q timeout_seconds is invalid", ErrInvalidResult, task.ClientKey)
		}
		seen[task.ClientKey] = task
	}
	for _, task := range tasks {
		for _, dep := range task.DependsOn {
			if dep == task.ClientKey {
				return fmt.Errorf("%w: task %q depends on itself", ErrInvalidResult, task.ClientKey)
			}
			if err := validateKey("task.depends_on", dep); err != nil {
				return err
			}
		}
	}
	if hasTaskCycle(seen) {
		return fmt.Errorf("%w: task dependency graph contains a cycle", ErrInvalidResult)
	}
	return nil
}

func hasTaskCycle(tasks map[string]TaskProposal) bool {
	state := map[string]uint8{}
	var visit func(string) bool
	visit = func(key string) bool {
		if state[key] == 1 {
			return true
		}
		if state[key] == 2 {
			return false
		}
		state[key] = 1
		deps := append([]string(nil), tasks[key].DependsOn...)
		sort.Strings(deps)
		for _, dep := range deps {
			if _, ok := tasks[dep]; ok && visit(dep) {
				return true
			}
		}
		state[key] = 2
		return false
	}
	for key := range tasks {
		if visit(key) {
			return true
		}
	}
	return false
}

func validateSource(source SourceProposal, cfg RunConfig) error {
	if err := validateKey("source.client_key", source.ClientKey); err != nil {
		return err
	}
	canonical, err := CanonicalURL(source.URL)
	if err != nil {
		return fmt.Errorf("%w: source %q: %v", ErrInvalidResult, source.ClientKey, err)
	}
	source.URL = canonical
	if source.RetrievedAt.IsZero() {
		return fmt.Errorf("%w: source %q requires retrieved_at", ErrInvalidResult, source.ClientKey)
	}
	if source.RetrievedAt.After(time.Now().UTC().Add(10 * time.Minute)) {
		return fmt.Errorf("%w: source %q retrieved_at is in the future", ErrInvalidResult, source.ClientKey)
	}
	if len(source.SnapshotText) == 0 || len(source.SnapshotText) > cfg.MaxSnapshotBytes {
		return fmt.Errorf("%w: source %q snapshot must be between 1 and %d bytes", ErrInvalidResult, source.ClientKey, cfg.MaxSnapshotBytes)
	}
	if strings.TrimSpace(source.IndependenceKey) == "" || len(source.IndependenceKey) > maxClientKeyBytes {
		return fmt.Errorf("%w: source %q independence_key is invalid", ErrInvalidResult, source.ClientKey)
	}
	if len(source.Metadata) > maxMetadataBytes || (len(source.Metadata) > 0 && !json.Valid(source.Metadata)) {
		return fmt.Errorf("%w: source %q metadata is invalid", ErrInvalidResult, source.ClientKey)
	}
	return nil
}

func validateClaim(claim ClaimProposal, observations map[string]ObservationProposal) error {
	if err := validateKey("claim.client_key", claim.ClientKey); err != nil {
		return err
	}
	if strings.TrimSpace(claim.Text) == "" || len(claim.Text) > maxClaimTextBytes {
		return fmt.Errorf("%w: claim %q text is invalid", ErrInvalidResult, claim.ClientKey)
	}
	switch claim.Significance {
	case "low", "medium", "high", "critical":
	default:
		return fmt.Errorf("%w: claim %q significance is invalid", ErrInvalidResult, claim.ClientKey)
	}
	switch claim.Status {
	case "", ClaimStatusProposed, ClaimStatusSupported, ClaimStatusDisputed, ClaimStatusRefuted, ClaimStatusSuperseded, ClaimStatusUnresolved:
	default:
		return fmt.Errorf("%w: claim %q status is invalid", ErrInvalidResult, claim.ClientKey)
	}
	if !unitInterval(claim.Confidence) {
		return fmt.Errorf("%w: claim %q confidence must be in [0,1]", ErrInvalidResult, claim.ClientKey)
	}
	if len(claim.Evidence) == 0 {
		return fmt.Errorf("%w: claim %q requires evidence", ErrInvalidResult, claim.ClientKey)
	}
	for _, evidence := range claim.Evidence {
		if _, ok := observations[evidence.ObservationKey]; !ok {
			return fmt.Errorf("%w: claim %q references unknown observation %q", ErrInvalidResult, claim.ClientKey, evidence.ObservationKey)
		}
		if evidence.Relation != "supports" && evidence.Relation != "contradicts" {
			return fmt.Errorf("%w: claim %q has invalid evidence relation", ErrInvalidResult, claim.ClientKey)
		}
		if !unitInterval(evidence.Strength) {
			return fmt.Errorf("%w: claim %q evidence strength must be in [0,1]", ErrInvalidResult, claim.ClientKey)
		}
	}
	return nil
}

func CanonicalURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("URL scheme must be http or https")
	}
	if u.Hostname() == "" {
		return "", errors.New("URL host is required")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host = net.JoinHostPort(strings.Trim(host, "[]"), port)
	}
	u.Host = host
	u.Fragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}

func validTaskKind(kind TaskKind) bool {
	switch kind {
	case TaskKindPlan, TaskKindDiscover, TaskKindDeepRead, TaskKindVerify, TaskKindCounterSearch, TaskKindReplan, TaskKindSynthesize, TaskKindQualityGate, TaskKindCitationAudit:
		return true
	default:
		return false
	}
}

var capabilityPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func validCapability(capability string) bool {
	return capabilityPattern.MatchString(strings.ToLower(strings.TrimSpace(capability)))
}

func validateKey(name, value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxClientKeyBytes {
		return fmt.Errorf("%w: %s must be between 1 and %d bytes", ErrInvalidResult, name, maxClientKeyBytes)
	}
	return nil
}

func validateStringList(name string, values []string) error {
	if len(values) > maxResultItems {
		return fmt.Errorf("%w: %s exceeds %d items", ErrInvalidResult, name, maxResultItems)
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" || len(value) > maxTaskObjectiveBytes {
			return fmt.Errorf("%w: %s contains invalid value", ErrInvalidResult, name)
		}
	}
	return nil
}

func unitInterval(v float64) bool { return v >= 0 && v <= 1 }

func isEmptyJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("{}"))
}
