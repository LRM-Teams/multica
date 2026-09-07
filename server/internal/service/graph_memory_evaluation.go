// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/util"
)

// Graph Memory evaluation protocol (PAST-style vertical slices, test-only).
//
// This plane is intentionally independent from skill_evaluation_run /
// research evaluation objects: an episode needs mutable lifecycle state plus
// an immutable arm/session binding and an append-only usage ledger. All APIs
// are owner/admin-gated at the handler AND fail closed behind a process-level
// configuration gate plus an explicit workspace allowlist, so the plane stays
// dark on production-shaped deployments even if a route is reached.
//
// Arm semantics are enforced by server-side entry points, never by prompt
// wording:
//
//	graph_on         managed Memory Agent + Graph MCP + recall enabled;
//	                 durable graph writes flow normally.
//	persistence_off  recall disabled, gateway five operations refused,
//	                 graph capture anchors skipped, legacy execution-memory
//	                 injection suppressed, durable writes off.
//
// ActiveEpisodeArm is the single lookup those entry points share; the partial
// unique index uq_graph_memory_evaluation_episode_live_channel guarantees the
// answer is unambiguous while any episode for the channel is live.

var (
	ErrGraphMemoryEvaluationDisabled   = errors.New("graph memory evaluation protocol is disabled")
	ErrGraphMemoryEvaluationNotFound   = errors.New("graph memory evaluation object not found")
	ErrGraphMemoryEvaluationState      = errors.New("graph memory evaluation state transition invalid")
	ErrGraphMemoryEvaluationClosure    = errors.New("graph memory evaluation closure conditions not met")
	ErrGraphMemoryEvaluationEvidence   = errors.New("graph memory evaluation evidence incomplete")
	ErrGraphMemoryEvaluationPolicyLock = errors.New("graph memory evaluation channel already has a live episode")
)

const (
	GraphMemoryEvaluationArmGraphOn        = "graph_on"
	GraphMemoryEvaluationArmPersistenceOff = "persistence_off"

	// The seven strict fresh-session closure conditions (Handoff 7 §6).
	// Every required condition must report state=complete before an episode
	// may settle; anything else stays running or fails.
	GraphMemoryClosureSessionGenerationReset = "session_generation_reset"
	GraphMemoryClosurePrimaryReplyCommitted  = "primary_reply_committed"
	GraphMemoryClosureDaemonProjection       = "daemon_projection_complete"
	GraphMemoryClosureNoActiveClaim          = "graph_run_no_active_claim"
	GraphMemoryClosureCheckpointSettled      = "checkpoint_settled_or_skipped"
	GraphMemoryClosureJobsSettled            = "jobs_settled_or_skipped"
	GraphMemoryClosureStateTiedToGeneration  = "state_tied_to_generation"
)

var graphMemoryEvaluationClosureConditions = []string{
	GraphMemoryClosureSessionGenerationReset,
	GraphMemoryClosurePrimaryReplyCommitted,
	GraphMemoryClosureDaemonProjection,
	GraphMemoryClosureNoActiveClaim,
	GraphMemoryClosureCheckpointSettled,
	GraphMemoryClosureJobsSettled,
	GraphMemoryClosureStateTiedToGeneration,
}

// GraphMemoryEvaluationService owns the protocol plane's durable state.
type GraphMemoryEvaluationService struct {
	pool *pgxpool.Pool
}

func NewGraphMemoryEvaluationService(pool *pgxpool.Pool) *GraphMemoryEvaluationService {
	return &GraphMemoryEvaluationService{pool: pool}
}

// GraphMemoryEvaluationPlaneEnabled reports whether the process-level gate is
// on at all (workspace allowlist not considered). Main uses it to decide
// whether to wire arm enforcement into recall/gateway; unwired means the
// plane cannot alter runtime behavior anywhere.
func GraphMemoryEvaluationPlaneEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MULTICA_GRAPH_MEMORY_EVALUATION_ENABLED"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// GraphMemoryEvaluationGated reports whether the protocol plane may operate
// for this workspace at all. Fail closed: the process gate must be explicitly
// enabled AND the workspace must appear in the explicit allowlist.
func GraphMemoryEvaluationGated(workspaceID string) bool {
	if !GraphMemoryEvaluationPlaneEnabled() {
		return false
	}
	wsUUID, err := util.ParseUUID(strings.TrimSpace(workspaceID))
	if err != nil {
		return false
	}
	normalized := util.UUIDToString(wsUUID)
	for _, candidate := range strings.Split(os.Getenv("MULTICA_GRAPH_MEMORY_EVALUATION_WORKSPACES"), ",") {
		if strings.EqualFold(strings.TrimSpace(candidate), normalized) {
			return true
		}
	}
	return false
}

