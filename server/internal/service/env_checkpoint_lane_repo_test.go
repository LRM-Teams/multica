// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// --- fakes ---

type fakeLaneQueries struct {
	claimed   []db.ClaimEnvCheckpointLaneParams
	got       []db.GetEnvCheckpointLaneParams
	listed    []db.ListEnvCheckpointLanesParams
	steps     []db.UpdateEnvCheckpointLaneStepParams
	readied   []db.MarkEnvCheckpointLaneReadyParams
	failed    []db.MarkEnvCheckpointLaneFailedParams
	counted   []db.CountProvisioningEnvCheckpointLanesParams
	row       db.EnvCheckpointLane
	rows      []db.EnvCheckpointLane
	count     int64
	claimErr  error
	genericEr error
}

func (f *fakeLaneQueries) ClaimEnvCheckpointLane(_ context.Context, arg db.ClaimEnvCheckpointLaneParams) (db.EnvCheckpointLane, error) {
	f.claimed = append(f.claimed, arg)
	if f.claimErr != nil {
		return db.EnvCheckpointLane{}, f.claimErr
	}
	return f.row, nil
}

func (f *fakeLaneQueries) GetEnvCheckpointLane(_ context.Context, arg db.GetEnvCheckpointLaneParams) (db.EnvCheckpointLane, error) {
	f.got = append(f.got, arg)
	if f.genericEr != nil {
		return db.EnvCheckpointLane{}, f.genericEr
	}
	return f.row, nil
}

func (f *fakeLaneQueries) ListEnvCheckpointLanes(_ context.Context, arg db.ListEnvCheckpointLanesParams) ([]db.EnvCheckpointLane, error) {
	f.listed = append(f.listed, arg)
	return f.rows, f.genericEr
}

func (f *fakeLaneQueries) UpdateEnvCheckpointLaneStep(_ context.Context, arg db.UpdateEnvCheckpointLaneStepParams) (db.EnvCheckpointLane, error) {
	f.steps = append(f.steps, arg)
	if f.genericEr != nil {
		return db.EnvCheckpointLane{}, f.genericEr
	}
	return f.row, nil
}

func (f *fakeLaneQueries) MarkEnvCheckpointLaneReady(_ context.Context, arg db.MarkEnvCheckpointLaneReadyParams) (db.EnvCheckpointLane, error) {
	f.readied = append(f.readied, arg)
	return f.row, f.genericEr
}

func (f *fakeLaneQueries) MarkEnvCheckpointLaneFailed(_ context.Context, arg db.MarkEnvCheckpointLaneFailedParams) (db.EnvCheckpointLane, error) {
	f.failed = append(f.failed, arg)
	return f.row, f.genericEr
}

func (f *fakeLaneQueries) CountProvisioningEnvCheckpointLanes(_ context.Context, arg db.CountProvisioningEnvCheckpointLanesParams) (int64, error) {
	f.counted = append(f.counted, arg)
	return f.count, f.genericEr
}

const testLaneUUID = "88888888-8888-8888-8888-888888888888"

func laneRow() db.EnvCheckpointLane {
	return db.EnvCheckpointLane{
		ID:           mustUUIDValue(testLaneUUID),
		CheckpointID: mustUUIDValue(testCheckpointUUID),
		WorkspaceID:  mustUUIDValue(testWorkspaceUUID),
		LaneKey:      "anchor#0",
		Status:       LaneStatusProvisioning,
	}
}

// --- lane repository tests ---

// TestLaneClaimWinsOnInsert covers the normal path: the row the unique index
// accepted is the lane this caller owns.
func TestLaneClaimWinsOnInsert(t *testing.T) {
	q := &fakeLaneQueries{row: laneRow()}
	repo := NewEnvCheckpointLaneRepository(q)

	lane, won, err := repo.ClaimLane(context.Background(), testCheckpointUUID, testWorkspaceUUID, "anchor#0")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !won {
		t.Fatal("an accepted insert must report the claim as won")
	}
	if lane.ID != testLaneUUID || lane.LaneKey != "anchor#0" || lane.Status != LaneStatusProvisioning {
		t.Fatalf("lane = %+v", lane)
	}
	if len(q.claimed) != 1 || q.claimed[0].LaneKey != "anchor#0" {
		t.Fatalf("claim params = %+v", q.claimed)
	}
}

// TestLaneClaimLosingTheRaceIsNotAnError is the idempotency contract: the second
// caller for the same lane key gets won=false and no error, and then reads the
// existing row. Turning no-rows into an error here would make a retried resume
// fail instead of returning the lane it already built.
func TestLaneClaimLosingTheRaceIsNotAnError(t *testing.T) {
	q := &fakeLaneQueries{claimErr: pgx.ErrNoRows}
	repo := NewEnvCheckpointLaneRepository(q)

	_, won, err := repo.ClaimLane(context.Background(), testCheckpointUUID, testWorkspaceUUID, "anchor#0")
	if err != nil {
		t.Fatalf("losing the claim race must not be an error, got %v", err)
	}
	if won {
		t.Fatal("a rejected insert must report the claim as lost")
	}
}

