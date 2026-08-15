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

func TestResearchMessagePrincipalAndRunEventScopeConstraints(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	left := seedResearchRunFixture(t, ctx, pool)
	right := seedResearchRunFixture(t, ctx, pool)

	leftEvent := uuid.NewString()
	rightEvent := uuid.NewString()
	insertEvent := `INSERT INTO research_run_event (
		id,workspace_id,session_id,sequence,event_type,idempotency_key,actor_type
	) VALUES ($1::uuid,$2::uuid,$3::uuid,1,'run_started',$4,'system')`
	seedDiagnosticArtifact(t, ctx, pool, left.workspaceID, left.sessionID, leftEvent, ArtifactKindRunEvent,
		insertEvent, leftEvent, left.workspaceID, left.sessionID, "left-"+uuid.NewString())
	seedDiagnosticArtifact(t, ctx, pool, right.workspaceID, right.sessionID, rightEvent, ArtifactKindRunEvent,
		insertEvent, rightEvent, right.workspaceID, right.sessionID, "right-"+uuid.NewString())

	insertMessage := `INSERT INTO research_message (
		id,workspace_id,session_id,sender_type,sender_id,target_agent_id,run_event_id,body
	) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,$7::uuid,'message')`
	_, err = pool.Exec(ctx, insertMessage, uuid.NewString(), left.workspaceID, left.sessionID, "agent", left.agentID, right.agentID, leftEvent)
	assertResearchMessageConstraint(t, err, "research_message_target_agent_scoped_fkey")
	_, err = pool.Exec(ctx, insertMessage, uuid.NewString(), left.workspaceID, left.sessionID, "agent", left.agentID, left.agentID, rightEvent)
	assertResearchMessageConstraint(t, err, "research_message_run_event_scoped_fkey")
	_, err = pool.Exec(ctx, insertMessage, uuid.NewString(), left.workspaceID, left.sessionID, "agent", right.agentID, left.agentID, leftEvent)
	assertResearchMessageConstraint(t, err, "research_message_sender_principal_guard")
	_, err = pool.Exec(ctx, insertMessage, uuid.NewString(), left.workspaceID, left.sessionID, "system", left.userID, left.agentID, leftEvent)
	assertResearchMessageConstraint(t, err, "research_message_sender_principal_guard")

	agentMessageID := uuid.NewString()
	seedDiagnosticArtifact(t, ctx, pool, left.workspaceID, left.sessionID, agentMessageID, ArtifactKindResearchMessage,
		insertMessage, agentMessageID, left.workspaceID, left.sessionID, "agent", left.agentID, left.agentID, leftEvent)
	userMessageID := uuid.NewString()
	seedDiagnosticArtifact(t, ctx, pool, left.workspaceID, left.sessionID, userMessageID, ArtifactKindResearchMessage,
		insertMessage, userMessageID, left.workspaceID, left.sessionID, "user", left.userID, nil, nil)
}

func assertResearchMessageConstraint(t *testing.T, err error, constraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != constraint {
		t.Fatalf("error=%v constraint=%q want=%q", err, func() string {
			if pgErr == nil {
				return ""
			}
			return pgErr.ConstraintName
		}(), constraint)
	}
}
