// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// --- fakes ---

type fakeSavepointQueries struct {
	created   []db.CreateSandboxSnapshotParams
	attached  []db.AttachSandboxSnapshotToCheckpointParams
	failed    []db.MarkSandboxSnapshotFailedParams
	instances []db.UpdateSandboxInstanceStatusParams

	// polls is the sequence GetSandboxSnapshotForWorkspace hands back, one per
	// call; the last entry repeats once exhausted.
	polls     []db.SandboxSnapshot
	pollCount int

	createErr error
	attachErr error
}

func (f *fakeSavepointQueries) CreateSandboxSnapshot(_ context.Context, arg db.CreateSandboxSnapshotParams) (db.SandboxSnapshot, error) {
	f.created = append(f.created, arg)
	if f.createErr != nil {
		return db.SandboxSnapshot{}, f.createErr
	}
	return db.SandboxSnapshot{
		ID:          mustUUIDValue(testSnapshotUUID),
		WorkspaceID: arg.WorkspaceID,
		InstanceID:  arg.InstanceID,
		NodeID:      arg.NodeID,
		Status:      arg.Status,
		Name:        arg.Name,
	}, nil
}

func (f *fakeSavepointQueries) AttachSandboxSnapshotToCheckpoint(_ context.Context, arg db.AttachSandboxSnapshotToCheckpointParams) (db.SandboxSnapshot, error) {
	f.attached = append(f.attached, arg)
	if f.attachErr != nil {
		return db.SandboxSnapshot{}, f.attachErr
	}
	return db.SandboxSnapshot{ID: arg.ID, WorkspaceID: arg.WorkspaceID, CheckpointID: arg.CheckpointID, Status: savepointStatusCreating}, nil
}

func (f *fakeSavepointQueries) GetSandboxSnapshotForWorkspace(_ context.Context, _ db.GetSandboxSnapshotForWorkspaceParams) (db.SandboxSnapshot, error) {
	if len(f.polls) == 0 {
		return db.SandboxSnapshot{Status: savepointStatusCreating}, nil
	}
	idx := f.pollCount
	if idx >= len(f.polls) {
		idx = len(f.polls) - 1
	}
	f.pollCount++
	return f.polls[idx], nil
}

func (f *fakeSavepointQueries) MarkSandboxSnapshotFailed(_ context.Context, arg db.MarkSandboxSnapshotFailedParams) (db.SandboxSnapshot, error) {
	f.failed = append(f.failed, arg)
	return db.SandboxSnapshot{ID: arg.ID, Status: savepointStatusFailed, Error: arg.Error}, nil
}

func (f *fakeSavepointQueries) UpdateSandboxInstanceStatus(_ context.Context, arg db.UpdateSandboxInstanceStatusParams) (db.SandboxInstance, error) {
	f.instances = append(f.instances, arg)
	return db.SandboxInstance{ID: arg.ID, Status: arg.Status}, nil
}

type fakeSandboxJobEnqueuer struct {
	jobs     []fakeEnqueuedJob
	notified []string
	err      error
}

type fakeEnqueuedJob struct {
	WorkspaceID string
	ActorUserID string
	NodeID      string
	InstanceID  string
	JobType     string
	Payload     json.RawMessage
}

func (f *fakeSandboxJobEnqueuer) EnqueueSandboxJob(_ context.Context, workspaceID, actorUserID, nodeID, instanceID, jobType string, payload json.RawMessage) (SandboxLifecycleJobResult, error) {
	f.jobs = append(f.jobs, fakeEnqueuedJob{workspaceID, actorUserID, nodeID, instanceID, jobType, payload})
	if f.err != nil {
		return SandboxLifecycleJobResult{}, f.err
	}
	return SandboxLifecycleJobResult{JobID: "job-1", InstanceID: instanceID, NodeID: nodeID, JobType: jobType}, nil
}

func (f *fakeSandboxJobEnqueuer) NotifySandboxJobAvailable(_ context.Context, nodeID, jobID string) error {
	f.notified = append(f.notified, nodeID+"/"+jobID)
	return nil
}

const (
	testSnapshotUUID = "44444444-4444-4444-4444-444444444444"
	testInstanceUUID = "55555555-5555-5555-5555-555555555555"
	testNodeUUID     = "66666666-6666-6666-6666-666666666666"
	testTaskUUID     = "88888888-8888-8888-8888-888888888888"
	testAgentUUID    = "99999999-9999-9999-9999-999999999999"
	testRuntimeUUID  = "aaaaaaaa-1111-1111-1111-111111111111"
	testIssueUUID    = "bbbbbbbb-1111-1111-1111-111111111111"
	testSessionUUID  = "cccccccc-1111-1111-1111-111111111111"
	testUserUUID     = "77777777-7777-7777-7777-777777777777"
)

func mustUUIDValue(s string) pgtype.UUID {
	var u pgtype.UUID
	_ = u.Scan(s)
	return u
}