// ActiveEpisodeArm returns the arm of the single live episode for a channel,
// if any. This is the enforcement lookup shared by recall, the gateway, and
// capture; it reads only durable state and never widens access on error.
func (s *GraphMemoryEvaluationService) ActiveEpisodeArm(ctx context.Context, workspaceID, channelID string) (string, bool, error) {
	if s == nil || s.pool == nil {
		return "", false, nil
	}
	wsUUID, err := util.ParseUUID(strings.TrimSpace(workspaceID))
	if err != nil {
		return "", false, nil
	}
	channelUUID, err := util.ParseUUID(strings.TrimSpace(channelID))
	if err != nil {
		return "", false, nil
	}
	var arm string
	err = s.pool.QueryRow(ctx, `
		SELECT arm FROM graph_memory_evaluation_episode
		WHERE workspace_id=$1::uuid AND channel_id=$2::uuid
		  AND status IN ('pending','running')
		LIMIT 1`, wsUUID, channelUUID).Scan(&arm)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return arm, true, nil
}

// EvaluationPersistenceOff is the narrow helper the enforcement seams call:
// true only when a live episode on this channel runs the persistence_off arm.
// Errors read as "not off"; the seams themselves fail closed on error before
// this helper's result matters, so persistence is never widened.
func (s *GraphMemoryEvaluationService) EvaluationPersistenceOff(ctx context.Context, workspaceID, channelID string) bool {
	arm, live, err := s.ActiveEpisodeArm(ctx, workspaceID, channelID)
	return live && err == nil && arm == GraphMemoryEvaluationArmPersistenceOff
}

// EvaluationPersistenceOffTx is the transactional variant for capture paths
// that already hold the message transaction.
func EvaluationPersistenceOffTx(ctx context.Context, tx pgx.Tx, workspaceID, channelID pgtype.UUID) bool {
	if tx == nil || !workspaceID.Valid || !channelID.Valid {
		return false
	}
	var arm string
	err := tx.QueryRow(ctx, `
		SELECT arm FROM graph_memory_evaluation_episode
		WHERE workspace_id=$1 AND channel_id=$2
		  AND status IN ('pending','running')
		LIMIT 1`, workspaceID, channelID).Scan(&arm)
	return err == nil && arm == GraphMemoryEvaluationArmPersistenceOff
}

type GraphMemoryEvaluationRunInput struct {
	WorkspaceID    string
	RunID          string
	Label          string
	CreatedByActor string
}

type GraphMemoryEvaluationEpisodeInput struct {
	WorkspaceID       string
	RunID             string
	EpisodeID         string
	ChannelID         string
	PrimaryAgentID    string
	Arm               string
	SessionGeneration string
}

