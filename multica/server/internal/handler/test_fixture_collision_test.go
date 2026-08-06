package handler

import "testing"

// Task #86 (task #78 audit follow-up): four more test fixture helpers built
// a unique-constrained column purely from t.Name() (plus a fixed prefix),
// with no per-call entropy — the same shape insertSkillPromoteWorkspaceMember
// had before task #78/#1807. A single successful test run never notices,
// because t.Cleanup deletes the row before another caller can collide with
// it. It only surfaces when a row from an interrupted run (panic, kill,
// crash) survives past cleanup and a later run with the identical t.Name()
// tries to insert the same deterministic value again.
//
// These tests reproduce that same collision deterministically, in-process,
// without needing an actual crash: call the fixture helper twice back to
// back with identical arguments. Since t.Name() is constant within one test
// function, two calls hit the exact same deterministic key the old code
// built — exactly what a leftover row from a prior interrupted run would
// also hit. Confirm-broken: before the fix, the second call fails with a
// unique constraint violation; after the fix (randomID() suffix), both
// calls succeed and produce two distinct rows.

func TestInsertHandlerTestSkill_RepeatedCallsDoNotCollide(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	first, _ := insertHandlerTestSkill(t, "collision-check", "first body")
	second, _ := insertHandlerTestSkill(t, "collision-check", "second body")
	if first == second {
		t.Fatalf("two calls with the same namePrefix produced the same skill id %q", first)
	}
}

func TestInsertHandlerTestSkillInForeignWorkspace_RepeatedCallsDoNotCollide(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	first := insertHandlerTestSkillInForeignWorkspace(t, "collision-check", "first body")
	second := insertHandlerTestSkillInForeignWorkspace(t, "collision-check", "second body")
	if first == second {
		t.Fatalf("two calls with the same namePrefix produced the same skill id %q", first)
	}
}

func TestCreateHandlerTestMember_RepeatedCallsWithSameRoleDoNotCollide(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	first := createHandlerTestMember(t, "member")
	second := createHandlerTestMember(t, "member")
	if first == second {
		t.Fatalf("two calls with the same role produced the same user id %q", first)
	}
}

func TestInsertAgentSurfaceBindDenyChannel_RepeatedCallsDoNotCollide(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	first := insertAgentSurfaceBindDenyChannel(t, testWorkspaceID, testUserID)
	second := insertAgentSurfaceBindDenyChannel(t, testWorkspaceID, testUserID)
	if first == second {
		t.Fatalf("two calls produced the same channel id %q", first)
	}
}