func TestLaneClaimPropagatesRealErrors(t *testing.T) {
	sentinel := errors.New("connection reset")
	repo := NewEnvCheckpointLaneRepository(&fakeLaneQueries{claimErr: sentinel})

	if _, _, err := repo.ClaimLane(context.Background(), testCheckpointUUID, testWorkspaceUUID, "anchor#0"); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the query error", err)
	}
}

// TestLaneClaimRefusesARowFromAnotherWorkspace is a defensive check on the one
// query that does not take workspace_id: it derives it from the checkpoint. If
// the derived workspace ever disagrees with the caller's, something upstream
// loaded a checkpoint it should not have, and continuing would materialize a
// lane into the wrong tenant.
func TestLaneClaimRefusesARowFromAnotherWorkspace(t *testing.T) {
	row := laneRow()
	row.WorkspaceID = mustUUIDValue("99999999-9999-9999-9999-999999999999")
	repo := NewEnvCheckpointLaneRepository(&fakeLaneQueries{row: row})

	if _, _, err := repo.ClaimLane(context.Background(), testCheckpointUUID, testWorkspaceUUID, "anchor#0"); err == nil {
		t.Fatal("a lane derived into a different workspace must be refused")
	}
}

// TestLaneStepOnlyWritesTheIDsItWasGiven pins the recovery mechanism: the query
// COALESCEs, so a step must send NULL for everything it is not filling in. A
// zero UUID sent instead of NULL would overwrite an id recorded by an earlier
// step and break continuation.
func TestLaneStepOnlyWritesTheIDsItWasGiven(t *testing.T) {
	q := &fakeLaneQueries{row: laneRow()}
	repo := NewEnvCheckpointLaneRepository(q)

	if _, err := repo.RecordLaneStep(context.Background(), testLaneUUID, testWorkspaceUUID, LaneStep{
		InstanceID: testInstanceUUID,
	}); err != nil {
		t.Fatalf("record step: %v", err)
	}
	if len(q.steps) != 1 {
		t.Fatalf("writes = %d, want 1", len(q.steps))
	}
	got := q.steps[0]
	if !got.InstanceID.Valid || got.InstanceID != mustUUIDValue(testInstanceUUID) {
		t.Fatalf("instance_id = %+v, want the id we passed", got.InstanceID)
	}
	for name, id := range map[string]pgtype.UUID{
		"project_id":        got.ProjectID,
		"runtime_id":        got.RuntimeID,
		"task_id":           got.TaskID,
		"env_id":            got.EnvID,
		"channel_id":        got.ChannelID,
		"chat_session_id":   got.ChatSessionID,
		"source_message_id": got.SourceMessageID,
	} {
		if id.Valid {
			t.Fatalf("%s was sent as a value; unset steps must be NULL so COALESCE keeps the recorded id", name)
		}
	}
}

func TestLaneStepRejectsAMalformedID(t *testing.T) {
	q := &fakeLaneQueries{row: laneRow()}
	repo := NewEnvCheckpointLaneRepository(q)

	if _, err := repo.RecordLaneStep(context.Background(), testLaneUUID, testWorkspaceUUID, LaneStep{
		InstanceID: "not-a-uuid",
	}); err == nil {
		t.Fatal("a malformed step id must be rejected rather than written as NULL")
	}
	if len(q.steps) != 0 {
		t.Fatalf("nothing may reach the database, got %+v", q.steps)
	}
}

