package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

type fixedSnapshotResearchRun struct {
	snap researchrun.RunSnapshot
}

func (f *fixedSnapshotResearchRun) Create(context.Context, researchrun.StartInput) (researchrun.Run, error) {
	panic("unexpected Create")
}

func (f *fixedSnapshotResearchRun) Snapshot(_ context.Context, sessionID, _ string) (researchrun.RunSnapshot, error) {
	snap := f.snap
	snap.Run.SessionID = sessionID
	return snap, nil
}

func (f *fixedSnapshotResearchRun) SnapshotForAttempt(_ context.Context, sessionID, _, _ string) (researchrun.RunSnapshot, error) {
	return f.Snapshot(context.Background(), sessionID, "")
}

func (f *fixedSnapshotResearchRun) ListFleetMembers(context.Context, string, string) ([]researchrun.FleetMember, error) {
	return nil, nil
}

func (f *fixedSnapshotResearchRun) Pause(context.Context, string, string, string) (researchrun.Run, error) {
	panic("unexpected Pause")
}

func (f *fixedSnapshotResearchRun) Resume(context.Context, string, string, string) (researchrun.Run, error) {
	panic("unexpected Resume")
}

func (f *fixedSnapshotResearchRun) Cancel(context.Context, string, string, string, string) (researchrun.Run, error) {
	panic("unexpected Cancel")
}

func (f *fixedSnapshotResearchRun) Archive(context.Context, string, string, string, string) (researchrun.Run, error) {
	panic("unexpected Archive")
}

func (f *fixedSnapshotResearchRun) Confirm(context.Context, string, string, string) (researchrun.Run, error) {
	panic("unexpected Confirm")
}

func (f *fixedSnapshotResearchRun) Steer(context.Context, researchrun.SteerInput) (researchrun.Run, error) {
	panic("unexpected Steer")
}

func (f *fixedSnapshotResearchRun) NodeCommand(context.Context, researchrun.NodeCommandInput) (researchrun.NodeCommandOutcome, error) {
	panic("unexpected NodeCommand")
}

func (f *fixedSnapshotResearchRun) SubmitResult(context.Context, string, string, string, string, string, string, json.RawMessage) (researchrun.AcceptResultOutcome, error) {
	panic("unexpected SubmitResult")
}

func (f *fixedSnapshotResearchRun) ReconcileDue(context.Context, int) (int, error) {
	panic("unexpected ReconcileDue")
}

var _ researchrun.ResearchRun = (*fixedSnapshotResearchRun)(nil)

func countAuditGraphNodes(t *testing.T, sessionID pgtype.UUID) int64 {
	t.Helper()
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	var count int64
	if err := testPool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM research_graph_node
		WHERE workspace_id = $1 AND session_id = $2
	`, parseUUID(testWorkspaceID), sessionID).Scan(&count); err != nil {
		t.Fatalf("count audit graph nodes: %v", err)
	}
	return count
}

// Regression: durable run ledger projection must populate typed GET even when the
// audit event-log graph table is empty (the D5 white-screen incident).
func TestGetResearchGraphTypedUsesRunV2WhenAuditGraphEmpty(t *testing.T) {
	sessionID := seedInitializedResearchSessionForSnapshotTest(t)
	if count := countAuditGraphNodes(t, sessionID); count != 0 {
		t.Fatalf("audit graph rows=%d, want 0 for empty-table regression", count)
	}

	snap := fixtureSevenQuestionSession()
	useResearchRunEngine(t, &fixedSnapshotResearchRun{snap: snap})

	code, typed := doGetTyped(t, sessionID)
	if code != http.StatusOK {
		t.Fatalf("typed get status=%d", code)
	}
	canvasNodes, _ := projectRunV2Graph(snap)
	canvasNodes[0].SessionID = uuidToString(sessionID)
	if len(typed.Nodes) != len(canvasNodes) {
		t.Fatalf("typed nodes=%d canvas nodes=%d", len(typed.Nodes), len(canvasNodes))
	}
	if typed.GraphVersion != snap.Run.StateVersion {
		t.Fatalf("graph_version=%d want state_version=%d", typed.GraphVersion, snap.Run.StateVersion)
	}
	for _, node := range typed.Nodes {
		if node.Level == "" || !validResearchNodeLevel(node.Level) {
			t.Fatalf("node %q has invalid level %q", node.ID, node.Level)
		}
	}
}

func TestGetResearchGraphTypedRunV2MatchesSnapshotProjection(t *testing.T) {
	sessionID := seedInitializedResearchSessionForSnapshotTest(t)
	snap := fixtureSevenQuestionSession()
	snap.Run.SessionID = uuidToString(sessionID)
	useResearchRunEngine(t, &fixedSnapshotResearchRun{snap: snap})

	snapshotNodes, _ := projectRunV2Graph(snap)
	code, typed := doGetTyped(t, sessionID)
	if code != http.StatusOK {
		t.Fatalf("typed get status=%d", code)
	}
	if len(typed.Nodes) != len(snapshotNodes) {
		t.Fatalf("typed/snapshot node count mismatch: %d vs %d", len(typed.Nodes), len(snapshotNodes))
	}
	byID := map[string]ResearchGraphTypedNodeResp{}
	for _, n := range typed.Nodes {
		byID[n.ID] = n
	}
	for _, canvas := range snapshotNodes {
		got, ok := byID[canvas.ID]
		if !ok {
			t.Fatalf("typed graph missing canvas node %q", canvas.ID)
		}
		if got.NodeType != canvas.NodeType || got.Title != canvas.Title {
			t.Fatalf("typed node %q drifted from canvas projection", canvas.ID)
		}
	}
}
