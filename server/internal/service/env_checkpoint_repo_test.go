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

// --- fake queries ---

type fakeCheckpointQueries struct {
	created  []db.CreateEnvCheckpointParams
	updated  []db.UpdateEnvCheckpointSaveStatusParams
	row      db.EnvCheckpoint
	rows     []db.EnvCheckpoint
	err      error
	getParam db.GetEnvCheckpointForWorkspaceParams
	lsParam  db.ListEnvCheckpointsForProjectParams
	deleted  []db.DeleteEnvCheckpointParams
}

func (f *fakeCheckpointQueries) DeleteEnvCheckpoint(_ context.Context, arg db.DeleteEnvCheckpointParams) error {
	f.deleted = append(f.deleted, arg)
	return f.err
}

func (f *fakeCheckpointQueries) CreateEnvCheckpoint(_ context.Context, arg db.CreateEnvCheckpointParams) (db.EnvCheckpoint, error) {
	f.created = append(f.created, arg)
	if f.err != nil {
		return db.EnvCheckpoint{}, f.err
	}
	return f.row, nil
}

func (f *fakeCheckpointQueries) GetEnvCheckpointForWorkspace(_ context.Context, arg db.GetEnvCheckpointForWorkspaceParams) (db.EnvCheckpoint, error) {
	f.getParam = arg
	if f.err != nil {
		return db.EnvCheckpoint{}, f.err
	}
	return f.row, nil
}

func (f *fakeCheckpointQueries) ListEnvCheckpointsForProject(_ context.Context, arg db.ListEnvCheckpointsForProjectParams) ([]db.EnvCheckpoint, error) {
	f.lsParam = arg
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

func (f *fakeCheckpointQueries) UpdateEnvCheckpointSaveStatus(_ context.Context, arg db.UpdateEnvCheckpointSaveStatusParams) (db.EnvCheckpoint, error) {
	f.updated = append(f.updated, arg)
	if f.err != nil {
		return db.EnvCheckpoint{}, f.err
	}
	return f.row, nil
}

func mustUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		t.Fatalf("scan uuid %q: %v", s, err)
	}
	return u
}

const (
	testWorkspaceUUID  = "11111111-1111-1111-1111-111111111111"
	testProjectUUID    = "22222222-2222-2222-2222-222222222222"
	testCheckpointUUID = "33333333-3333-3333-3333-333333333333"
)

// --- tests ---