// CreateRun starts a protocol pass. Run ids are caller-supplied so the
// harness can pin them to its own artifacts; collisions are state errors.
func (s *GraphMemoryEvaluationService) CreateRun(ctx context.Context, input GraphMemoryEvaluationRunInput) error {
	if s == nil || s.pool == nil {
		return ErrGraphMemoryEvaluationDisabled
	}
	if !GraphMemoryEvaluationGated(input.WorkspaceID) {
		return ErrGraphMemoryEvaluationDisabled
	}
	if strings.TrimSpace(input.RunID) == "" || strings.TrimSpace(input.CreatedByActor) == "" {
		return ErrGraphMemoryEvaluationState
	}
	wsUUID, err := util.ParseUUID(strings.TrimSpace(input.WorkspaceID))
	if err != nil {
		return ErrGraphMemoryEvaluationState
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO graph_memory_evaluation_run (workspace_id, run_id, label, status, created_by_actor)
		VALUES ($1::uuid, $2, $3, 'running', $4)
		ON CONFLICT (workspace_id, run_id) DO NOTHING`,
		wsUUID, strings.TrimSpace(input.RunID), strings.TrimSpace(input.Label), strings.TrimSpace(input.CreatedByActor))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: run_id already exists", ErrGraphMemoryEvaluationState)
	}
	return nil
}

// CreateEpisode binds one episode to a channel under an arm. The database's
// live-channel partial unique index refuses a second live episode; the arm
// must be one of the two protocol values and is immutable from here on.
func (s *GraphMemoryEvaluationService) CreateEpisode(ctx context.Context, input GraphMemoryEvaluationEpisodeInput) error {
	if s == nil || s.pool == nil {
		return ErrGraphMemoryEvaluationDisabled
	}
	if !GraphMemoryEvaluationGated(input.WorkspaceID) {
		return ErrGraphMemoryEvaluationDisabled
	}
	arm := strings.TrimSpace(input.Arm)
	if arm != GraphMemoryEvaluationArmGraphOn && arm != GraphMemoryEvaluationArmPersistenceOff {
		return fmt.Errorf("%w: unknown arm %q", ErrGraphMemoryEvaluationState, arm)
	}
	if strings.TrimSpace(input.SessionGeneration) == "" {
		return fmt.Errorf("%w: session_generation is required", ErrGraphMemoryEvaluationState)
	}
	wsUUID, err := util.ParseUUID(strings.TrimSpace(input.WorkspaceID))
	if err != nil {
		return ErrGraphMemoryEvaluationState
	}
	channelUUID, err := util.ParseUUID(strings.TrimSpace(input.ChannelID))
	if err != nil {
		return ErrGraphMemoryEvaluationState
	}
	agentUUID, err := util.ParseUUID(strings.TrimSpace(input.PrimaryAgentID))
	if err != nil {
		return ErrGraphMemoryEvaluationState
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO graph_memory_evaluation_episode
		  (workspace_id, run_id, episode_id, channel_id, primary_agent_id,
		   arm, memory_policy, session_generation, status, closure_checklist)
		SELECT $1::uuid, $2, $3, $4::uuid, $5::uuid, $6, $6, $7, 'pending', '{}'::jsonb
		WHERE EXISTS (SELECT 1 FROM graph_memory_evaluation_run
		              WHERE workspace_id=$1::uuid AND run_id=$2 AND status='running')`,
		wsUUID, strings.TrimSpace(input.RunID), strings.TrimSpace(input.EpisodeID),
		channelUUID, agentUUID, arm, strings.TrimSpace(input.SessionGeneration))
	if err != nil {
		if strings.Contains(err.Error(), "uq_graph_memory_evaluation_episode_live_channel") {
			return ErrGraphMemoryEvaluationPolicyLock
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrGraphMemoryEvaluationNotFound
	}
	return nil
}

// StartEpisode moves pending → running and records the input message that
// opens the episode turn.
func (s *GraphMemoryEvaluationService) StartEpisode(ctx context.Context, workspaceID, runID, episodeID, inputMessageID string) error {
	if s == nil || s.pool == nil {
		return ErrGraphMemoryEvaluationDisabled
	}
	msgUUID, err := util.ParseUUID(strings.TrimSpace(inputMessageID))
	if err != nil {
		return fmt.Errorf("%w: input_message_id invalid", ErrGraphMemoryEvaluationState)
	}
	wsUUID, err := util.ParseUUID(strings.TrimSpace(workspaceID))
	if err != nil {
		return ErrGraphMemoryEvaluationState
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE graph_memory_evaluation_episode
		SET status='running', input_message_id=$4::uuid, started_at=now()
		WHERE workspace_id=$1::uuid AND run_id=$2 AND episode_id=$3
		  AND status='pending'`,
		wsUUID, strings.TrimSpace(runID), strings.TrimSpace(episodeID), msgUUID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrGraphMemoryEvaluationNotFound
	}
	return nil
}

// SettleEpisode applies strict fresh-session closure: every required
// condition must be reported complete, the output message must be bound, and
// the channel's graph memory agent must hold no active run claim (the one
// condition the server verifies itself rather than trusting the caller).
func (s *GraphMemoryEvaluationService) SettleEpisode(ctx context.Context, workspaceID, runID, episodeID, outputMessageID string, checklist map[string]GraphMemoryClosureState) error {
	if s == nil || s.pool == nil {
		return ErrGraphMemoryEvaluationDisabled
	}
	if !GraphMemoryEvaluationGated(workspaceID) {
		return ErrGraphMemoryEvaluationDisabled
	}
	if err := validateGraphMemoryClosureChecklist(checklist); err != nil {
		return err
	}
	outUUID, err := util.ParseUUID(strings.TrimSpace(outputMessageID))
	if err != nil {
		return fmt.Errorf("%w: output_message_id invalid", ErrGraphMemoryEvaluationState)
	}
	wsUUID, err := util.ParseUUID(strings.TrimSpace(workspaceID))
	if err != nil {
		return ErrGraphMemoryEvaluationState
	}
	encoded, err := encodeGraphMemoryClosureChecklist(checklist)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Server-verified condition: the episode channel's graph memory agent
	// holds no active run claim (running-status run claimed on its state).
	// graph_memory_agent_state is keyed by channel only; workspace scoping
	// comes through the managed-channel row.
	var claimActive bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS(
		  SELECT 1 FROM graph_memory_agent_state state
		  JOIN graph_memory_channel_agent managed ON managed.channel_id=state.channel_id
		  JOIN graph_memory_agent_run run ON run.id=state.active_run_id AND run.status='running'
		  WHERE managed.workspace_id=$1::uuid
		    AND managed.channel_id IN (
		      SELECT ep.channel_id FROM graph_memory_evaluation_episode ep
		      WHERE ep.workspace_id=$1::uuid AND ep.run_id=$2 AND ep.episode_id=$3)
		    AND state.active_run_id IS NOT NULL)`,
		wsUUID, strings.TrimSpace(runID), strings.TrimSpace(episodeID)).Scan(&claimActive)
	if err != nil {
		return err
	}
	if claimActive {
		return fmt.Errorf("%w: graph memory run claim still active", ErrGraphMemoryEvaluationClosure)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE graph_memory_evaluation_episode
		SET status='settled', output_message_id=$4::uuid, closure_checklist=$5::jsonb, settled_at=now()
		WHERE workspace_id=$1::uuid AND run_id=$2 AND episode_id=$3
		  AND status='running'`,
		wsUUID, strings.TrimSpace(runID), strings.TrimSpace(episodeID), outUUID, encoded)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrGraphMemoryEvaluationNotFound
	}
	return tx.Commit(ctx)
}

// FailEpisode records a terminal failure with a reason; the arm binding and
// usage ledger survive for post-mortem.
func (s *GraphMemoryEvaluationService) FailEpisode(ctx context.Context, workspaceID, runID, episodeID, reason string) error {
	if s == nil || s.pool == nil {
		return ErrGraphMemoryEvaluationDisabled
	}
	wsUUID, err := util.ParseUUID(strings.TrimSpace(workspaceID))
	if err != nil {
		return ErrGraphMemoryEvaluationState
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE graph_memory_evaluation_episode
		SET status='failed', failure_reason=$4, settled_at=now()
		WHERE workspace_id=$1::uuid AND run_id=$2 AND episode_id=$3
		  AND status IN ('pending','running')`,
		wsUUID, strings.TrimSpace(runID), strings.TrimSpace(episodeID), strings.TrimSpace(reason))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrGraphMemoryEvaluationNotFound
	}
	return nil
}

