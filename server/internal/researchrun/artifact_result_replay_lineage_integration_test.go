package researchrun

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAcceptResultReplayRejectsChangedPersistedLineage(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	cases := []struct {
		name   string
		mutate func(context.Context, acceptanceRaceFixture) error
	}{
		{
			name: "stored_manifest_hash",
			mutate: func(ctx context.Context, fx acceptanceRaceFixture) error {
				_, err := pool.Exec(ctx, `
					UPDATE research_result_artifact
					SET manifest_hash='sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
					WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND attempt_id=$3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.attempt.ID)
				return err
			},
		},
		{
			name: "manifest_id",
			mutate: func(ctx context.Context, fx acceptanceRaceFixture) error {
				_, err := pool.Exec(ctx, `
					UPDATE research_result_artifact SET manifest_id=NULL
					WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND attempt_id=$3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.attempt.ID)
				return err
			},
		},
		{
			name: "current_manifest_hash",
			mutate: func(ctx context.Context, fx acceptanceRaceFixture) error {
				_, err := pool.Exec(ctx, `
					UPDATE research_artifact_context_manifest
					SET manifest_hash='sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
					WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND attempt_id=$3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.attempt.ID)
				return err
			},
		},
		{
			name: "resolved_input_version_set",
			mutate: func(ctx context.Context, fx acceptanceRaceFixture) error {
				_, err := pool.Exec(ctx, `
					DELETE FROM research_artifact_input_reference reference
					USING research_result_artifact result, research_artifact_passport passport,
					      research_artifact_version version
					WHERE (passport.workspace_id,passport.session_id,passport.id)=
					      (result.workspace_id,result.session_id,result.id)
					  AND (version.workspace_id,version.session_id,version.artifact_id,version.version)=
					      (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
					  AND reference.consumer_version_id=version.id
					  AND reference.relation='acceptance_input'
					  AND result.workspace_id=$1::uuid AND result.session_id=$2::uuid
					  AND result.attempt_id=$3::uuid
					  AND reference.id=(
					    SELECT candidate.id FROM research_artifact_input_reference candidate
					    WHERE candidate.consumer_version_id=version.id
					      AND candidate.relation='acceptance_input'
					    ORDER BY candidate.id LIMIT 1
					  )
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.attempt.ID)
				return err
			},
		},
		{
			name: "acceptance_watermark",
			mutate: func(ctx context.Context, fx acceptanceRaceFixture) error {
				_, err := pool.Exec(ctx, `
					UPDATE research_result_artifact result
					SET acceptance_policy_watermark=manifest.policy_watermark
					FROM research_artifact_context_manifest manifest
					WHERE manifest.id=result.manifest_id
					  AND result.workspace_id=$1::uuid AND result.session_id=$2::uuid
					  AND result.attempt_id=$3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.attempt.ID)
				return err
			},
		},
		{
			name: "stored_result_json",
			mutate: func(ctx context.Context, fx acceptanceRaceFixture) error {
				_, err := pool.Exec(ctx, `
					UPDATE research_result_artifact
					SET result=jsonb_set(result,'{summary}','"tampered replay projection"'::jsonb)
					WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND attempt_id=$3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.attempt.ID)
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := seedResearchRunFixture(t, ctx, pool)
			defer cleanupResearchRunFixture(pool, fixture)
			store := NewPostgresStore(pool)
			attempt, inboxID, raw, run, task := setupRunningPlanAttempt(t, ctx, store, fixture)
			result, hash, decodeErr := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			input := AcceptResultInput{
				SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
				InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
			}
			if _, err = store.AcceptResult(ctx, input); err != nil {
				t.Fatalf("first AcceptResult: %v", err)
			}
			fx := acceptanceRaceFixture{
				pool: pool, store: store, fixture: fixture, run: run, task: task,
				attempt: attempt, inboxID: inboxID, input: input,
			}
			if err = tc.mutate(ctx, fx); err != nil {
				if strings.Contains(err.Error(), "append-only") || strings.Contains(err.Error(), "immutable") {
					return
				}
				t.Fatalf("mutate: %v", err)
			}
			if _, err = store.AcceptResult(ctx, input); !errors.Is(err, ErrResultConflict) {
				t.Fatalf("replay after %s mutation err=%v want ErrResultConflict", tc.name, err)
			}
		})
	}
}

func TestResultReplayLineageMigrationFilesStayPaired(t *testing.T) {
	for _, path := range []string{
		"../../migrations/352_research_result_replay_lineage.up.sql",
		"../../migrations/352_research_result_replay_lineage.down.sql",
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("migration file %s: %v", path, err)
		}
	}
}
