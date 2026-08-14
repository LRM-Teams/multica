package researchrun

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLegacySourceSnapshotPayloadGuard(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Legacy Source reference guard",
		Title: "Source guard", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	snapshotID := uuid.NewString()
	otherSnapshotID := uuid.NewString()
	seedDiagnosticArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, snapshotID, ArtifactKindSourceSnapshot, `
		INSERT INTO research_source_snapshot(
		  id,workspace_id,session_id,canonical_url,title,publisher,source_class,
		  evidence_traits,independence_key,retrieved_at,content_hash,snapshot_text,
		  metadata,verification_status
		) VALUES ($1::uuid,$2::uuid,$3::uuid,'https://example.test/source-a','A','example','primary',
		  '{}'::text[],'a',now(),'sha256:a','a','{}'::jsonb,'verified')
	`, snapshotID, fixture.workspaceID, run.SessionID)
	seedDiagnosticArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, otherSnapshotID, ArtifactKindSourceSnapshot, `
		INSERT INTO research_source_snapshot(
		  id,workspace_id,session_id,canonical_url,title,publisher,source_class,
		  evidence_traits,independence_key,retrieved_at,content_hash,snapshot_text,
		  metadata,verification_status
		) VALUES ($1::uuid,$2::uuid,$3::uuid,'https://example.test/source-b','B','example','primary',
		  '{}'::text[],'b',now(),'sha256:b','b','{}'::jsonb,'verified')
	`, otherSnapshotID, fixture.workspaceID, run.SessionID)
	matchingID := uuid.NewString()
	seedDiagnosticArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, matchingID, ArtifactKindLegacySource, `
		INSERT INTO research_source(id,workspace_id,session_id,title,source_snapshot_id,payload)
		VALUES($1::uuid,$2::uuid,$3::uuid,'matching',$4::uuid,jsonb_build_object('snapshot_id',$4::text))
	`, matchingID, fixture.workspaceID, run.SessionID, snapshotID)

	for _, tc := range []struct {
		name         string
		payload      string
		relationalID string
	}{
		{name: "malformed", payload: `{"snapshot_id":"not-a-uuid"}`, relationalID: snapshotID},
		{name: "mismatch", payload: `{"snapshot_id":"` + otherSnapshotID + `"}`, relationalID: snapshotID},
		{name: "json only", payload: `{"snapshot_id":"` + snapshotID + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sourceID := uuid.NewString()
			insertErr := execDiagnosticArtifact(ctx, pool, fixture.workspaceID, run.SessionID, sourceID, ArtifactKindLegacySource, `
				INSERT INTO research_source(id,workspace_id,session_id,title,source_snapshot_id,payload)
				VALUES($1::uuid,$2::uuid,$3::uuid,$4,NULLIF($5,'')::uuid,$6::jsonb)
			`, sourceID, fixture.workspaceID, run.SessionID, tc.name, tc.relationalID, tc.payload)
			var pgErr *pgconn.PgError
			if !errors.As(insertErr, &pgErr) || pgErr.ConstraintName != "research_legacy_source_snapshot_payload_guard" {
				t.Fatalf("insert err=%v constraint=%q", insertErr, pgErrConstraintName(pgErr))
			}
		})
	}
}

func pgErrConstraintName(err *pgconn.PgError) string {
	if err == nil {
		return ""
	}
	return err.ConstraintName
}
