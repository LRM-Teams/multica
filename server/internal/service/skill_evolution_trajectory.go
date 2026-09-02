// SPDX-License-Identifier: Apache-2.0

package service

// Service wiring for the skillevolution trajectory projector (spec
// §12.2). This file adapts durable task data into the domain's neutral
// source structs: redaction, binary rejection, size capping, and artifact
// externalization all come from the shared interaction-DAG sanitizer —
// there is deliberately no second redactor here.

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/skillevolution"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/db/generated"
)

// TrajectoryArtifactChecker reports whether a content-addressed artifact
// ref has a backing blob. The consolidation pipeline wires the real blob
// store; nil fails closed whenever the trajectory carries refs.
type TrajectoryArtifactChecker interface {
	ArtifactExists(ctx context.Context, workspaceID, ref string) (bool, error)
}

// SkillEvolutionTrajectoryProjector builds the evolution-eligible view of
// one durable run from task_message + the authoritative task state.
type SkillEvolutionTrajectoryProjector struct {
	pool      *pgxpool.Pool
	artifacts TrajectoryArtifactChecker
}

func NewSkillEvolutionTrajectoryProjector(pool *pgxpool.Pool, artifacts TrajectoryArtifactChecker) *SkillEvolutionTrajectoryProjector {
	return &SkillEvolutionTrajectoryProjector{pool: pool, artifacts: artifacts}
}

// TaskOutcomeSignal reads the authoritative run state (agent_inbox_event
// is the canonical run table task_message rows hang off) and derives the
// classification signal. Error signatures map through a small explicit
// marker list; anything unrecognized stays unclassified so the domain
// classifier fails closed instead of guessing agent failure.
func (p *SkillEvolutionTrajectoryProjector) TaskOutcomeSignal(ctx context.Context, workspaceIDStr, taskIDStr string) (skillevolution.OutcomeSignal, error) {
	workspaceID, err := util.ParseUUID(workspaceIDStr)
	if err != nil {
		return skillevolution.OutcomeSignal{}, fmt.Errorf("trajectory projector: workspace_id: %w", err)
	}
	taskID, err := util.ParseUUID(taskIDStr)
	if err != nil {
		return skillevolution.OutcomeSignal{}, fmt.Errorf("trajectory projector: task_id: %w", err)
	}
	var status, runError, terminal string
	err = p.pool.QueryRow(ctx, `
		SELECT status, COALESCE(error, ''), COALESCE(terminal_outcome, '')
		FROM agent_inbox_event
		WHERE id = $1 AND workspace_id = $2`,
		taskID, workspaceID).Scan(&status, &runError, &terminal)
	if err != nil {
		return skillevolution.OutcomeSignal{}, fmt.Errorf("trajectory projector: task state: %w", err)
	}
	// terminal_outcome is the authoritative verdict once the run finished;
	// an in-flight failure falls back to the raw status.
	derived := terminal
	if derived == "" {
		derived = status
	}
	signal := skillevolution.OutcomeSignal{Status: normalizeOutcomeStatus(derived)}
	if runError != "" {
		signal.ErrorClass = classifyRunErrorSignature(runError)
	}
	lowered := strings.ToLower(runError)
	signal.Partial = strings.Contains(lowered, "partial")
	return signal, nil
}

// normalizeOutcomeStatus folds the terminal vocabulary onto the closed
// signal vocabulary: successful deliveries are "completed", failures stay
// "failed", and everything else (skipped, expired, cancelled, held,
// no_reply) keeps its own shape so ClassifyOutcome fails closed on it.
func normalizeOutcomeStatus(terminal string) string {
	switch terminal {
	case "completed", "replied", "sent":
		return "completed"
	default:
		return terminal
	}
}

// classifyRunErrorSignature maps recognized error text onto the closed
// class vocabulary. An empty result means unclassified — never a guess.
func classifyRunErrorSignature(runError string) string {
	lowered := strings.ToLower(runError)
	for _, marker := range []string{
		"connection reset", "connection refused", "dial tcp", "context deadline",
		"provider unavailable", "provider error", "upstream 5", "rate limit",
		"disk full", "out of memory", "internal error",
	} {
		if strings.Contains(lowered, marker) {
			return "infrastructure"
		}
	}
	for _, marker := range []string{
		"permission denied", "unauthorized", "forbidden", "not allowed",
		"policy denied", "unsupported",
	} {
		if strings.Contains(lowered, marker) {
			return "policy"
		}
	}
	return ""
}

