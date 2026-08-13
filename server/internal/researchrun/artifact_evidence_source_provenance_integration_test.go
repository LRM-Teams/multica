package researchrun

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAcceptedSourceAndObservationVersionsBindCanonicalContentAndAttempt(t *testing.T) {
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
	run, _, err := store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID,
		FleetID: fixture.fleetID, CreatedBy: fixture.userID, LeadAgentID: fixture.agentID,
		Goal: "Evidence provenance", Title: "Evidence provenance", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	submitStoreTask(t, ctx, pool, store, fixture, "plan:1", e2eDeliveryPlan(), run.Config)
	evidence := upgradeResultToV5(e2eVerifiedEvidenceV4())
	evidence.ClientRequestID = "evidence-source-provenance"
	evidence.AnswerClaimKey = "answer-claim"
	submitStoreTask(t, ctx, pool, store, fixture, "verify-1", evidence, run.Config)

	var (
		sourceID, sourceTaskID, sourceAttemptID, canonicalURL, title, publisher string
		sourceClass, independenceKey, snapshotText, sourceContentHash           string
		verificationStatus, versionHash, hashOrigin, provenance                 string
		evidenceTraits                                                          []string
		retrievedAt                                                             time.Time
		metadata                                                                []byte
	)
	if err = pool.QueryRow(ctx, `
		SELECT source.id::text, source.produced_by_task_id::text, version.produced_by_attempt_id::text,
		       source.canonical_url, source.title, source.publisher, source.source_class,
		       source.evidence_traits, source.independence_key, source.retrieved_at,
		       source.snapshot_text, source.content_hash, source.metadata, source.verification_status,
		       version.content_hash, version.hash_origin, passport.provenance_completeness
		FROM research_source_snapshot source
		JOIN research_artifact_passport passport
		  ON (passport.workspace_id,passport.session_id,passport.id)=(source.workspace_id,source.session_id,source.id)
		JOIN research_artifact_version version
		  ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
		     (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
		WHERE source.workspace_id=$1::uuid AND source.session_id=$2::uuid
		ORDER BY source.id LIMIT 1
	`, fixture.workspaceID, fixture.sessionID).Scan(
		&sourceID, &sourceTaskID, &sourceAttemptID, &canonicalURL, &title, &publisher,
		&sourceClass, &evidenceTraits, &independenceKey, &retrievedAt, &snapshotText,
		&sourceContentHash, &metadata, &verificationStatus, &versionHash, &hashOrigin, &provenance,
	); err != nil {
		t.Fatal(err)
	}
	wantSourceHash, err := ArtifactContentHash(ArtifactKindSourceSnapshot, map[string]any{
		"produced_by_task_id": sourceTaskID, "canonical_url": canonicalURL,
		"title": title, "publisher": publisher, "source_class": sourceClass,
		"evidence_traits": evidenceTraits, "independence_key": independenceKey,
		"retrieved_at": retrievedAt, "snapshot_text": snapshotText,
		"content_hash": sourceContentHash, "metadata": json.RawMessage(metadata),
		"verification_status": verificationStatus,
	})
	if err != nil {
		t.Fatal(err)
	}
	if versionHash != wantSourceHash || hashOrigin != string(ArtifactHashOriginProduction) ||
		provenance != string(ArtifactProvenanceComplete) || sourceAttemptID == "" {
		t.Fatalf("source version hash=%q want=%q origin=%q provenance=%q attempt=%q",
			versionHash, wantSourceHash, hashOrigin, provenance, sourceAttemptID)
	}

	var (
		observationTaskID, observationAttemptID, quote, locator, interpretation string
		observationContentHash, observationVerification, observationVersionHash string
		observationOrigin, observationProvenance                                string
		datum                                                                   []byte
	)
	if err = pool.QueryRow(ctx, `
		SELECT observation.produced_by_task_id::text, version.produced_by_attempt_id::text,
		       observation.quote, observation.datum, observation.locator, observation.interpretation,
		       observation.content_hash, observation.verification_status, version.content_hash,
		       version.hash_origin, passport.provenance_completeness
		FROM research_observation observation
		JOIN research_artifact_passport passport
		  ON (passport.workspace_id,passport.session_id,passport.id)=
		     (observation.workspace_id,observation.session_id,observation.id)
		JOIN research_artifact_version version
		  ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
		     (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
		WHERE observation.workspace_id=$1::uuid AND observation.session_id=$2::uuid
		  AND observation.source_snapshot_id=$3::uuid
		ORDER BY observation.id LIMIT 1
	`, fixture.workspaceID, fixture.sessionID, sourceID).Scan(
		&observationTaskID, &observationAttemptID, &quote, &datum, &locator, &interpretation,
		&observationContentHash, &observationVerification, &observationVersionHash,
		&observationOrigin, &observationProvenance,
	); err != nil {
		t.Fatal(err)
	}
	wantObservationHash, err := ArtifactContentHash(ArtifactKindObservation, map[string]any{
		"source_snapshot_id": sourceID, "produced_by_task_id": observationTaskID,
		"quote": quote, "datum": json.RawMessage(datum), "locator": locator,
		"interpretation": interpretation, "content_hash": observationContentHash,
		"verification_status": observationVerification,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observationVersionHash != wantObservationHash ||
		observationOrigin != string(ArtifactHashOriginProduction) ||
		observationProvenance != string(ArtifactProvenanceComplete) ||
		observationAttemptID != sourceAttemptID {
		t.Fatalf("observation version hash=%q want=%q origin=%q provenance=%q attempt=%q sourceAttempt=%q",
			observationVersionHash, wantObservationHash, observationOrigin,
			observationProvenance, observationAttemptID, sourceAttemptID)
	}
}