func testSourceRef() SandboxInstanceRef {
	return SandboxInstanceRef{
		InstanceID:  testInstanceUUID,
		WorkspaceID: testWorkspaceUUID,
		NodeID:      testNodeUUID,
		LocalRef:    "cube-live-1",
	}
}

func newTestSavepointCreator(q savepointQueries, jobs sandboxJobEnqueuer) SavepointCreator {
	// A millisecond poll interval keeps the wait real (the loop is exercised)
	// without making the test sleep.
	return newSavepointCreator(q, jobs, 2*time.Second, time.Millisecond)
}

// --- tests ---

// TestSavepointCreateEnqueuesCreateTemplateAndOwnsTheSnapshot is the core of the
// savepoint primitive: one create_template job against the live source, and the
// snapshot row bound to the checkpoint that asked for it. Ownership is what
// later lets deletion release the Cube template, so an unattached savepoint
// leaks it.
func TestSavepointCreateEnqueuesCreateTemplateAndOwnsTheSnapshot(t *testing.T) {
	q := &fakeSavepointQueries{polls: []db.SandboxSnapshot{{
		ID: mustUUIDValue(testSnapshotUUID), Status: savepointStatusReady, CubeSnapshotID: "cube-tmpl-1",
		InstanceID: mustUUIDValue(testInstanceUUID),
	}}}
	jobs := &fakeSandboxJobEnqueuer{}

	sp, err := newTestSavepointCreator(q, jobs).CreateSavepoint(context.Background(), testSourceRef(), testCheckpointUUID, testUserUUID)
	if err != nil {
		t.Fatalf("create savepoint: %v", err)
	}
	if sp.Status != savepointStatusReady || sp.CubeSnapshotID != "cube-tmpl-1" {
		t.Fatalf("savepoint = %+v, want a ready snapshot carrying its Cube template", sp)
	}
	if sp.SnapshotID != testSnapshotUUID || sp.InstanceID != testInstanceUUID {
		t.Fatalf("savepoint identity = %+v", sp)
	}
	if len(jobs.jobs) != 1 || jobs.jobs[0].JobType != "create_template" {
		t.Fatalf("jobs = %+v, want a single create_template", jobs.jobs)
	}
	if jobs.jobs[0].InstanceID != testInstanceUUID || jobs.jobs[0].NodeID != testNodeUUID {
		t.Fatalf("job addressed the wrong instance/node: %+v", jobs.jobs[0])
	}
	var payload map[string]any
	if err := json.Unmarshal(jobs.jobs[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	// sandboxd reads snapshot_id to mark the row ready, and local_ref to know
	// which live Cube sandbox to snapshot; without either the job is a no-op that
	// leaves the row 'creating' forever.
	if payload["snapshot_id"] != testSnapshotUUID {
		t.Fatalf("payload snapshot_id = %v", payload["snapshot_id"])
	}
	if payload["local_ref"] != "cube-live-1" {
		t.Fatalf("payload local_ref = %v", payload["local_ref"])
	}
	if len(q.attached) != 1 || q.attached[0].CheckpointID != mustUUIDValue(testCheckpointUUID) {
		t.Fatalf("snapshot was not bound to its checkpoint: %+v", q.attached)
	}
	if len(jobs.notified) != 1 {
		t.Fatalf("node was not notified: %+v", jobs.notified)
	}
}

// TestSavepointCreateLeavesTheSourceRunning is the property that separates a
// savepoint from a save: no stop job, because the source keeps working while its
// state is captured. Enqueueing a stop here would silently turn every
// snapshot-mode checkpoint back into pause-in-place.
func TestSavepointCreateLeavesTheSourceRunning(t *testing.T) {
	q := &fakeSavepointQueries{polls: []db.SandboxSnapshot{{
		ID: mustUUIDValue(testSnapshotUUID), Status: savepointStatusReady, CubeSnapshotID: "cube-tmpl-1",
	}}}
	jobs := &fakeSandboxJobEnqueuer{}

	if _, err := newTestSavepointCreator(q, jobs).CreateSavepoint(context.Background(), testSourceRef(), testCheckpointUUID, testUserUUID); err != nil {
		t.Fatalf("create savepoint: %v", err)
	}
	for _, job := range jobs.jobs {
		if job.JobType == "stop" || job.JobType == "delete" {
			t.Fatalf("savepoint must not disturb the source, got a %q job", job.JobType)
		}
	}
}

// TestSavepointCreateWaitsForTheRowToLeaveCreating pins that the creator blocks
// on the terminal state instead of returning the 'creating' row it just wrote.
// Returning early would hand back an empty Cube template id, and the lane built
// from it would boot a blank sandbox.
func TestSavepointCreateWaitsForTheRowToLeaveCreating(t *testing.T) {
	q := &fakeSavepointQueries{polls: []db.SandboxSnapshot{
		{Status: savepointStatusCreating},
		{Status: savepointStatusCreating},
		{ID: mustUUIDValue(testSnapshotUUID), Status: savepointStatusReady, CubeSnapshotID: "cube-tmpl-1"},
	}}

	sp, err := newTestSavepointCreator(q, &fakeSandboxJobEnqueuer{}).CreateSavepoint(
		context.Background(), testSourceRef(), testCheckpointUUID, testUserUUID)
	if err != nil {
		t.Fatalf("create savepoint: %v", err)
	}
	if sp.Status != savepointStatusReady {
		t.Fatalf("status = %q, want ready", sp.Status)
	}
	if q.pollCount < 3 {
		t.Fatalf("polls = %d, want the creator to keep waiting while the row said creating", q.pollCount)
	}
}

// TestSavepointCreateReportsAFailedSnapshotWithoutAnError returns the observed
// row rather than an error, because EnvCheckpointService.Create is what turns a
// non-ready savepoint into ErrSavepointFailed with the snapshot id attached.
func TestSavepointCreateReportsAFailedSnapshotWithoutAnError(t *testing.T) {
	q := &fakeSavepointQueries{polls: []db.SandboxSnapshot{{
		ID: mustUUIDValue(testSnapshotUUID), Status: savepointStatusFailed,
		Error: pgtype.Text{String: "cube refused", Valid: true},
	}}}

	sp, err := newTestSavepointCreator(q, &fakeSandboxJobEnqueuer{}).CreateSavepoint(
		context.Background(), testSourceRef(), testCheckpointUUID, testUserUUID)
	if err != nil {
		t.Fatalf("a failed snapshot is reported through Status, not an error: %v", err)
	}
	if sp.Status != savepointStatusFailed {
		t.Fatalf("status = %q, want failed", sp.Status)
	}
}

// TestSavepointCreateFailsTheRowOnTimeout keeps a lost job from leaving a row
// stuck in 'creating' forever, which would make the checkpoint look
// perpetually in progress and block its deletion.
func TestSavepointCreateFailsTheRowOnTimeout(t *testing.T) {
	q := &fakeSavepointQueries{polls: []db.SandboxSnapshot{{Status: savepointStatusCreating}}}

	sp, err := newSavepointCreator(q, &fakeSandboxJobEnqueuer{}, 20*time.Millisecond, time.Millisecond).
		CreateSavepoint(context.Background(), testSourceRef(), testCheckpointUUID, testUserUUID)
	if err == nil {
		t.Fatal("a savepoint that never reached a terminal state must be an error")
	}
	if sp.Status == savepointStatusReady {
		t.Fatalf("a timed-out savepoint must not report ready: %+v", sp)
	}
	if len(q.failed) != 1 {
		t.Fatalf("the stuck row must be marked failed, got %+v", q.failed)
	}
}

func TestSavepointCreateRejectsMalformedIDsBeforeWriting(t *testing.T) {
	q := &fakeSavepointQueries{}
	creator := newTestSavepointCreator(q, &fakeSandboxJobEnqueuer{})

	bad := testSourceRef()
	bad.InstanceID = "not-a-uuid"
	if _, err := creator.CreateSavepoint(context.Background(), bad, testCheckpointUUID, testUserUUID); err == nil {
		t.Fatal("a malformed instance id must be rejected")
	}
	if _, err := creator.CreateSavepoint(context.Background(), testSourceRef(), "not-a-uuid", testUserUUID); err == nil {
		t.Fatal("a malformed checkpoint id must be rejected")
	}
	if len(q.created) != 0 {
		t.Fatalf("nothing may reach the database, got %+v", q.created)
	}
}

// TestSavepointCreateFailsTheRowWhenTheJobCannotBeEnqueued mirrors the existing
// snapshot endpoint: a row with no job behind it is dead, so it is failed
// immediately rather than waited out.
func TestSavepointCreateFailsTheRowWhenTheJobCannotBeEnqueued(t *testing.T) {
	q := &fakeSavepointQueries{}
	jobs := &fakeSandboxJobEnqueuer{err: errors.New("queue down")}

	if _, err := newTestSavepointCreator(q, jobs).CreateSavepoint(
		context.Background(), testSourceRef(), testCheckpointUUID, testUserUUID); err == nil {
		t.Fatal("a failed enqueue must be an error")
	}
	if len(q.failed) != 1 {
		t.Fatalf("the orphaned row must be marked failed, got %+v", q.failed)
	}
}

func TestSavepointTerminalStates(t *testing.T) {
	for status, wantTerminal := range map[string]bool{
		savepointStatusCreating: false,
		savepointStatusReady:    true,
		savepointStatusFailed:   true,
		// A savepoint already being deleted is terminal for the purposes of
		// waiting: it will never become ready, so waiting is pointless.
		savepointStatusDeleting: true,
		"":                      false,
	} {
		if got := savepointStatusIsTerminal(status); got != wantTerminal {
			t.Fatalf("savepointStatusIsTerminal(%q) = %v, want %v", status, got, wantTerminal)
		}
	}
}
