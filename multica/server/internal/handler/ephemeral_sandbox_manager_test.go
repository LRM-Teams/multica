package handler

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type fakeRetrySandboxLifecycle struct {
	oldRef      service.SandboxInstanceRef
	lookupErr   error
	createRef   service.SandboxInstanceRef
	createErr   error
	createInput service.CreateSandboxInstanceInput
	createActor string
	deleteRef   service.SandboxInstanceRef
	deleteActor string
	deleteErr   error
}

func (f *fakeRetrySandboxLifecycle) GetSandboxInstanceRef(context.Context, string, string) (service.SandboxInstanceRef, error) {
	return f.oldRef, f.lookupErr
}

func (f *fakeRetrySandboxLifecycle) CreateSandboxInstance(_ context.Context, in service.CreateSandboxInstanceInput, actor string) (service.SandboxInstanceRef, error) {
	f.createInput = in
	f.createActor = actor
	return f.createRef, f.createErr
}

func (f *fakeRetrySandboxLifecycle) DeleteSandboxInstance(_ context.Context, ref service.SandboxInstanceRef, actor string) error {
	f.deleteRef = ref
	f.deleteActor = actor
	return f.deleteErr
}

func TestEphemeralSandboxManagerPrepareRetryCreatesNewRuntimeAndSandbox(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, _ := setupBoundRuntimeAgent(t, "pi")
	lifecycle := &fakeRetrySandboxLifecycle{
		oldRef:    service.SandboxInstanceRef{InstanceID: "old", WorkspaceID: testWorkspaceID, Template: "old-template", CreatorUserID: testUserID},
		createRef: service.SandboxInstanceRef{InstanceID: "new", WorkspaceID: testWorkspaceID, Template: "old-template"},
	}
	manager := newEphemeralSandboxManager(testHandler, lifecycle)
	parent := db.AgentInboxEvent{
		AgentID: parseUUID(agentID),
		Context: mergeEphemeralSandboxContext(nil, "old", testUserID),
	}

	resources, err := manager.PrepareRetry(ctx, parent)
	if err != nil {
		t.Fatalf("PrepareRetry: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_runtime WHERE id = $1`, resources.RuntimeID) })
	if resources.RuntimeID == (pgtype.UUID{}) {
		t.Fatal("replacement runtime id is empty")
	}
	if lifecycle.createInput.Template != "old-template" || !lifecycle.createInput.DaemonEnabled {
		t.Fatalf("create input = %+v", lifecycle.createInput)
	}
	daemonID := lifecycle.createInput.RuntimeEnv["MULTICA_DAEMON_ID"]
	if daemonID == "" {
		t.Fatal("MULTICA_DAEMON_ID is empty")
	}
	var provider string
	if err := testPool.QueryRow(ctx, `SELECT provider FROM agent_runtime WHERE id = $1`, resources.RuntimeID).Scan(&provider); err != nil {
		t.Fatalf("read replacement runtime: %v", err)
	}
	if provider != "pi" {
		t.Fatalf("provider = %q, want pi", provider)
	}
	marker, ok := service.ExtractEphemeralSandbox(resources.Context)
	if !ok || marker.SandboxInstanceID != "new" || marker.ActorUserID != testUserID {
		t.Fatalf("replacement marker = %+v, ok=%v", marker, ok)
	}
}

func TestEphemeralSandboxManagerPrepareRetryCompensatesRuntimeOnCreateFailure(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, _ := setupBoundRuntimeAgent(t, "pi")
	lifecycle := &fakeRetrySandboxLifecycle{
		oldRef:    service.SandboxInstanceRef{InstanceID: "old", WorkspaceID: testWorkspaceID, Template: "old-template", CreatorUserID: testUserID},
		createErr: errors.New("create failed"),
	}
	manager := newEphemeralSandboxManager(testHandler, lifecycle)
	_, err := manager.PrepareRetry(ctx, db.AgentInboxEvent{
		AgentID: parseUUID(agentID),
		Context: mergeEphemeralSandboxContext(nil, "old", testUserID),
	})
	if err == nil {
		t.Fatal("expected create failure")
	}
	daemonID := lifecycle.createInput.RuntimeEnv["MULTICA_DAEMON_ID"]
	if daemonID == "" {
		t.Fatal("replacement daemon id was not allocated")
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_runtime WHERE daemon_id = $1`, daemonID) })
	var runtimeID string
	err = testPool.QueryRow(ctx, `SELECT id FROM agent_runtime WHERE daemon_id = $1`, daemonID).Scan(&runtimeID)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("replacement runtime was not deleted: id=%q err=%v", runtimeID, err)
	}
}

func TestEphemeralSandboxManagerCleanupUsesMarkerActor(t *testing.T) {
	testEphemeralSandboxManagerCleanupActor(t, testUserID, testUserID)
}

func TestEphemeralSandboxManagerCleanupFallsBackToSandboxCreator(t *testing.T) {
	testEphemeralSandboxManagerCleanupActor(t, "", testUserID)
}

func testEphemeralSandboxManagerCleanupActor(t *testing.T, markerActor, wantActor string) {
	t.Helper()
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, _ := setupBoundRuntimeAgent(t, "pi")
	runtimeID, _, err := (&envDispatchDepsAdapter{h: testHandler}).PrecreateAgentRuntime(ctx, testWorkspaceID, testUserID, agentID)
	if err != nil {
		t.Fatalf("precreate runtime: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_runtime WHERE id = $1`, runtimeID) })
	lifecycle := &fakeRetrySandboxLifecycle{
		oldRef: service.SandboxInstanceRef{InstanceID: "old", WorkspaceID: testWorkspaceID, CreatorUserID: testUserID},
	}
	manager := newEphemeralSandboxManager(testHandler, lifecycle)
	contextJSON := mergeEphemeralSandboxContext(nil, "old", markerActor)
	err = manager.Cleanup(ctx, db.AgentInboxEvent{
		ID:        util.MustParseUUID("aaaaaaaa-0000-0000-0000-000000000001"),
		AgentID:   parseUUID(agentID),
		RuntimeID: parseUUID(runtimeID),
		Context:   contextJSON,
	})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if lifecycle.deleteActor != wantActor {
		t.Fatalf("delete actor = %q, want %q", lifecycle.deleteActor, wantActor)
	}
}

