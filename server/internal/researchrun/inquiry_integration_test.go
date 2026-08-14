package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestInquiryDatabaseGuardsRejectInvalidTransitionsEndpointsAndCycles(t *testing.T) {
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
	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID,
		Goal: "Guard Inquiry Graph", Title: "Inquiry guards", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	var questionID string
	if err = pool.QueryRow(ctx, `
		SELECT id::text FROM research_question
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid ORDER BY created_at,id LIMIT 1
	`, fixture.workspaceID, run.SessionID).Scan(&questionID); err != nil {
		t.Fatal(err)
	}
	hypothesisID := uuid.NewString()
	execIntegrationDomainInsert(t, ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, insertErr := tx.Exec(ctx, `
			INSERT INTO research_hypothesis (id,workspace_id,session_id,question_id,statement,client_key)
			VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,'A falsifiable hypothesis','legacy:'||$1::text)
		`, hypothesisID, fixture.workspaceID, run.SessionID, questionID); insertErr != nil {
			return insertErr
		}
		backfillIntegrationArtifactPassport(t, ctx, tx, fixture.workspaceID, run.SessionID, hypothesisID, string(ArtifactKindHypothesis), nil, nil)
		return nil
	})
	if _, err = pool.Exec(ctx, `
		UPDATE research_hypothesis SET status='supported'
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
	`, fixture.workspaceID, run.SessionID, hypothesisID); err == nil {
		t.Fatal("proposed hypothesis jumped directly to supported")
	}
	edgeID := uuid.NewString()
	execIntegrationDomainInsert(t, ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, insertErr := tx.Exec(ctx, `
			INSERT INTO research_inquiry_edge (
			  id,workspace_id,session_id,from_kind,from_entity_id,to_kind,to_entity_id,relation,client_key
			) VALUES ($1::uuid,$2::uuid,$3::uuid,'question',$4::uuid,'hypothesis',$5::uuid,'decomposes','legacy:'||$1::text)
		`, edgeID, fixture.workspaceID, run.SessionID, questionID, hypothesisID); insertErr != nil {
			return insertErr
		}
		backfillIntegrationArtifactPassport(t, ctx, tx, fixture.workspaceID, run.SessionID, edgeID, string(ArtifactKindInquiryEdge), nil, nil)
		return nil
	})
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_inquiry_edge (
		  workspace_id,session_id,from_kind,from_entity_id,to_kind,to_entity_id,relation
		) VALUES ($1::uuid,$2::uuid,'hypothesis',$3::uuid,'question',$4::uuid,'decomposes')
	`, fixture.workspaceID, run.SessionID, hypothesisID, questionID); err == nil {
		t.Fatal("reverse dependency edge created a cycle")
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_inquiry_edge (
		  workspace_id,session_id,from_kind,from_entity_id,to_kind,to_entity_id,relation
		) VALUES ($1::uuid,$2::uuid,'question',$3::uuid,'claim',$4::uuid,'tests')
	`, fixture.workspaceID, run.SessionID, questionID, uuid.NewString()); err == nil {
		t.Fatal("edge accepted a missing polymorphic endpoint")
	}
}
