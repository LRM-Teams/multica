package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type fakeReleaseQueries struct {
	snap       db.SandboxSnapshot
	getErr     error
	getArg     db.GetSandboxSnapshotForWorkspaceParams
	deleted    []db.DeleteSandboxSnapshotParams
	deleteErr  error
	deleteCall int
}

func (f *fakeReleaseQueries) GetSandboxSnapshotForWorkspace(_ context.Context, arg db.GetSandboxSnapshotForWorkspaceParams) (db.SandboxSnapshot, error) {
	f.getArg = arg
	return f.snap, f.getErr
}

func (f *fakeReleaseQueries) DeleteSandboxSnapshot(_ context.Context, arg db.DeleteSandboxSnapshotParams) error {
	f.deleteCall++
	f.deleted = append(f.deleted, arg)
	return f.deleteErr
}

type fakeDeletionScheduler struct {
	scheduled []string
	err       error
}

func (f *fakeDeletionScheduler) scheduleSnapshotTemplateDeletion(_ context.Context, snap db.SandboxSnapshot, _ pgtype.UUID, _ string) (db.SandboxSnapshot, error) {
	if f.err != nil {
		return db.SandboxSnapshot{}, f.err
	}
	f.scheduled = append(f.scheduled, snap.CubeSnapshotID)
	return snap, nil
}

const (
	releaseSnapshotID = "aaaaaaaa-1111-2222-3333-444444444444"
	releaseWorkspace  = "bbbbbbbb-1111-2222-3333-444444444444"
)

func newReleaser(t *testing.T, snap db.SandboxSnapshot) (*savepointReleaserAdapter, *fakeReleaseQueries, *fakeDeletionScheduler) {
	t.Helper()
	q := &fakeReleaseQueries{snap: snap}
	sched := &fakeDeletionScheduler{}
	return &savepointReleaserAdapter{q: q, scheduler: sched}, q, sched
}

func readySnapshot(t *testing.T) db.SandboxSnapshot {
	t.Helper()
	id, err := util.ParseUUID(releaseSnapshotID)
	if err != nil {
		t.Fatalf("parse snapshot id: %v", err)
	}
	return db.SandboxSnapshot{ID: id, CubeSnapshotID: "cube-1", Status: "ready"}
}

// Releasing a ready savepoint schedules its template for deletion through the
// same job a user-initiated snapshot delete uses.
func TestReleaseSavepointSchedulesTemplateDeletion(t *testing.T) {
	rel, q, sched := newReleaser(t, readySnapshot(t))

	if err := rel.ReleaseSavepoint(context.Background(), releaseSnapshotID, releaseWorkspace, "u"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if len(sched.scheduled) != 1 || sched.scheduled[0] != "cube-1" {
		t.Fatalf("scheduled = %v, want the savepoint's template", sched.scheduled)
	}
	wantWS, _ := util.ParseUUID(releaseWorkspace)
	if q.getArg.WorkspaceID != wantWS {
		t.Fatalf("lookup arg = %+v, want it scoped to the workspace", q.getArg)
	}
}

// A savepoint that is already gone must not fail the release. Release runs while
// deleting a checkpoint, so failing here would pin the checkpoint row forever on
// a savepoint that no longer exists.
func TestReleaseSavepointThatIsAlreadyGoneSucceeds(t *testing.T) {
	rel, q, sched := newReleaser(t, db.SandboxSnapshot{})
	q.getErr = pgx.ErrNoRows

	if err := rel.ReleaseSavepoint(context.Background(), releaseSnapshotID, releaseWorkspace, "u"); err != nil {
		t.Fatalf("release of a vanished savepoint: %v", err)
	}
	if len(sched.scheduled) != 0 {
		t.Fatalf("nothing to schedule, got %v", sched.scheduled)
	}
}

// Already deleting means a job is queued. Enqueuing a second one would ask the
// node to delete the same template twice.
func TestReleaseSavepointAlreadyDeletingIsNotScheduledAgain(t *testing.T) {
	snap := readySnapshot(t)
	snap.Status = "deleting"
	rel, _, sched := newReleaser(t, snap)

	if err := rel.ReleaseSavepoint(context.Background(), releaseSnapshotID, releaseWorkspace, "u"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if len(sched.scheduled) != 0 {
		t.Fatalf("a deleting savepoint must not be re-scheduled, got %v", sched.scheduled)
	}
}

// A savepoint still being written has no settled template to delete; deleting now
// would race the create, so the checkpoint delete is refused and retried later.
func TestReleaseSavepointStillCreatingIsRefused(t *testing.T) {
	snap := readySnapshot(t)
	snap.Status = "creating"
	rel, _, sched := newReleaser(t, snap)

	if err := rel.ReleaseSavepoint(context.Background(), releaseSnapshotID, releaseWorkspace, "u"); err == nil {
		t.Fatal("releasing a savepoint mid-create must be refused")
	}
	if len(sched.scheduled) != 0 {
		t.Fatalf("nothing must be scheduled, got %v", sched.scheduled)
	}
}

// A savepoint that never reached Cube has no template. There is nothing for the
// node to delete, so the row goes directly instead of queuing a job that would
// fail on a template that was never created.
func TestReleaseSavepointWithNoTemplateDropsTheRow(t *testing.T) {
	snap := readySnapshot(t)
	snap.CubeSnapshotID = ""
	rel, q, sched := newReleaser(t, snap)

	if err := rel.ReleaseSavepoint(context.Background(), releaseSnapshotID, releaseWorkspace, "u"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if q.deleteCall != 1 {
		t.Fatalf("row deletes = %d, want 1", q.deleteCall)
	}
	if len(sched.scheduled) != 0 {
		t.Fatalf("no template to schedule, got %v", sched.scheduled)
	}
}

// A failure to schedule has to surface, or the checkpoint row would be deleted
// while its template is still on the node with nothing left pointing at it.
func TestReleaseSavepointReportsASchedulingFailure(t *testing.T) {
	rel, _, sched := newReleaser(t, readySnapshot(t))
	sched.err = errors.New("queue unavailable")

	if err := rel.ReleaseSavepoint(context.Background(), releaseSnapshotID, releaseWorkspace, "u"); err == nil {
		t.Fatal("a scheduling failure must fail the release")
	}
}

func TestReleaseSavepointRejectsMalformedIDs(t *testing.T) {
	rel, _, _ := newReleaser(t, readySnapshot(t))
	if err := rel.ReleaseSavepoint(context.Background(), "not-a-uuid", releaseWorkspace, "u"); err == nil {
		t.Fatal("malformed snapshot id must be rejected")
	}
	if err := rel.ReleaseSavepoint(context.Background(), releaseSnapshotID, "not-a-uuid", "u"); err == nil {
		t.Fatal("malformed workspace id must be rejected")
	}
}
