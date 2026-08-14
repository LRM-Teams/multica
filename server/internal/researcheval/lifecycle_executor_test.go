package researcheval

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/researchrun"
)

type lifecycleFixture struct {
	ref       EvaluationRunRef
	snapshots []researchrun.RunSnapshot
	launches  int
	reads     int
	extracts  int
	seenSeed  int64
}

func (fixture *lifecycleFixture) LaunchEvaluationRun(_ context.Context, _ SubjectInput, seed int64) (EvaluationRunRef, error) {
	fixture.launches++
	fixture.seenSeed = seed
	return fixture.ref, nil
}

func (fixture *lifecycleFixture) SnapshotEvaluationRun(_ context.Context, _ EvaluationRunRef) (researchrun.RunSnapshot, error) {
	if len(fixture.snapshots) == 0 {
		return researchrun.RunSnapshot{}, errors.New("fixture has no snapshots")
	}
	index := fixture.reads
	fixture.reads++
	if index >= len(fixture.snapshots) {
		index = len(fixture.snapshots) - 1
	}
	return fixture.snapshots[index], nil
}

func (fixture *lifecycleFixture) ExtractEvaluationArtifact(_ context.Context, _ SubjectInput, seed int64, _ EvaluationRunRef, snapshot researchrun.RunSnapshot) (Artifact, error) {
	fixture.extracts++
	fixture.seenSeed = seed
	return Artifact{ReportMD: string(snapshot.Run.Status)}, nil
}

func lifecycleSnapshot(status researchrun.RunStatus) researchrun.RunSnapshot {
	return researchrun.RunSnapshot{Run: researchrun.Run{SessionID: "run-1", WorkspaceID: "workspace-1", Status: status}}
}

func TestLifecycleExecutorWaitsForExplicitTerminalStateThenExtracts(t *testing.T) {
	fixture := &lifecycleFixture{
		ref: EvaluationRunRef{SessionID: "run-1", WorkspaceID: "workspace-1"},
		snapshots: []researchrun.RunSnapshot{
			lifecycleSnapshot(researchrun.RunStatusRunning),
			lifecycleSnapshot(researchrun.RunStatusAwaitingUserConfirm),
			lifecycleSnapshot(researchrun.RunStatusCompleted),
		},
	}
	executor, err := NewLifecycleExecutor(fixture, fixture, fixture, LifecycleExecutorOptions{PollInterval: time.Millisecond, Timeout: time.Minute, After: func(context.Context, time.Duration) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := executor.Execute(context.Background(), SubjectInput{}, 41)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.ReportMD != "completed" || fixture.launches != 1 || fixture.reads != 3 || fixture.extracts != 1 || fixture.seenSeed != 41 {
		t.Fatalf("artifact=%+v fixture=%+v", artifact, fixture)
	}
}

func TestLifecycleExecutorExtractsFailedRunAsObservableArtifact(t *testing.T) {
	fixture := &lifecycleFixture{ref: EvaluationRunRef{SessionID: "run-1", WorkspaceID: "workspace-1"}, snapshots: []researchrun.RunSnapshot{lifecycleSnapshot(researchrun.RunStatusFailed)}}
	executor, _ := NewLifecycleExecutor(fixture, fixture, fixture, LifecycleExecutorOptions{})
	artifact, err := executor.Execute(context.Background(), SubjectInput{}, 1)
	if err != nil || artifact.ReportMD != "failed" || fixture.extracts != 1 {
		t.Fatalf("artifact=%+v err=%v extracts=%d", artifact, err, fixture.extracts)
	}
}

func TestLifecycleExecutorRejectsIdentityDriftBeforeExtraction(t *testing.T) {
	fixture := &lifecycleFixture{ref: EvaluationRunRef{SessionID: "run-1", WorkspaceID: "workspace-1"}, snapshots: []researchrun.RunSnapshot{{Run: researchrun.Run{SessionID: "other", WorkspaceID: "workspace-1", Status: researchrun.RunStatusCompleted}}}}
	executor, _ := NewLifecycleExecutor(fixture, fixture, fixture, LifecycleExecutorOptions{})
	_, err := executor.Execute(context.Background(), SubjectInput{}, 1)
	if !errors.Is(err, ErrInvalidEvaluationRun) || fixture.extracts != 0 {
		t.Fatalf("err=%v extracts=%d", err, fixture.extracts)
	}
}

func TestLifecycleExecutorPropagatesCancellationWhileWaiting(t *testing.T) {
	fixture := &lifecycleFixture{ref: EvaluationRunRef{SessionID: "run-1", WorkspaceID: "workspace-1"}, snapshots: []researchrun.RunSnapshot{lifecycleSnapshot(researchrun.RunStatusRunning)}}
	ctx, cancel := context.WithCancel(context.Background())
	executor, _ := NewLifecycleExecutor(fixture, fixture, fixture, LifecycleExecutorOptions{After: func(context.Context, time.Duration) error { cancel(); return context.Canceled }})
	_, err := executor.Execute(ctx, SubjectInput{}, 1)
	if !errors.Is(err, context.Canceled) || fixture.extracts != 0 {
		t.Fatalf("err=%v extracts=%d", err, fixture.extracts)
	}
}

func TestLifecycleExecutorTimesOutWithoutExtractingPartialRun(t *testing.T) {
	fixture := &lifecycleFixture{ref: EvaluationRunRef{SessionID: "run-1", WorkspaceID: "workspace-1"}, snapshots: []researchrun.RunSnapshot{lifecycleSnapshot(researchrun.RunStatusRunning)}}
	now := time.Unix(1_700_000_000, 0)
	executor, err := NewLifecycleExecutor(fixture, fixture, fixture, LifecycleExecutorOptions{
		Timeout: time.Minute,
		Now:     func() time.Time { return now },
		After: func(context.Context, time.Duration) error {
			now = now.Add(time.Minute)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), SubjectInput{}, 1)
	if !errors.Is(err, ErrEvaluationRunTimeout) || fixture.reads != 2 || fixture.extracts != 0 {
		t.Fatalf("err=%v reads=%d extracts=%d", err, fixture.reads, fixture.extracts)
	}
}
