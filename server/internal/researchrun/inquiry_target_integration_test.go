package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTaskInquiryTargetsAreScopedTypedAndOrdered(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fixture := seedResearchRunFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fixture)
	store := NewPostgresStore(pool)
	run, _, err := store.CreateRun(ctx, StartInput{WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Bind Inquiry targets", Title: "Inquiry targets", DepthTier: "standard", Language: "English"}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID, run.WorkspaceID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	var questionID string
	if err = pool.QueryRow(ctx, `SELECT id::text FROM research_question WHERE session_id=$1::uuid ORDER BY created_at,id LIMIT 1`, run.SessionID).Scan(&questionID); err != nil {
		t.Fatal(err)
	}
	hypothesisID := uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_hypothesis(id,workspace_id,session_id,question_id,statement,client_key) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'Target hypothesis','legacy:'||$1::text)`, hypothesisID, fixture.workspaceID, run.SessionID, questionID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	backfillIntegrationArtifactPassport(t, ctx, tx, fixture.workspaceID, run.SessionID, hypothesisID, string(ArtifactKindHypothesis), nil, nil)
	if err = bindTaskInquiryTargetsTx(ctx, tx, fixture.workspaceID, run.SessionID, tasks[0].ID, []TaskInquiryTarget{{Kind: InquiryKindQuestion, EntityID: questionID}, {Kind: InquiryKindHypothesis, EntityID: hypothesisID}}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT target_kind,target_entity_id::text,ordinal FROM research_task_inquiry_target WHERE task_id=$1::uuid ORDER BY ordinal`, tasks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := []TaskInquiryTarget{{Kind: InquiryKindQuestion, EntityID: questionID}, {Kind: InquiryKindHypothesis, EntityID: hypothesisID}}
	index := 0
	for rows.Next() {
		var kind InquiryEntityKind
		var id string
		var ordinal int
		if err = rows.Scan(&kind, &id, &ordinal); err != nil {
			t.Fatal(err)
		}
		if index >= len(want) || kind != want[index].Kind || id != want[index].EntityID || ordinal != index {
			t.Fatalf("row %d=%s:%s ordinal=%d", index, kind, id, ordinal)
		}
		index++
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if index != len(want) {
		t.Fatalf("rows=%d want=%d", index, len(want))
	}

	for name, target := range map[string]TaskInquiryTarget{"missing": {Kind: InquiryKindClaim, EntityID: uuid.NewString()}, "dispute": {Kind: InquiryKindDispute, EntityID: uuid.NewString()}} {
		t.Run(name, func(t *testing.T) {
			tx, beginErr := pool.Begin(ctx)
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			defer tx.Rollback(ctx)
			err := bindTaskInquiryTargetsTx(ctx, tx, fixture.workspaceID, run.SessionID, tasks[0].ID, []TaskInquiryTarget{target})
			if err == nil {
				t.Fatal("invalid target accepted")
			}
		})
	}
	if _, err = pool.Exec(ctx, `INSERT INTO research_task_inquiry_target(workspace_id,session_id,task_id,target_kind,target_entity_id,ordinal)
		VALUES($1::uuid,$2::uuid,$3::uuid,'dispute',$4::uuid,99)`, fixture.workspaceID, run.SessionID, tasks[0].ID, uuid.NewString()); err == nil {
		t.Fatal("database accepted a dispute target before Chapter H")
	}
}