// ProjectRunTrajectory loads one task's messages, runs them through the
// shared sanitizer, pairs each sanitized row with its write-time
// visibility class, and projects the evolution-eligible view under the
// run-start eligibility snapshot. Dangling artifact refs fail closed: a
// ref with no provable backing blob never enters the corpus.
func (p *SkillEvolutionTrajectoryProjector) ProjectRunTrajectory(
	ctx context.Context,
	workspaceIDStr, taskIDStr string,
	eligibility skillevolution.TrajectoryEligibility,
	outcome skillevolution.OutcomeRecord,
) (skillevolution.ObservableTrajectory, error) {
	if eligibility.RunID != taskIDStr || eligibility.WorkspaceID != workspaceIDStr {
		return skillevolution.ObservableTrajectory{}, fmt.Errorf("%w: eligibility snapshot is pinned to run %s in workspace %s",
			skillevolution.ErrTrajectoryNotEligible, eligibility.RunID, eligibility.WorkspaceID)
	}
	// Cross-tenant guard: the run must resolve inside the workspace
	// before any message is read.
	var exists int
	if err := p.pool.QueryRow(ctx, `
		SELECT count(*) FROM agent_inbox_event
		WHERE id = $1::uuid AND workspace_id = $2::uuid`,
		taskIDStr, workspaceIDStr).Scan(&exists); err != nil {
		return skillevolution.ObservableTrajectory{}, fmt.Errorf("trajectory projector: scope check: %w", err)
	}
	if exists != 1 {
		return skillevolution.ObservableTrajectory{}, fmt.Errorf("%w: task %s is not in workspace %s",
			skillevolution.ErrTrajectoryNotEligible, taskIDStr, workspaceIDStr)
	}

	taskID, err := util.ParseUUID(taskIDStr)
	if err != nil {
		return skillevolution.ObservableTrajectory{}, fmt.Errorf("trajectory projector: task_id: %w", err)
	}
	messages, err := db.New(p.pool).ListTaskMessages(ctx, taskID)
	if err != nil {
		return skillevolution.ObservableTrajectory{}, fmt.Errorf("trajectory projector: load messages: %w", err)
	}
	policy := DefaultSanitizerPolicy()
	sanitized, err := SanitizeTrajectory(messages, policy)
	if err != nil {
		return skillevolution.ObservableTrajectory{}, fmt.Errorf("trajectory projector: sanitize: %w", err)
	}
	// SanitizeTrajectory preserves input order, so sanitized row i pairs
	// with message i and inherits its write-time visibility class.
	segment := skillevolution.SourceSegment{
		SegmentID:        "task:" + taskIDStr,
		SanitizerVersion: sanitized.SanitizerVersion,
		PolicyVersion:    policy.PolicyVersion,
		Messages:         make([]skillevolution.SourceMessage, 0, len(sanitized.Messages)),
		ArtifactRefs:     sanitized.ArtifactRefs,
	}
	for index, row := range sanitized.Messages {
		visibility := "diagnostic_only"
		if index < len(messages) {
			visibility = messages[index].Visibility
		}
		segment.Messages = append(segment.Messages, skillevolution.SourceMessage{
			Sequence: int64(row.Sequence), Type: row.Type, Tool: row.Tool,
			Content: row.Content, Input: row.Input, Output: row.Output,
			Visibility: visibility,
		})
	}
	trajectory, err := skillevolution.ProjectObservableTrajectory(eligibility, outcome, []skillevolution.SourceSegment{segment})
	if err != nil {
		return skillevolution.ObservableTrajectory{}, err
	}
	for _, ref := range trajectory.ArtifactRefs {
		if p.artifacts == nil {
			return skillevolution.ObservableTrajectory{}, fmt.Errorf("%w: no artifact checker is wired but the trajectory carries refs",
				skillevolution.ErrTrajectoryArtifactRef)
		}
		exists, err := p.artifacts.ArtifactExists(ctx, workspaceIDStr, ref)
		if err != nil {
			return skillevolution.ObservableTrajectory{}, fmt.Errorf("trajectory projector: artifact check: %w", err)
		}
		if !exists {
			return skillevolution.ObservableTrajectory{}, fmt.Errorf("%w: %s has no backing blob",
				skillevolution.ErrTrajectoryArtifactRef, ref)
		}
	}
	return trajectory, nil
}