// CompleteRun closes a run; episodes decide their own terminal states first.
func (s *GraphMemoryEvaluationService) CompleteRun(ctx context.Context, workspaceID, runID, finalStatus string) error {
	if s == nil || s.pool == nil {
		return ErrGraphMemoryEvaluationDisabled
	}
	switch finalStatus {
	case "completed", "failed", "aborted":
	default:
		return fmt.Errorf("%w: unknown run status %q", ErrGraphMemoryEvaluationState, finalStatus)
	}
	wsUUID, err := util.ParseUUID(strings.TrimSpace(workspaceID))
	if err != nil {
		return ErrGraphMemoryEvaluationState
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE graph_memory_evaluation_run
		SET status=$3, completed_at=now()
		WHERE workspace_id=$1::uuid AND run_id=$2 AND status='running'`,
		wsUUID, strings.TrimSpace(runID), finalStatus)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrGraphMemoryEvaluationNotFound
	}
	return nil
}

// GraphMemoryUsageEventInput appends one usage/evidence ledger row. The kind
// vocabulary is database-enforced; unknown kinds fail closed. The episode's
// immutable session_generation is read from the row itself so ledger rows can
// never disagree with the binding they claim to evidence.
type GraphMemoryUsageEventInput struct {
	WorkspaceID string
	RunID       string
	EpisodeID   string
	Kind        string
	Payload     map[string]any
}

func (s *GraphMemoryEvaluationService) RecordUsage(ctx context.Context, input GraphMemoryUsageEventInput) error {
	if s == nil || s.pool == nil {
		return ErrGraphMemoryEvaluationDisabled
	}
	wsUUID, err := util.ParseUUID(strings.TrimSpace(input.WorkspaceID))
	if err != nil {
		return ErrGraphMemoryEvaluationState
	}
	var payload json.RawMessage = json.RawMessage(`{}`)
	if input.Payload != nil {
		payload, err = json.Marshal(input.Payload)
		if err != nil {
			return err
		}
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO graph_memory_evaluation_usage_event
		  (workspace_id, run_id, episode_id, session_generation, kind, payload)
		SELECT ep.workspace_id, ep.run_id, ep.episode_id, ep.session_generation, $4, $5::jsonb
		FROM graph_memory_evaluation_episode ep
		WHERE ep.workspace_id=$1::uuid AND ep.run_id=$2 AND ep.episode_id=$3`,
		wsUUID, strings.TrimSpace(input.RunID), strings.TrimSpace(input.EpisodeID),
		strings.TrimSpace(input.Kind), payload)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrGraphMemoryEvaluationNotFound
	}
	return nil
}

