package researcheval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/researchrun"
)

var (
	ErrEvaluationRunTimeout = errors.New("research evaluation run timed out")
	ErrInvalidEvaluationRun = errors.New("invalid research evaluation run")
)

type EvaluationRunRef struct {
	SessionID   string
	WorkspaceID string
}

type EvaluationRunLauncher interface {
	LaunchEvaluationRun(context.Context, SubjectInput, int64) (EvaluationRunRef, error)
}

type EvaluationRunReader interface {
	SnapshotEvaluationRun(context.Context, EvaluationRunRef) (researchrun.RunSnapshot, error)
}

type EvaluationArtifactExtractor interface {
	ExtractEvaluationArtifact(context.Context, SubjectInput, int64, EvaluationRunRef, researchrun.RunSnapshot) (Artifact, error)
}

type LifecycleExecutorOptions struct {
	PollInterval time.Duration
	Timeout      time.Duration
	After        func(context.Context, time.Duration) error
	Now          func() time.Time
}

type LifecycleExecutor struct {
	launcher  EvaluationRunLauncher
	reader    EvaluationRunReader
	extractor EvaluationArtifactExtractor
	options   LifecycleExecutorOptions
}

func NewLifecycleExecutor(launcher EvaluationRunLauncher, reader EvaluationRunReader, extractor EvaluationArtifactExtractor, options LifecycleExecutorOptions) (*LifecycleExecutor, error) {
	if launcher == nil || reader == nil || extractor == nil {
		return nil, fmt.Errorf("%w: launcher, reader, and extractor are required", ErrInvalidEvaluationRun)
	}
	if options.PollInterval <= 0 {
		options.PollInterval = time.Second
	}
	if options.Timeout <= 0 {
		options.Timeout = 30 * time.Minute
	}
	if options.After == nil {
		options.After = waitForEvaluationPoll
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &LifecycleExecutor{launcher: launcher, reader: reader, extractor: extractor, options: options}, nil
}

func (executor *LifecycleExecutor) Execute(ctx context.Context, subject SubjectInput, seed int64) (Artifact, error) {
	if executor == nil || executor.launcher == nil || executor.reader == nil || executor.extractor == nil {
		return Artifact{}, fmt.Errorf("%w: lifecycle executor is not initialized", ErrInvalidEvaluationRun)
	}
	ref, err := executor.launcher.LaunchEvaluationRun(ctx, subject, seed)
	if err != nil {
		return Artifact{}, fmt.Errorf("launch evaluation run: %w", err)
	}
	ref.SessionID, ref.WorkspaceID = strings.TrimSpace(ref.SessionID), strings.TrimSpace(ref.WorkspaceID)
	if ref.SessionID == "" || ref.WorkspaceID == "" {
		return Artifact{}, fmt.Errorf("%w: launch returned an empty run identity", ErrInvalidEvaluationRun)
	}

	deadline := executor.options.Now().Add(executor.options.Timeout)
	for {
		if err = ctx.Err(); err != nil {
			return Artifact{}, err
		}
		snapshot, snapshotErr := executor.reader.SnapshotEvaluationRun(ctx, ref)
		if snapshotErr != nil {
			return Artifact{}, fmt.Errorf("snapshot evaluation run %q: %w", ref.SessionID, snapshotErr)
		}
		if snapshot.Run.SessionID != ref.SessionID || snapshot.Run.WorkspaceID != ref.WorkspaceID {
			return Artifact{}, fmt.Errorf("%w: snapshot identity does not match launched run", ErrInvalidEvaluationRun)
		}
		if evaluationRunTerminal(snapshot.Run.Status) {
			artifact, extractErr := executor.extractor.ExtractEvaluationArtifact(ctx, subject, seed, ref, snapshot)
			if extractErr != nil {
				return Artifact{}, fmt.Errorf("extract evaluation artifact for run %q: %w", ref.SessionID, extractErr)
			}
			return artifact, nil
		}
		if !executor.options.Now().Before(deadline) {
			return Artifact{}, fmt.Errorf("%w: run %q remained %q", ErrEvaluationRunTimeout, ref.SessionID, snapshot.Run.Status)
		}
		if err = executor.options.After(ctx, executor.options.PollInterval); err != nil {
			return Artifact{}, err
		}
	}
}

func evaluationRunTerminal(status researchrun.RunStatus) bool {
	switch status {
	case researchrun.RunStatusCompleted, researchrun.RunStatusFailed, researchrun.RunStatusCancelled, researchrun.RunStatusArchived:
		return true
	default:
		return false
	}
}

func waitForEvaluationPoll(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
