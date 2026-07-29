// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// --- in-flight resolver ---

type fakeInFlightQueries struct {
	rows    []db.AgentInboxEvent
	args    []pgtype.UUID
	listErr error
}

func (f *fakeInFlightQueries) ListInFlightTasksForProject(_ context.Context, projectID pgtype.UUID) ([]db.AgentInboxEvent, error) {
	f.args = append(f.args, projectID)
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.rows, nil
}

func inboxEvent(mutate func(*db.AgentInboxEvent)) db.AgentInboxEvent {
	ev := db.AgentInboxEvent{
		ID:        mustUUIDValue(testTaskUUID),
		AgentID:   mustUUIDValue(testAgentUUID),
		RuntimeID: mustUUIDValue(testRuntimeUUID),
		IssueID:   mustUUIDValue(testIssueUUID),
	}
	if mutate != nil {
		mutate(&ev)
	}
	return ev
}

func TestInFlightResolverMapsAnIssueTask(t *testing.T) {
	q := &fakeInFlightQueries{rows: []db.AgentInboxEvent{inboxEvent(nil)}}

	got, err := NewInFlightTaskResolver(q).ListInFlightTasksForProject(
		context.Background(), testWorkspaceUUID, testProjectUUID)
	if err != nil {
		t.Fatalf("list in-flight: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("triggers = %d, want 1", len(got))
	}
	want := ResumeTrigger{
		TaskID: testTaskUUID, RuntimeID: testRuntimeUUID, AgentID: testAgentUUID,
		IssueID: testIssueUUID, ProjectID: testProjectUUID, Kind: "issue",
	}
	if got[0] != want {
		t.Fatalf("trigger = %+v, want %+v", got[0], want)
	}
}

func TestInFlightResolverMapsAChatTask(t *testing.T) {
	q := &fakeInFlightQueries{rows: []db.AgentInboxEvent{inboxEvent(func(ev *db.AgentInboxEvent) {
		ev.IssueID = pgtype.UUID{}
		ev.ChatSessionID = mustUUIDValue(testSessionUUID)
	})}}

	got, err := NewInFlightTaskResolver(q).ListInFlightTasksForProject(
		context.Background(), testWorkspaceUUID, testProjectUUID)
	if err != nil {
		t.Fatalf("list in-flight: %v", err)
	}
	if len(got) != 1 || got[0].Kind != "chat" || got[0].ChatSessionID != testSessionUUID {
		t.Fatalf("trigger = %+v, want a chat trigger on the session", got)
	}
	if got[0].IssueID != "" {
		t.Fatalf("a chat trigger must carry no issue, got %q", got[0].IssueID)
	}
}

// TestInFlightResolverSkipsTasksThatCannotBeTriggered is the silent-failure
// guard. Continuation resets the task by (task id, runtime id); a row missing
// either can never be resumed, so capturing it would store a trigger that fails
// at resume time -- long after the operator could do anything about it.
func TestInFlightResolverSkipsTasksThatCannotBeTriggered(t *testing.T) {
	q := &fakeInFlightQueries{rows: []db.AgentInboxEvent{
		inboxEvent(func(ev *db.AgentInboxEvent) { ev.RuntimeID = pgtype.UUID{} }),
		inboxEvent(func(ev *db.AgentInboxEvent) { ev.ID = pgtype.UUID{} }),
		inboxEvent(nil),
	}}

	got, err := NewInFlightTaskResolver(q).ListInFlightTasksForProject(
		context.Background(), testWorkspaceUUID, testProjectUUID)
	if err != nil {
		t.Fatalf("list in-flight: %v", err)
	}
	if len(got) != 1 || got[0].TaskID != testTaskUUID {
		t.Fatalf("only the triggerable task may be captured, got %+v", got)
	}
}

// A row bound to neither an issue nor a chat session has no conversation for the
// resumed agent to continue, so it is not a usable trigger either.
func TestInFlightResolverSkipsATaskWithNoConversation(t *testing.T) {
	q := &fakeInFlightQueries{rows: []db.AgentInboxEvent{
		inboxEvent(func(ev *db.AgentInboxEvent) { ev.IssueID = pgtype.UUID{} }),
	}}

	got, err := NewInFlightTaskResolver(q).ListInFlightTasksForProject(
		context.Background(), testWorkspaceUUID, testProjectUUID)
	if err != nil {
		t.Fatalf("list in-flight: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("triggers = %+v, want none", got)
	}
}

func TestInFlightResolverPassesTheProjectAndRejectsAMalformedOne(t *testing.T) {
	q := &fakeInFlightQueries{}
	r := NewInFlightTaskResolver(q)

	if _, err := r.ListInFlightTasksForProject(context.Background(), testWorkspaceUUID, testProjectUUID); err != nil {
		t.Fatalf("list in-flight: %v", err)
	}
	if len(q.args) != 1 || q.args[0] != mustUUIDValue(testProjectUUID) {
		t.Fatalf("query args = %+v", q.args)
	}
	if _, err := r.ListInFlightTasksForProject(context.Background(), testWorkspaceUUID, "not-a-uuid"); err == nil {
		t.Fatal("a malformed project id must be rejected")
	}
	if len(q.args) != 1 {
		t.Fatalf("nothing may be queried for a malformed id, args = %+v", q.args)
	}
}

func TestInFlightResolverPropagatesQueryErrors(t *testing.T) {
	sentinel := errors.New("boom")
	if _, err := NewInFlightTaskResolver(&fakeInFlightQueries{listErr: sentinel}).
		ListInFlightTasksForProject(context.Background(), testWorkspaceUUID, testProjectUUID); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the query error", err)
	}
}

// --- saver / resumer ---

type fakeLifecycleJobs struct {
	saved   []SandboxInstanceRef
	resumed []SandboxInstanceRef

	saveErr   error
	resumeErr error
}

func (f *fakeLifecycleJobs) Save(_ context.Context, ref SandboxInstanceRef, _ string) (SandboxLifecycleJobResult, error) {
	f.saved = append(f.saved, ref)
	if f.saveErr != nil {
		return SandboxLifecycleJobResult{}, f.saveErr
	}
	return SandboxLifecycleJobResult{JobID: "job-1", InstanceID: ref.InstanceID, JobType: "save"}, nil
}

func (f *fakeLifecycleJobs) Resume(_ context.Context, ref SandboxInstanceRef, _ string) (SandboxLifecycleJobResult, error) {
	f.resumed = append(f.resumed, ref)
	if f.resumeErr != nil {
		return SandboxLifecycleJobResult{}, f.resumeErr
	}
	return SandboxLifecycleJobResult{JobID: "job-2", InstanceID: ref.InstanceID, JobType: "resume"}, nil
}

func TestSaverForwardsTheRefAndPropagatesFailure(t *testing.T) {
	ref := SandboxInstanceRef{WorkspaceID: testWorkspaceUUID, InstanceID: testInstanceUUID}
	jobs := &fakeLifecycleJobs{}
	if err := NewSandboxInstanceSaver(jobs).Save(context.Background(), ref, testUserUUID); err != nil {
		t.Fatalf("save: %v", err)
	}
	if len(jobs.saved) != 1 || jobs.saved[0].InstanceID != testInstanceUUID {
		t.Fatalf("saved = %+v", jobs.saved)
	}

	// A dropped save error would record the checkpoint as complete while the
	// instance is still running, so the checkpoint would claim state it never
	// captured.
	sentinel := errors.New("boom")
	if err := NewSandboxInstanceSaver(&fakeLifecycleJobs{saveErr: sentinel}).
		Save(context.Background(), ref, testUserUUID); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the save error", err)
	}
}

func TestResumerForwardsTheRefAndPropagatesFailure(t *testing.T) {
	ref := SandboxInstanceRef{WorkspaceID: testWorkspaceUUID, InstanceID: testInstanceUUID}
	jobs := &fakeLifecycleJobs{}
	if err := NewSandboxInstanceResumer(jobs).Resume(context.Background(), ref, testUserUUID); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(jobs.resumed) != 1 || jobs.resumed[0].InstanceID != testInstanceUUID {
		t.Fatalf("resumed = %+v", jobs.resumed)
	}

	sentinel := errors.New("boom")
	if err := NewSandboxInstanceResumer(&fakeLifecycleJobs{resumeErr: sentinel}).
		Resume(context.Background(), ref, testUserUUID); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the resume error", err)
	}
}