// RecordPolicyDenial is a convenience wrapper the enforcement seams use so a
// refused recall/gateway/capture under persistence_off leaves ledger evidence
// instead of only a log line. Best-effort by design: the refusal itself has
// already happened; a ledger failure must not change it.
func (s *GraphMemoryEvaluationService) RecordPolicyDenial(ctx context.Context, workspaceID, channelID, seam string) {
	if s == nil || s.pool == nil {
		return
	}
	wsUUID, err := util.ParseUUID(strings.TrimSpace(workspaceID))
	if err != nil {
		return
	}
	channelUUID, err := util.ParseUUID(strings.TrimSpace(channelID))
	if err != nil {
		return
	}
	_, _ = s.pool.Exec(ctx, `
		INSERT INTO graph_memory_evaluation_usage_event
		  (workspace_id, run_id, episode_id, session_generation, kind, payload)
		SELECT ep.workspace_id, ep.run_id, ep.episode_id, ep.session_generation,
		       'policy_denial', jsonb_build_object('seam', $3::text, 'at', now())
		FROM graph_memory_evaluation_episode ep
		WHERE ep.workspace_id=$1::uuid AND ep.channel_id=$2::uuid
		  AND ep.status IN ('pending','running')
		LIMIT 1`, wsUUID, channelUUID, strings.TrimSpace(seam))
}

