package researchrun

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func seedV6InsightArtifactVersion(t *testing.T, run *transactionRecoveryRun, suffix string) string {
	t.Helper()
	insightID, artifactVersionID, insightVersionID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	hash := "sha256:" + strings.Repeat(suffix, 64)
	tx, err := run.pool.Begin(run.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(run.ctx)
	if _, err = tx.Exec(run.ctx, `INSERT INTO research_insight(id,workspace_id,session_id,title,summary,status,importance,level)
		VALUES($1::uuid,$2::uuid,$3::uuid,'test','test','accepted',0.5,2)`, insightID, run.fixture.workspaceID, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(run.ctx, `INSERT INTO research_artifact_passport(id,workspace_id,session_id,entity_kind,current_version,lifecycle_status,provenance_completeness)
		VALUES($1::uuid,$2::uuid,$3::uuid,'insight',NULL,'accepted','complete')`, insightID, run.fixture.workspaceID, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(run.ctx, `INSERT INTO research_artifact_version(id,workspace_id,session_id,artifact_id,version,schema_name,schema_version,content_hash,access_level,hash_origin)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'research_insight_version','6',$5,'raw','production')`, artifactVersionID, run.fixture.workspaceID, run.fixture.sessionID, insightID, hash); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(run.ctx, `UPDATE research_artifact_passport SET current_version=1 WHERE id=$1::uuid`, insightID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(run.ctx, `INSERT INTO research_insight_version(id,workspace_id,session_id,insight_id,revision,artifact_version_id,tier,catalog_summary,brief_summary,objective,conclusion,content,status,content_hash)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,$5::uuid,'M','test','test','test','test','test','accepted',$6)`, insightVersionID, run.fixture.workspaceID, run.fixture.sessionID, insightID, artifactVersionID, hash); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(run.ctx); err != nil {
		t.Fatal(err)
	}
	return artifactVersionID
}

func TestV6AbsorptionSingleSuccessorRace(t *testing.T) {
	run := newTransactionRecoveryRun(t, "V6 absorption race")
	input := seedV6InsightArtifactVersion(t, run, "1")
	left := seedV6InsightArtifactVersion(t, run, "2")
	right := seedV6InsightArtifactVersion(t, run, "3")
	leftInsightVersion, rightInsightVersion := uuid.NewString(), uuid.NewString()
	if err := run.pool.QueryRow(run.ctx, `SELECT id::text FROM research_insight_version WHERE artifact_version_id=$1::uuid`, left).Scan(&leftInsightVersion); err != nil {
		t.Fatal(err)
	}
	if err := run.pool.QueryRow(run.ctx, `SELECT id::text FROM research_insight_version WHERE artifact_version_id=$1::uuid`, right).Scan(&rightInsightVersion); err != nil {
		t.Fatal(err)
	}
	leftRound, rightRound := uuid.NewString(), uuid.NewString()
	if _, err := run.pool.Exec(run.ctx, `INSERT INTO research_integration_round(id,workspace_id,session_id,trigger_kind,input_event_sequence,input_state_hash,goal_version,plan_version,status)
		VALUES($1::uuid,$3::uuid,$4::uuid,'manual',100,$5,1,1,'accepted'),($2::uuid,$3::uuid,$4::uuid,'manual',101,$6,1,1,'accepted')`,
		leftRound, rightRound, run.fixture.workspaceID, run.fixture.sessionID, "sha256:"+strings.Repeat("4", 64), "sha256:"+strings.Repeat("5", 64)); err != nil {
		t.Fatal(err)
	}
	tx1, err := run.pool.Begin(run.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx1.Rollback(run.ctx)
	insert := `INSERT INTO research_node_absorption(workspace_id,session_id,input_artifact_version_id,successor_insight_version_id,integration_round_id,relation)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'assimilation')`
	if _, err = tx1.Exec(run.ctx, insert, run.fixture.workspaceID, run.fixture.sessionID, input, leftInsightVersion, leftRound); err != nil {
		t.Fatal(err)
	}
	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		tx2, e := run.pool.Begin(context.Background())
		if e == nil {
			_, e = tx2.Exec(context.Background(), insert, run.fixture.workspaceID, run.fixture.sessionID, input, rightInsightVersion, rightRound)
			if e == nil {
				e = tx2.Commit(context.Background())
			} else {
				_ = tx2.Rollback(context.Background())
			}
		}
		done <- result{e}
	}()
	if err = tx1.Commit(run.ctx); err != nil {
		t.Fatal(err)
	}
	if second := <-done; second.err == nil {
		t.Fatal("both successor writes committed")
	}
	var count int
	if err = run.pool.QueryRow(run.ctx, `SELECT count(*)::int FROM research_node_absorption WHERE session_id=$1::uuid AND input_artifact_version_id=$2::uuid`, run.fixture.sessionID, input).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}