// TestCheckpointRepoCreateCarriesSaveModeAndRefs guards the field that the whole
// change hinges on: a checkpoint whose save_mode does not survive the write is
// read back as pause_in_place and refuses to fan out.
func TestCheckpointRepoCreateCarriesSaveModeAndRefs(t *testing.T) {
	q := &fakeCheckpointQueries{row: db.EnvCheckpoint{
		ID:          mustUUID(t, testCheckpointUUID),
		WorkspaceID: mustUUID(t, testWorkspaceUUID),
		ProjectID:   mustUUID(t, testProjectUUID),
		SaveMode:    string(SaveModeSnapshot),
		SaveStatus:  string(EnvCheckpointSavePending),
	}}
	repo := NewEnvCheckpointRepository(q)

	cp, err := repo.CreateCheckpoint(context.Background(), EnvCheckpointCreateInput{
		WorkspaceID: testWorkspaceUUID,
		ProjectID:   testProjectUUID,
		EventRef:    "evt-1",
		Kind:        "manual",
		SaveMode:    SaveModeSnapshot,
		EnvIDMap:    map[string]string{"src": "dst"},
		SandboxRefs: []SandboxInstanceRef{{InstanceID: "inst-1", WorkspaceID: testWorkspaceUUID}},
		DBSnapshot:  json.RawMessage(`{"issues":[]}`),
		SaveTimeout: 7 * time.Second,
	}, EnvCheckpointSavePending, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(q.created) != 1 {
		t.Fatalf("writes = %d, want 1", len(q.created))
	}
	got := q.created[0]
	if got.SaveMode != string(SaveModeSnapshot) {
		t.Fatalf("save_mode written = %q, want snapshot", got.SaveMode)
	}
	if got.SaveTimeoutMs != 7000 {
		t.Fatalf("save_timeout_ms = %d, want 7000", got.SaveTimeoutMs)
	}
	if got.SaveStatus != string(EnvCheckpointSavePending) {
		t.Fatalf("save_status = %q, want pending", got.SaveStatus)
	}
	var refs []SandboxInstanceRef
	if err := json.Unmarshal(got.SandboxRefs, &refs); err != nil {
		t.Fatalf("sandbox_refs is not valid json: %v", err)
	}
	if len(refs) != 1 || refs[0].InstanceID != "inst-1" {
		t.Fatalf("sandbox_refs = %+v, want the one source instance", refs)
	}
	if cp.SaveMode != SaveModeSnapshot {
		t.Fatalf("returned save_mode = %q, want snapshot", cp.SaveMode)
	}
	if cp.ID != testCheckpointUUID {
		t.Fatalf("returned id = %q, want %q", cp.ID, testCheckpointUUID)
	}
}

// TestCheckpointRepoDefaultsMissingSaveModeToPauseInPlace covers rows written
// before migration 244 as well as the empty-string case: the column is NOT NULL
// with a pause_in_place default, and reading a legacy row must resolve the same
// way rather than yielding an invalid empty mode.
func TestCheckpointRepoDefaultsMissingSaveModeToPauseInPlace(t *testing.T) {
	q := &fakeCheckpointQueries{row: db.EnvCheckpoint{
		ID:          mustUUID(t, testCheckpointUUID),
		WorkspaceID: mustUUID(t, testWorkspaceUUID),
		ProjectID:   mustUUID(t, testProjectUUID),
		SaveMode:    "",
		SaveStatus:  string(EnvCheckpointSaveComplete),
	}}
	repo := NewEnvCheckpointRepository(q)

	cp, err := repo.GetCheckpoint(context.Background(), testCheckpointUUID, testWorkspaceUUID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cp.SaveMode != SaveModePauseInPlace {
		t.Fatalf("legacy save_mode = %q, want pause_in_place", cp.SaveMode)
	}
}

// TestCheckpointRepoStaysWorkspaceScoped pins that the workspace never comes
// from anywhere but the caller: a checkpoint read with the wrong workspace must
// query for that workspace and miss, not fall back to an unscoped lookup.
func TestCheckpointRepoStaysWorkspaceScoped(t *testing.T) {
	q := &fakeCheckpointQueries{row: db.EnvCheckpoint{
		ID:          mustUUID(t, testCheckpointUUID),
		WorkspaceID: mustUUID(t, testWorkspaceUUID),
		ProjectID:   mustUUID(t, testProjectUUID),
		SaveMode:    string(SaveModePauseInPlace),
		SaveStatus:  string(EnvCheckpointSaveComplete),
	}}
	repo := NewEnvCheckpointRepository(q)

	if _, err := repo.GetCheckpoint(context.Background(), testCheckpointUUID, testWorkspaceUUID); err != nil {
		t.Fatalf("get: %v", err)
	}
	if q.getParam.WorkspaceID != mustUUID(t, testWorkspaceUUID) {
		t.Fatalf("get was not workspace scoped: %+v", q.getParam)
	}
	if _, err := repo.ListCheckpoints(context.Background(), testWorkspaceUUID, testProjectUUID); err != nil {
		t.Fatalf("list: %v", err)
	}
	if q.lsParam.WorkspaceID != mustUUID(t, testWorkspaceUUID) || q.lsParam.ProjectID != mustUUID(t, testProjectUUID) {
		t.Fatalf("list was not workspace/project scoped: %+v", q.lsParam)
	}
}

// TestCheckpointRepoRejectsMalformedIDsBeforeQuerying keeps a bad id from
// reaching the database as a zero UUID, which would silently read or write the
// wrong row rather than failing.
func TestCheckpointRepoRejectsMalformedIDsBeforeQuerying(t *testing.T) {
	q := &fakeCheckpointQueries{}
	repo := NewEnvCheckpointRepository(q)

	if _, err := repo.GetCheckpoint(context.Background(), "not-a-uuid", testWorkspaceUUID); err == nil {
		t.Fatal("a malformed checkpoint id must be rejected")
	}
	if _, err := repo.CreateCheckpoint(context.Background(), EnvCheckpointCreateInput{
		WorkspaceID: "not-a-uuid", ProjectID: testProjectUUID,
	}, EnvCheckpointSavePending, ""); err == nil {
		t.Fatal("a malformed workspace id must be rejected")
	}
	if len(q.created) != 0 {
		t.Fatalf("nothing may reach the database, got %d writes", len(q.created))
	}
}

func TestCheckpointRepoPropagatesQueryErrors(t *testing.T) {
	sentinel := errors.New("boom")
	repo := NewEnvCheckpointRepository(&fakeCheckpointQueries{err: sentinel})

	if _, err := repo.UpdateCheckpointSaveStatus(context.Background(), testCheckpointUUID, testWorkspaceUUID,
		EnvCheckpointSaveFailed, "save failed"); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the query error wrapped", err)
	}
}

// TestCheckpointRepoRoundTripsTheJSONColumns pins the decode side: env id map,
// sandbox refs and the resume trigger are all JSONB, and a checkpoint that
// cannot report its refs cannot be resumed.
func TestCheckpointRepoRoundTripsTheJSONColumns(t *testing.T) {
	q := &fakeCheckpointQueries{row: db.EnvCheckpoint{
		ID:            mustUUID(t, testCheckpointUUID),
		WorkspaceID:   mustUUID(t, testWorkspaceUUID),
		ProjectID:     mustUUID(t, testProjectUUID),
		SaveMode:      string(SaveModeSnapshot),
		SaveStatus:    string(EnvCheckpointSaveComplete),
		EnvIDMap:      []byte(`{"src":"dst"}`),
		SandboxRefs:   []byte(`[{"instance_id":"inst-1","workspace_id":"ws"}]`),
		ResumeTrigger: []byte(`{"agent_id":"a-1"}`),
		DbSnapshot:    []byte(`{"issues":[]}`),
		SaveTimeoutMs: 7000,
		SaveError:     pgtype.Text{String: "", Valid: false},
	}}
	repo := NewEnvCheckpointRepository(q)

	cp, err := repo.GetCheckpoint(context.Background(), testCheckpointUUID, testWorkspaceUUID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cp.EnvIDMap["src"] != "dst" {
		t.Fatalf("env_id_map = %v", cp.EnvIDMap)
	}
	if len(cp.SandboxRefs) != 1 || cp.SandboxRefs[0].InstanceID != "inst-1" {
		t.Fatalf("sandbox_refs = %+v", cp.SandboxRefs)
	}
	if string(cp.ResumeTrigger) != `{"agent_id":"a-1"}` {
		t.Fatalf("resume_trigger = %s", cp.ResumeTrigger)
	}
	if cp.SaveTimeoutMs != 7000 {
		t.Fatalf("save_timeout_ms = %d", cp.SaveTimeoutMs)
	}
	if cp.SaveError != "" {
		t.Fatalf("save_error = %q, want empty for a NULL column", cp.SaveError)
	}
}

// The delete must carry the workspace, not just the id. Without it a caller
// holding a checkpoint id from another workspace could delete that workspace's
// checkpoint and cascade away its savepoints and lanes.
func TestRepoDeleteIsScopedToTheWorkspace(t *testing.T) {
	q := &fakeCheckpointQueries{}
	repo := NewEnvCheckpointRepository(q)

	if err := repo.DeleteCheckpoint(context.Background(), testCheckpointUUID, testWorkspaceUUID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(q.deleted) != 1 {
		t.Fatalf("deletes = %d, want 1", len(q.deleted))
	}
	if q.deleted[0].ID != mustUUID(t, testCheckpointUUID) ||
		q.deleted[0].WorkspaceID != mustUUID(t, testWorkspaceUUID) {
		t.Fatalf("delete params = %+v, want both ids", q.deleted[0])
	}
}

func TestRepoDeleteRejectsMalformedIDs(t *testing.T) {
	repo := NewEnvCheckpointRepository(&fakeCheckpointQueries{})
	if err := repo.DeleteCheckpoint(context.Background(), "not-a-uuid", testWorkspaceUUID); err == nil {
		t.Fatal("malformed checkpoint id must be rejected before the delete")
	}
	if err := repo.DeleteCheckpoint(context.Background(), testCheckpointUUID, "not-a-uuid"); err == nil {
		t.Fatal("malformed workspace id must be rejected before the delete")
	}
}