// MarkOfficialScore moves the scoring state. 'unsupported' is a one-way
// absorbing state for incomplete evidence; 'scored' requires a non-null
// score payload and a settled episode (database-enforced as well).
func (s *GraphMemoryEvaluationService) MarkOfficialScore(ctx context.Context, workspaceID, runID, episodeID, state string, score map[string]any, evidenceHash string) error {
	if s == nil || s.pool == nil {
		return ErrGraphMemoryEvaluationDisabled
	}
	switch state {
	case "unsupported":
		if score != nil {
			return fmt.Errorf("%w: unsupported cannot carry a score", ErrGraphMemoryEvaluationEvidence)
		}
	case "scored":
		if score == nil || strings.TrimSpace(evidenceHash) == "" {
			return fmt.Errorf("%w: scored requires score payload and evidence hash", ErrGraphMemoryEvaluationEvidence)
		}
	case "ready":
		if strings.TrimSpace(evidenceHash) == "" {
			return fmt.Errorf("%w: ready requires evidence hash", ErrGraphMemoryEvaluationEvidence)
		}
	default:
		return fmt.Errorf("%w: unknown score state %q", ErrGraphMemoryEvaluationState, state)
	}
	wsUUID, err := util.ParseUUID(strings.TrimSpace(workspaceID))
	if err != nil {
		return ErrGraphMemoryEvaluationState
	}
	var scoreValue json.RawMessage
	if score != nil {
		scoreValue, err = json.Marshal(score)
		if err != nil {
			return err
		}
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE graph_memory_evaluation_episode
		SET official_score_state=$4,
		    official_score=CASE WHEN $5::jsonb IS NULL THEN official_score ELSE $5::jsonb END
		WHERE workspace_id=$1::uuid AND run_id=$2 AND episode_id=$3
		  AND status='settled'
		  AND official_score_state <> 'unsupported'`,
		wsUUID, strings.TrimSpace(runID), strings.TrimSpace(episodeID), state, scoreValue)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrGraphMemoryEvaluationEvidence
	}
	if score != nil {
		_ = s.RecordUsage(ctx, GraphMemoryUsageEventInput{
			WorkspaceID: workspaceID, RunID: runID, EpisodeID: episodeID,
			Kind: "artifact_snapshot",
			Payload: map[string]any{
				"official_score_state": state,
				"evidence_hash":        strings.TrimSpace(evidenceHash),
				"at":                   time.Now().UTC().Format(time.RFC3339Nano),
			},
		})
	}
	return nil
}

// GraphMemoryClosureState is one condition's reported state. Unknown is a
// real state, not a placeholder: it can never be coerced to complete.
type GraphMemoryClosureState struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

type GraphMemoryEvaluationRunView struct {
	WorkspaceID    string     `json:"workspace_id"`
	RunID          string     `json:"run_id"`
	Label          string     `json:"label"`
	Status         string     `json:"status"`
	CreatedByActor string     `json:"created_by_actor"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

type GraphMemoryEvaluationEpisodeView struct {
	RunID              string          `json:"run_id"`
	EpisodeID          string          `json:"episode_id"`
	ChannelID          string          `json:"channel_id"`
	PrimaryAgentID     string          `json:"primary_agent_id"`
	Arm                string          `json:"arm"`
	MemoryPolicy       string          `json:"memory_policy"`
	SessionGeneration  string          `json:"session_generation"`
	Status             string          `json:"status"`
	InputMessageID     string          `json:"input_message_id,omitempty"`
	OutputMessageID    string          `json:"output_message_id,omitempty"`
	ClosureChecklist   json.RawMessage `json:"closure_checklist,omitempty"`
	OfficialScoreState string          `json:"official_score_state"`
	OfficialScore      json.RawMessage `json:"official_score,omitempty"`
	FailureReason      string          `json:"failure_reason,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	StartedAt          *time.Time      `json:"started_at,omitempty"`
	SettledAt          *time.Time      `json:"settled_at,omitempty"`
}

// ListRuns returns the workspace's protocol passes, newest first.
func (s *GraphMemoryEvaluationService) ListRuns(ctx context.Context, workspaceID string, limit int) ([]GraphMemoryEvaluationRunView, error) {
	if s == nil || s.pool == nil {
		return nil, ErrGraphMemoryEvaluationDisabled
	}
	if !GraphMemoryEvaluationGated(workspaceID) {
		return nil, ErrGraphMemoryEvaluationDisabled
	}
	wsUUID, err := util.ParseUUID(strings.TrimSpace(workspaceID))
	if err != nil {
		return nil, ErrGraphMemoryEvaluationState
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT workspace_id::text, run_id, label, status, created_by_actor, created_at, completed_at
		FROM graph_memory_evaluation_run
		WHERE workspace_id=$1::uuid
		ORDER BY created_at DESC LIMIT $2`, wsUUID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GraphMemoryEvaluationRunView
	for rows.Next() {
		var view GraphMemoryEvaluationRunView
		if err := rows.Scan(&view.WorkspaceID, &view.RunID, &view.Label, &view.Status,
			&view.CreatedByActor, &view.CreatedAt, &view.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	return out, rows.Err()
}

// GetRun returns one run with its episodes.
func (s *GraphMemoryEvaluationService) GetRun(ctx context.Context, workspaceID, runID string) (GraphMemoryEvaluationRunView, []GraphMemoryEvaluationEpisodeView, error) {
	if s == nil || s.pool == nil {
		return GraphMemoryEvaluationRunView{}, nil, ErrGraphMemoryEvaluationDisabled
	}
	if !GraphMemoryEvaluationGated(workspaceID) {
		return GraphMemoryEvaluationRunView{}, nil, ErrGraphMemoryEvaluationDisabled
	}
	wsUUID, err := util.ParseUUID(strings.TrimSpace(workspaceID))
	if err != nil {
		return GraphMemoryEvaluationRunView{}, nil, ErrGraphMemoryEvaluationState
	}
	var run GraphMemoryEvaluationRunView
	err = s.pool.QueryRow(ctx, `
		SELECT workspace_id::text, run_id, label, status, created_by_actor, created_at, completed_at
		FROM graph_memory_evaluation_run
		WHERE workspace_id=$1::uuid AND run_id=$2`, wsUUID, strings.TrimSpace(runID)).
		Scan(&run.WorkspaceID, &run.RunID, &run.Label, &run.Status,
			&run.CreatedByActor, &run.CreatedAt, &run.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return GraphMemoryEvaluationRunView{}, nil, ErrGraphMemoryEvaluationNotFound
	}
	if err != nil {
		return GraphMemoryEvaluationRunView{}, nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT run_id, episode_id, channel_id::text, primary_agent_id::text, arm, memory_policy,
		       session_generation, status, COALESCE(input_message_id::text,''), COALESCE(output_message_id::text,''),
		       closure_checklist, official_score_state, official_score, failure_reason,
		       created_at, started_at, settled_at
		FROM graph_memory_evaluation_episode
		WHERE workspace_id=$1::uuid AND run_id=$2
		ORDER BY created_at`, wsUUID, strings.TrimSpace(runID))
	if err != nil {
		return GraphMemoryEvaluationRunView{}, nil, err
	}
	defer rows.Close()
	episodes := []GraphMemoryEvaluationEpisodeView{}
	for rows.Next() {
		var view GraphMemoryEvaluationEpisodeView
		if err := rows.Scan(&view.RunID, &view.EpisodeID, &view.ChannelID, &view.PrimaryAgentID,
			&view.Arm, &view.MemoryPolicy, &view.SessionGeneration, &view.Status,
			&view.InputMessageID, &view.OutputMessageID, &view.ClosureChecklist,
			&view.OfficialScoreState, &view.OfficialScore, &view.FailureReason,
			&view.CreatedAt, &view.StartedAt, &view.SettledAt); err != nil {
			return GraphMemoryEvaluationRunView{}, nil, err
		}
		episodes = append(episodes, view)
	}
	return run, episodes, rows.Err()
}

func validateGraphMemoryClosureChecklist(checklist map[string]GraphMemoryClosureState) error {
	for _, condition := range graphMemoryEvaluationClosureConditions {
		reported, ok := checklist[condition]
		if !ok {
			return fmt.Errorf("%w: missing condition %s", ErrGraphMemoryEvaluationClosure, condition)
		}
		if reported.State != "complete" {
			return fmt.Errorf("%w: condition %s is %s", ErrGraphMemoryEvaluationClosure, condition, reported.State)
		}
	}
	return nil
}

func encodeGraphMemoryClosureChecklist(checklist map[string]GraphMemoryClosureState) (json.RawMessage, error) {
	encoded := make(map[string]GraphMemoryClosureState, len(graphMemoryEvaluationClosureConditions))
	for _, condition := range graphMemoryEvaluationClosureConditions {
		if reported, ok := checklist[condition]; ok {
			encoded[condition] = reported
		}
	}
	raw, err := json.Marshal(encoded)
	if err != nil {
		return nil, err
	}
	return raw, nil
}