func TestEphemeralSandboxManagerCleanupPropagatesTransientLookupError(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID, _ := setupBoundRuntimeAgent(t, "pi")
	lookupErr := errors.New("lookup temporarily unavailable")
	manager := newEphemeralSandboxManager(testHandler, &fakeRetrySandboxLifecycle{lookupErr: lookupErr})
	err := manager.Cleanup(context.Background(), db.AgentInboxEvent{
		AgentID: parseUUID(agentID),
		Context: mergeEphemeralSandboxContext(nil, "old", testUserID),
	})
	if !errors.Is(err, lookupErr) || !strings.Contains(err.Error(), "lookup") {
		t.Fatalf("Cleanup error = %v, want transient lookup error", err)
	}
}

func TestSandboxDeleteJobIsIdempotent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	q := testHandler.Queries
	node, err := q.CreateSandboxNode(ctx, db.CreateSandboxNodeParams{
		NodeKey:        "delete-idempotency-" + uuid.NewString(),
		Name:           "delete idempotency",
		OwnerUserID:    parseUUID(testUserID),
		Capabilities:   []byte(`{}`),
		MaxConcurrency: 1,
		Metadata:       []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create sandbox node: %v", err)
	}
	instance, err := q.CreateSandboxInstance(ctx, db.CreateSandboxInstanceParams{
		WorkspaceID:   parseUUID(testWorkspaceID),
		CreatorUserID: parseUUID(testUserID),
		NodeID:        node.ID,
		Status:        "running",
		Template:      "default",
		Limits:        []byte(`{}`),
		Metadata:      []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create sandbox instance: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM sandbox_instance WHERE id = $1`, instance.ID)
		testPool.Exec(ctx, `DELETE FROM sandbox_node WHERE id = $1`, node.ID)
	})
	adapter := &envSandboxLifecycleDepsAdapter{h: testHandler}
	first, err := adapter.EnqueueSandboxJob(
		ctx, testWorkspaceID, testUserID,
		util.UUIDToString(node.ID), util.UUIDToString(instance.ID),
		"delete", []byte(`{}`),
	)
	if err != nil {
		t.Fatalf("first delete job: %v", err)
	}
	second, err := adapter.EnqueueSandboxJob(
		ctx, testWorkspaceID, testUserID,
		util.UUIDToString(node.ID), util.UUIDToString(instance.ID),
		"delete", []byte(`{}`),
	)
	if err != nil {
		t.Fatalf("second delete job: %v", err)
	}
	if first.JobID != second.JobID {
		t.Fatalf("delete job ids differ: first=%s second=%s", first.JobID, second.JobID)
	}
	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM sandbox_job
		WHERE instance_id = $1 AND type = 'delete'
		  AND status IN ('queued', 'dispatched', 'running')
	`, instance.ID).Scan(&count); err != nil {
		t.Fatalf("count active delete jobs: %v", err)
	}
	if count != 1 {
		t.Fatalf("active delete jobs = %d, want 1", count)
	}
}
