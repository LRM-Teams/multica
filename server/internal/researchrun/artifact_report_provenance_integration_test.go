package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAcceptedReportVersionBindsCanonicalContentAndProducerAttempt(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	fixture := seedResearchRunFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fixture)
	store := NewPostgresStore(pool)
	run, _, err := store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID,
		FleetID: fixture.fleetID, CreatedBy: fixture.userID, LeadAgentID: fixture.agentID,
		Goal: "Report provenance", Title: "Report provenance", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	submitStoreTask(t, ctx, pool, store, fixture, "plan:1", e2eDeliveryPlan(), run.Config)

	evidence := upgradeResultToV5(e2eVerifiedEvidenceV4())
	evidence.AnswerClaimKey = "answer-claim"
	for index, key := range []string{"verify-1", "verify-2", "verify-3"} {
		evidence.ClientRequestID = fmt.Sprintf("report-provenance-evidence-%d", index+1)
		submitStoreTask(t, ctx, pool, store, fixture, key, evidence, run.Config)
	}
	report := e2eStructuredReport(t, ctx, pool, fixture.sessionID)
	submitStoreTask(t, ctx, pool, store, fixture, "synthesize", ResultEnvelope{
		SchemaVersion: 5, ClientRequestID: "report-provenance-result",
		Summary: "report", Confidence: 0.9, Report: &report,
	}, run.Config)

	var (
		revision, goalVersion, planVersion                                int
		contentMD, taskID, attemptID, authorAgentID                       string
		versionAttemptID, contentHash, hashOrigin, provenanceCompleteness string
		structured                                                        []byte
	)
	if err = pool.QueryRow(ctx, `
		SELECT report.revision, report.content_md, report.structured,
		       report.goal_version, report.plan_version, report.produced_by_task_id::text,
		       report.produced_by_attempt_id::text, report.author_agent_id::text,
		       version.produced_by_attempt_id::text, version.content_hash,
		       version.hash_origin, passport.provenance_completeness
		FROM research_report report
		JOIN research_artifact_passport passport
		  ON (passport.workspace_id,passport.session_id,passport.id)=
		     (report.workspace_id,report.session_id,report.id)
		JOIN research_artifact_version version
		  ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
		     (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
		WHERE report.workspace_id=$1::uuid AND report.session_id=$2::uuid
		ORDER BY report.revision DESC LIMIT 1
	`, fixture.workspaceID, fixture.sessionID).Scan(
		&revision, &contentMD, &structured, &goalVersion, &planVersion, &taskID,
		&attemptID, &authorAgentID, &versionAttemptID, &contentHash, &hashOrigin,
		&provenanceCompleteness,
	); err != nil {
		t.Fatal(err)
	}
	wantHash, err := ArtifactContentHash(ArtifactKindReportRevision, map[string]any{
		"revision": revision, "content_md": contentMD,
		"structured": json.RawMessage(structured), "goal_version": goalVersion,
		"plan_version": planVersion, "produced_by_task_id": taskID,
		"produced_by_attempt_id": attemptID, "author_agent_id": authorAgentID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if contentHash != wantHash || hashOrigin != string(ArtifactHashOriginProduction) ||
		provenanceCompleteness != string(ArtifactProvenanceComplete) ||
		versionAttemptID != attemptID {
		t.Fatalf("report version hash=%q want=%q origin=%q provenance=%q attempt=%q wantAttempt=%q",
			contentHash, wantHash, hashOrigin, provenanceCompleteness, versionAttemptID, attemptID)
	}
	var structuredReport reportStructuredV1
	if err = json.Unmarshal(report.Structured, &structuredReport); err != nil {
		t.Fatal(err)
	}
	wantSourceIDs := make([]string, 0, len(structuredReport.Sources))
	for _, source := range structuredReport.Sources {
		wantSourceIDs = append(wantSourceIDs, source.SourceID)
	}
	var sourceIDs []string
	if err = pool.QueryRow(ctx, `
		SELECT array_agg(input_version.artifact_id::text ORDER BY reference.ordinal)
		FROM research_artifact_input_reference reference
		JOIN research_artifact_version report_version
		  ON report_version.workspace_id = reference.workspace_id
		 AND report_version.session_id = reference.session_id
		 AND report_version.id = reference.consumer_version_id
		JOIN research_artifact_version input_version
		  ON input_version.workspace_id = reference.workspace_id
		 AND input_version.session_id = reference.session_id
		 AND input_version.id = reference.input_version_id
		WHERE report_version.artifact_id = (
			SELECT id FROM research_report
			WHERE workspace_id = $1::uuid AND session_id = $2::uuid
			ORDER BY revision DESC LIMIT 1
		)
		  AND reference.relation = 'report_source'
		  AND reference.purpose = 'report_materialization'
	`, fixture.workspaceID, fixture.sessionID).Scan(&sourceIDs); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(sourceIDs) != fmt.Sprint(wantSourceIDs) {
		t.Fatalf("report source lineage=%v want=%v", sourceIDs, wantSourceIDs)
	}
}