func TestLaneQueriesStayWorkspaceScoped(t *testing.T) {
	q := &fakeLaneQueries{row: laneRow(), rows: []db.EnvCheckpointLane{laneRow()}, count: 2}
	repo := NewEnvCheckpointLaneRepository(q)
	ctx := context.Background()
	ws := mustUUIDValue(testWorkspaceUUID)

	if _, err := repo.GetLane(ctx, testCheckpointUUID, testWorkspaceUUID, "anchor#0"); err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := repo.ListLanes(ctx, testCheckpointUUID, testWorkspaceUUID); err != nil {
		t.Fatalf("list: %v", err)
	}
	if _, err := repo.MarkLaneReady(ctx, testLaneUUID, testWorkspaceUUID); err != nil {
		t.Fatalf("ready: %v", err)
	}
	if _, err := repo.MarkLaneFailed(ctx, testLaneUUID, testWorkspaceUUID, "boom"); err != nil {
		t.Fatalf("failed: %v", err)
	}
	n, err := repo.CountProvisioningLanes(ctx, testCheckpointUUID, testWorkspaceUUID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	if q.got[0].WorkspaceID != ws || q.listed[0].WorkspaceID != ws ||
		q.readied[0].WorkspaceID != ws || q.failed[0].WorkspaceID != ws || q.counted[0].WorkspaceID != ws {
		t.Fatal("every lane query must carry the caller's workspace")
	}
	if q.failed[0].Error.String != "boom" {
		t.Fatalf("failure reason = %q, want it recorded", q.failed[0].Error.String)
	}
}

// TestLaneRowMapsTheConversationIDs guards the columns added for fan-out: a lane
// that cannot report its channel/session/source message cannot be enqueued, so
// dropping them in the mapping fails every lane at the last step.
func TestLaneRowMapsTheConversationIDs(t *testing.T) {
	row := laneRow()
	row.Status = LaneStatusReady
	row.InstanceID = mustUUIDValue(testInstanceUUID)
	row.ChannelID = mustUUIDValue("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	row.ChatSessionID = mustUUIDValue("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	row.SourceMessageID = mustUUIDValue("cccccccc-cccc-cccc-cccc-cccccccccccc")
	row.Error = pgtype.Text{String: "", Valid: false}
	repo := NewEnvCheckpointLaneRepository(&fakeLaneQueries{row: row})

	lane, err := repo.GetLane(context.Background(), testCheckpointUUID, testWorkspaceUUID, "anchor#0")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if lane.ChannelID != "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" ||
		lane.ChatSessionID != "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb" ||
		lane.SourceMessageID != "cccccccc-cccc-cccc-cccc-cccccccccccc" {
		t.Fatalf("conversation ids lost in mapping: %+v", lane)
	}
	if lane.InstanceID != testInstanceUUID || lane.Status != LaneStatusReady {
		t.Fatalf("lane = %+v", lane)
	}
	if lane.Error != "" {
		t.Fatalf("error = %q, want empty for a NULL column", lane.Error)
	}
}

// --- savepoint reader tests ---

type fakeSavepointReaderQueries struct {
	listed []db.ListSandboxSnapshotsForCheckpointParams
	failed []db.MarkSandboxSnapshotFailedParams
	rows   []db.SandboxSnapshot
	err    error
}

func (f *fakeSavepointReaderQueries) ListSandboxSnapshotsForCheckpoint(_ context.Context, arg db.ListSandboxSnapshotsForCheckpointParams) ([]db.SandboxSnapshot, error) {
	f.listed = append(f.listed, arg)
	return f.rows, f.err
}

func (f *fakeSavepointReaderQueries) MarkSandboxSnapshotFailed(_ context.Context, arg db.MarkSandboxSnapshotFailedParams) (db.SandboxSnapshot, error) {
	f.failed = append(f.failed, arg)
	return db.SandboxSnapshot{}, f.err
}

// TestSavepointReaderListsOnlyTheCheckpointsOwnSavepoints is what makes fan-out
// resume address the right templates: the list is keyed on the owning checkpoint
// and the workspace, never on the instance.
func TestSavepointReaderListsOnlyTheCheckpointsOwnSavepoints(t *testing.T) {
	q := &fakeSavepointReaderQueries{rows: []db.SandboxSnapshot{{
		ID: mustUUIDValue(testSnapshotUUID), InstanceID: mustUUIDValue(testInstanceUUID),
		CubeSnapshotID: "cube-tmpl-1", Status: savepointStatusReady,
	}}}
	reader := NewSavepointReader(q)

	sps, err := reader.ListSavepoints(context.Background(), testCheckpointUUID, testWorkspaceUUID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sps) != 1 {
		t.Fatalf("savepoints = %d, want 1", len(sps))
	}
	if sps[0].CubeSnapshotID != "cube-tmpl-1" || sps[0].SnapshotID != testSnapshotUUID || sps[0].InstanceID != testInstanceUUID {
		t.Fatalf("savepoint = %+v", sps[0])
	}
	if q.listed[0].CheckpointID != mustUUIDValue(testCheckpointUUID) || q.listed[0].WorkspaceID != mustUUIDValue(testWorkspaceUUID) {
		t.Fatalf("list was not scoped to the checkpoint and workspace: %+v", q.listed[0])
	}
}

func TestSavepointReaderMarksFailedWithTheReason(t *testing.T) {
	q := &fakeSavepointReaderQueries{}
	if err := NewSavepointReader(q).MarkSavepointFailed(context.Background(), testSnapshotUUID, testWorkspaceUUID, "template gone"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	if len(q.failed) != 1 || q.failed[0].Error.String != "template gone" {
		t.Fatalf("failed = %+v", q.failed)
	}
}

func TestSavepointReaderRejectsMalformedIDs(t *testing.T) {
	q := &fakeSavepointReaderQueries{}
	reader := NewSavepointReader(q)

	if _, err := reader.ListSavepoints(context.Background(), "not-a-uuid", testWorkspaceUUID); err == nil {
		t.Fatal("a malformed checkpoint id must be rejected")
	}
	if err := reader.MarkSavepointFailed(context.Background(), "not-a-uuid", testWorkspaceUUID, "x"); err == nil {
		t.Fatal("a malformed snapshot id must be rejected")
	}
	if len(q.listed) != 0 || len(q.failed) != 0 {
		t.Fatal("nothing may reach the database")
	}
}
