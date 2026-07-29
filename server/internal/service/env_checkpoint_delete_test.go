// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"testing"
)

type fakeSavepointReleaser struct {
	released []string
	err      error
}

func (f *fakeSavepointReleaser) ReleaseSavepoint(_ context.Context, snapshotID, _, _ string) error {
	if f.err != nil {
		return f.err
	}
	f.released = append(f.released, snapshotID)
	return nil
}

type deleteFixture struct {
	repo      *fakeCheckpointRepo
	lanes     *fakeLaneRepo
	points    *fakeSavepointReader
	releaser  *fakeSavepointReleaser
	service   *EnvCheckpointService
	savepoint Savepoint
}

func newDeleteFixture(t *testing.T) *deleteFixture {
	t.Helper()
	f := &deleteFixture{
		repo:     newFakeCheckpointRepo(),
		lanes:    newFakeLaneRepo(),
		points:   &fakeSavepointReader{savepoints: []Savepoint{{SnapshotID: "snap-1", CubeSnapshotID: "cube-1", Status: savepointStatusReady}}},
		releaser: &fakeSavepointReleaser{},
	}
	f.repo.checkpoints["cp-1"] = EnvCheckpoint{
		ID: "cp-1", WorkspaceID: "ws", SaveMode: SaveModeSnapshot,
		SaveStatus: EnvCheckpointSaveComplete,
	}
	f.service = NewEnvCheckpointService(f.repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{},
		&fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, ContinuationRegistry{}).
		WithLanes(f.lanes, &fakeLaneMaterializer{}, f.points).
		WithSavepointReleaser(f.releaser)
	return f
}

// Deleting a checkpoint has to release its savepoints, because the checkpoint is
// the only thing that kept those Cube templates alive: the row cascades, so
// nothing would remain to tell anyone the templates are garbage.
func TestDeleteCheckpointReleasesItsSavepointsAndRemovesTheRow(t *testing.T) {
	f := newDeleteFixture(t)
	f.lanes.seed(EnvCheckpointLane{
		ID: "l-1", CheckpointID: "cp-1", WorkspaceID: "ws", LaneKey: "k0", Status: LaneStatusReady,
	})

	if err := f.service.Delete(context.Background(), "ws", "cp-1", "u"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(f.releaser.released) != 1 || f.releaser.released[0] != "snap-1" {
		t.Fatalf("released = %v, want the checkpoint's savepoint", f.releaser.released)
	}
	if _, err := f.repo.GetCheckpoint(context.Background(), "cp-1", "ws"); err == nil {
		t.Fatal("checkpoint row must be gone; its lanes and savepoint ownership cascade with it")
	}
}

// A lane that is still provisioning owns a sandbox the lane row is the only
// record of. Cascading it away would orphan that sandbox, which is what the lane
// status column exists to prevent (design D4).
func TestDeleteCheckpointRefusedWhileALaneIsStillProvisioning(t *testing.T) {
	f := newDeleteFixture(t)
	f.lanes.seed(EnvCheckpointLane{
		ID: "l-1", CheckpointID: "cp-1", WorkspaceID: "ws", LaneKey: "k0",
		Status: LaneStatusProvisioning, InstanceID: "inst-0",
	})

	err := f.service.Delete(context.Background(), "ws", "cp-1", "u")
	if !errors.Is(err, ErrCheckpointHasProvisioningLanes) {
		t.Fatalf("err = %v, want ErrCheckpointHasProvisioningLanes", err)
	}
	// A refusal must leave everything it refused to touch intact, or the caller
	// retrying sees a half-deleted checkpoint.
	if len(f.releaser.released) != 0 {
		t.Fatalf("refused deletion released %v", f.releaser.released)
	}
	lane, err := f.lanes.GetLane(context.Background(), "cp-1", "ws", "k0")
	if err != nil || lane.InstanceID != "inst-0" {
		t.Fatalf("lane = %+v, err = %v; refusal must retain the lane and its sandbox", lane, err)
	}
	if _, err := f.repo.GetCheckpoint(context.Background(), "cp-1", "ws"); err != nil {
		t.Fatalf("refusal must retain the checkpoint row: %v", err)
	}
}

// Terminal lanes hold nothing: a ready lane's sandbox belongs to its env, and a
// failed lane's was already reclaimed. Only provisioning blocks.
func TestDeleteCheckpointAllowedWithTerminalLanes(t *testing.T) {
	for _, status := range []string{LaneStatusReady, LaneStatusFailed} {
		t.Run(status, func(t *testing.T) {
			f := newDeleteFixture(t)
			f.lanes.seed(EnvCheckpointLane{
				ID: "l-1", CheckpointID: "cp-1", WorkspaceID: "ws", LaneKey: "k0", Status: status,
			})
			if err := f.service.Delete(context.Background(), "ws", "cp-1", "u"); err != nil {
				t.Fatalf("delete with a %s lane: %v", status, err)
			}
		})
	}
}

// If the template cannot be scheduled for deletion, the row must stay. Deleting
// it anyway would drop the only record of a template still on the node, leaking
// it permanently.
func TestDeleteCheckpointKeepsTheRowWhenReleaseFails(t *testing.T) {
	f := newDeleteFixture(t)
	f.releaser.err = errors.New("node unreachable")

	if err := f.service.Delete(context.Background(), "ws", "cp-1", "u"); err == nil {
		t.Fatal("a failed release must fail the delete")
	}
	if _, err := f.repo.GetCheckpoint(context.Background(), "cp-1", "ws"); err != nil {
		t.Fatalf("checkpoint row must survive a failed release: %v", err)
	}
}

// A pause_in_place checkpoint records no savepoint, so deletion has nothing to
// release and must not require the releaser seam.
func TestDeletePauseInPlaceCheckpointNeedsNoReleaser(t *testing.T) {
	f := newDeleteFixture(t)
	f.repo.checkpoints["cp-1"] = EnvCheckpoint{
		ID: "cp-1", WorkspaceID: "ws", SaveMode: SaveModePauseInPlace,
		SaveStatus: EnvCheckpointSaveComplete,
	}
	f.points.savepoints = nil

	if err := f.service.Delete(context.Background(), "ws", "cp-1", "u"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(f.releaser.released) != 0 {
		t.Fatalf("nothing to release, got %v", f.releaser.released)
	}
}

// A snapshot checkpoint with savepoints and no releaser must refuse rather than
// delete the row: dropping it would leak every template it owns.
func TestDeleteSnapshotCheckpointRefusedWithoutAReleaser(t *testing.T) {
	f := newDeleteFixture(t)
	f.service = NewEnvCheckpointService(f.repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{},
		&fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, ContinuationRegistry{}).
		WithLanes(f.lanes, &fakeLaneMaterializer{}, f.points)

	if err := f.service.Delete(context.Background(), "ws", "cp-1", "u"); err == nil {
		t.Fatal("deleting a checkpoint that owns savepoints without a releaser must be refused")
	}
	if _, err := f.repo.GetCheckpoint(context.Background(), "cp-1", "ws"); err != nil {
		t.Fatalf("checkpoint row must survive: %v", err)
	}
}

func TestDeleteCheckpointValidatesItsInput(t *testing.T) {
	f := newDeleteFixture(t)
	for _, tc := range []struct{ name, ws, id string }{
		{"no workspace", "", "cp-1"},
		{"no checkpoint", "ws", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := f.service.Delete(context.Background(), tc.ws, tc.id, "u"); err == nil {
				t.Fatal("expected a validation failure")
			}
		})
	}
}
