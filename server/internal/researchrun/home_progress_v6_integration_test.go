package researchrun

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/db"
)

func TestV6HomeProgressExcludesDirectorControlWork(t *testing.T) {
	run := newTransactionRecoveryRun(t, "V6 home progress excludes Director control work")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.pool.Exec(run.ctx, `DELETE FROM research_work_item WHERE session_id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.pool.Exec(run.ctx, `INSERT INTO research_work_item(
		id,workspace_id,session_id,kind,status,goal_version,idempotency_key,payload_schema_id,state_version,reason,max_attempts,attempt_count
	) VALUES
		($1::uuid,$2::uuid,$3::uuid,'research','succeeded',1,$4,'research.finding.v1',1,'Completed research',3,1),
		($5::uuid,$2::uuid,$3::uuid,'director','failed',1,$6,'research.director_action.v1',1,'Historical Director retry',3,3)`,
		uuid.NewString(), run.fixture.workspaceID, run.fixture.sessionID, "research:"+uuid.NewString(),
		uuid.NewString(), "director:"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	workspaceUUID := uuid.MustParse(run.fixture.workspaceID)
	rows, err := db.New(run.pool).ListResearchSessionProgress(run.ctx, pgtype.UUID{Bytes: workspaceUUID, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if uuid.UUID(row.SessionID.Bytes).String() != run.fixture.sessionID {
			continue
		}
		if row.TaskTotal != 1 || row.TaskCompleted != 1 || row.TaskBlocked != 0 {
			t.Fatalf("progress total=%d completed=%d blocked=%d, want 1/1/0", row.TaskTotal, row.TaskCompleted, row.TaskBlocked)
		}
		if row.AttentionKind == "blocked_tasks" || row.Recoverable {
			t.Fatalf("historical Director failure surfaced as attention: kind=%q recoverable=%v", row.AttentionKind, row.Recoverable)
		}
		return
	}
	t.Fatal("research session progress missing")
}
