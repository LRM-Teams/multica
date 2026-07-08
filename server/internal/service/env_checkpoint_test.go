package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- fakes ---

type fakeCheckpointRepo struct {
	mu          sync.Mutex
	checkpoints map[string]EnvCheckpoint
	nextID      int
	createCalls []createCheckpointCall
	updateCalls []updateCheckpointCall
	listCalls   []listCall
}

type createCheckpointCall struct {
	in      EnvCheckpointCreateInput
	status  EnvCheckpointStatus
	saveErr string
}

type updateCheckpointCall struct {
	checkpointID string
	workspaceID  string
	status       EnvCheckpointStatus
	saveErr      string
}

type listCall struct {
	workspaceID string
	projectID   string
}

func newFakeCheckpointRepo() *fakeCheckpointRepo {
	return &fakeCheckpointRepo{checkpoints: map[string]EnvCheckpoint{}}
}

func (r *fakeCheckpointRepo) CreateCheckpoint(_ context.Context, in EnvCheckpointCreateInput, status EnvCheckpointStatus, saveErr string) (EnvCheckpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	id := fmt.Sprintf("cp-%d", r.nextID)
	cp := EnvCheckpoint{
		ID:            id,
		WorkspaceID:   in.WorkspaceID,
		ProjectID:     in.ProjectID,
		EventRef:      in.EventRef,
		Kind:          in.Kind,
		EnvIDMap:      in.EnvIDMap,
		SandboxRefs:   in.SandboxRefs,
		DBSnapshot:    in.DBSnapshot,
		EntropyScore:  in.EntropyScore,
		SaveTimeoutMs: int(in.SaveTimeout / time.Millisecond),
		SaveStatus:    status,
		SaveError:     saveErr,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	r.checkpoints[id] = cp
	r.createCalls = append(r.createCalls, createCheckpointCall{in, status, saveErr})
	return cp, nil
}

func (r *fakeCheckpointRepo) UpdateCheckpointSaveStatus(_ context.Context, checkpointID, workspaceID string, status EnvCheckpointStatus, saveErr string) (EnvCheckpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp, ok := r.checkpoints[checkpointID]
	if !ok || cp.WorkspaceID != workspaceID {
		return EnvCheckpoint{}, fmt.Errorf("not found")
	}
	cp.SaveStatus = status
	cp.SaveError = saveErr
	cp.UpdatedAt = time.Now()
	r.checkpoints[checkpointID] = cp
	r.updateCalls = append(r.updateCalls, updateCheckpointCall{checkpointID, workspaceID, status, saveErr})
	return cp, nil
}

func (r *fakeCheckpointRepo) GetCheckpoint(_ context.Context, checkpointID, workspaceID string) (EnvCheckpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp, ok := r.checkpoints[checkpointID]
	if !ok || cp.WorkspaceID != workspaceID {
		return EnvCheckpoint{}, fmt.Errorf("not found")
	}
	return cp, nil
}

func (r *fakeCheckpointRepo) ListCheckpoints(_ context.Context, workspaceID, projectID string) ([]EnvCheckpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []EnvCheckpoint
	for _, cp := range r.checkpoints {
		if cp.WorkspaceID == workspaceID && cp.ProjectID == projectID {
			out = append(out, cp)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	r.listCalls = append(r.listCalls, listCall{workspaceID, projectID})
	return out, nil
}

type fakeCheckpointSaver struct {
	calls            []SandboxInstanceRef
	err              error
	blockUntilCancel bool
}

func (f *fakeCheckpointSaver) Save(ctx context.Context, ref SandboxInstanceRef, _ string) error {
	f.calls = append(f.calls, ref)
	if f.err != nil {
		return f.err
	}
	if f.blockUntilCancel {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

type fakeProjectSnapshotReader struct {
	snapshot json.RawMessage
	err      error
}

func (f *fakeProjectSnapshotReader) CaptureProjectSnapshot(_ context.Context, _, _ string) (json.RawMessage, error) {
	return f.snapshot, f.err
}

// --- tests ---

func TestEnvCheckpointCreateWaitsForSynchronousSaveComplete(t *testing.T) {
	repo := newFakeCheckpointRepo()
	saver := &fakeCheckpointSaver{}
	snapshot := &fakeProjectSnapshotReader{snapshot: json.RawMessage(`{"issues":[]}`)}
	svc := NewEnvCheckpointService(repo, saver, snapshot)

	cp, err := svc.Create(context.Background(), EnvCheckpointCreateInput{
		WorkspaceID: "ws",
		ProjectID:   "proj",
		EventRef:    "evt-1",
		Kind:        "structural",
		SandboxRefs: []SandboxInstanceRef{
			{InstanceID: "inst-1", WorkspaceID: "ws"},
		},
		ActorUserID: "u",
		SaveTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if cp.SaveStatus != EnvCheckpointSaveComplete {
		t.Fatalf("status = %s, want complete", cp.SaveStatus)
	}
	if len(saver.calls) != 1 {
		t.Fatalf("want 1 save call, got %d", len(saver.calls))
	}
	if len(repo.updateCalls) != 1 {
		t.Fatalf("want 1 status update, got %d", len(repo.updateCalls))
	}
	if repo.updateCalls[0].status != EnvCheckpointSaveComplete {
		t.Fatalf("update status = %s, want complete", repo.updateCalls[0].status)
	}
}

func TestEnvCheckpointCreateRecordsTimeoutStatus(t *testing.T) {
	repo := newFakeCheckpointRepo()
	saver := &fakeCheckpointSaver{blockUntilCancel: true}
	snapshot := &fakeProjectSnapshotReader{snapshot: json.RawMessage(`{}`)}
	svc := NewEnvCheckpointService(repo, saver, snapshot)

	cp, err := svc.Create(context.Background(), EnvCheckpointCreateInput{
		WorkspaceID: "ws",
		ProjectID:   "proj",
		SandboxRefs: []SandboxInstanceRef{{InstanceID: "inst-1", WorkspaceID: "ws"}},
		ActorUserID: "u",
		SaveTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if cp.SaveStatus != EnvCheckpointSaveTimedOut {
		t.Fatalf("status = %s, want timed_out", cp.SaveStatus)
	}
}

func TestEnvCheckpointCreateRecordsSaveFailureStatus(t *testing.T) {
	repo := newFakeCheckpointRepo()
	saver := &fakeCheckpointSaver{err: fmt.Errorf("sandbox crash")}
	snapshot := &fakeProjectSnapshotReader{snapshot: json.RawMessage(`{}`)}
	svc := NewEnvCheckpointService(repo, saver, snapshot)

	cp, err := svc.Create(context.Background(), EnvCheckpointCreateInput{
		WorkspaceID: "ws",
		ProjectID:   "proj",
		SandboxRefs: []SandboxInstanceRef{{InstanceID: "inst-1", WorkspaceID: "ws"}},
		ActorUserID: "u",
		SaveTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if cp.SaveStatus != EnvCheckpointSaveFailed {
		t.Fatalf("status = %s, want failed", cp.SaveStatus)
	}
	if !strings.Contains(cp.SaveError, "sandbox crash") {
		t.Fatalf("save error should contain 'sandbox crash', got %q", cp.SaveError)
	}
}

func TestEnvCheckpointListNewestFirstAndWorkspaceScoped(t *testing.T) {
	repo := newFakeCheckpointRepo()
	base := time.Now()
	repo.checkpoints["cp-1"] = EnvCheckpoint{ID: "cp-1", WorkspaceID: "ws", ProjectID: "proj", CreatedAt: base.Add(-2 * time.Minute)}
	repo.checkpoints["cp-2"] = EnvCheckpoint{ID: "cp-2", WorkspaceID: "ws", ProjectID: "proj", CreatedAt: base}
	repo.checkpoints["cp-3"] = EnvCheckpoint{ID: "cp-3", WorkspaceID: "other-ws", ProjectID: "proj", CreatedAt: base}

	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeProjectSnapshotReader{})

	list, err := svc.List(context.Background(), "ws", "proj")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 checkpoints for ws/proj, got %d", len(list))
	}
	if list[0].ID != "cp-2" || list[1].ID != "cp-1" {
		t.Fatalf("expected newest-first [cp-2, cp-1], got [%s, %s]", list[0].ID, list[1].ID)
	}
}

func TestEnvCheckpointStoresInlineDBSnapshot(t *testing.T) {
	repo := newFakeCheckpointRepo()
	snapshotJSON := json.RawMessage(`{"issues":[{"id":"i1"}],"sessions":[]}`)
	snapshot := &fakeProjectSnapshotReader{snapshot: snapshotJSON}
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, snapshot)

	cp, err := svc.Create(context.Background(), EnvCheckpointCreateInput{
		WorkspaceID: "ws",
		ProjectID:   "proj",
		SandboxRefs: []SandboxInstanceRef{{InstanceID: "inst-1", WorkspaceID: "ws"}},
		ActorUserID: "u",
		SaveTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if string(cp.DBSnapshot) != string(snapshotJSON) {
		t.Fatalf("snapshot mismatch: got %s, want %s", cp.DBSnapshot, snapshotJSON)
	}
	if len(repo.createCalls) != 1 {
		t.Fatalf("want 1 create call, got %d", len(repo.createCalls))
	}
	if string(repo.createCalls[0].in.DBSnapshot) != string(snapshotJSON) {
		t.Fatalf("create call snapshot mismatch: got %s", repo.createCalls[0].in.DBSnapshot)
	}
}
