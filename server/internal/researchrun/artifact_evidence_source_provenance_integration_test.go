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
	var legacyURL, legacyTitle, legacyClass, legacyStance, legacySummary, legacyExcerpt string
	var legacySnapshotID, legacyAttemptID, legacyVersionHash, legacyOrigin, legacyProvenance string
	var legacyWeight, legacyRelevance float64
	var legacyPayload []byte
	if err = pool.QueryRow(ctx, `
		SELECT source.url, source.title, source.source_class, source.credibility_weight,
		       source.stance, source.relevance, source.summary, source.excerpt, source.payload,
		       source.source_snapshot_id::text, version.produced_by_attempt_id::text,
		       version.content_hash, version.hash_origin, passport.provenance_completeness
		FROM research_source source
		JOIN research_artifact_passport passport
		  ON (passport.workspace_id,passport.session_id,passport.id)=(source.workspace_id,source.session_id,source.id)
		JOIN research_artifact_version version
		  ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
		     (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
		WHERE source.workspace_id=$1::uuid AND source.session_id=$2::uuid
		  AND source.source_snapshot_id=$3::uuid
	`, fixture.workspaceID, fixture.sessionID, sourceID).Scan(
		&legacyURL, &legacyTitle, &legacyClass, &legacyWeight, &legacyStance, &legacyRelevance,
		&legacySummary, &legacyExcerpt, &legacyPayload, &legacySnapshotID, &legacyAttemptID,
		&legacyVersionHash, &legacyOrigin, &legacyProvenance,
	); err != nil {
		t.Fatal(err)
	}
	wantLegacyHash, err := ArtifactContentHash(ArtifactKindLegacySource, legacySourceArtifactContent(
		legacyURL, legacyTitle, legacyClass, legacyWeight, legacyStance, legacyRelevance,
		legacySummary, legacyExcerpt, legacyPayload, legacySnapshotID,
	))
	if err != nil {
		t.Fatal(err)
	}
	if legacyVersionHash != wantLegacyHash || legacyOrigin != string(ArtifactHashOriginProduction) ||
		legacyProvenance != string(ArtifactProvenanceComplete) || legacyAttemptID != sourceAttemptID {
		t.Fatalf("legacy source hash=%q want=%q origin=%q provenance=%q attempt=%q sourceAttempt=%q",
			legacyVersionHash, wantLegacyHash, legacyOrigin, legacyProvenance, legacyAttemptID, sourceAttemptID)
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
	var projectionEdges, observationEdges, sourceProducerEdges, observationProducerEdges int
	if err = pool.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE reference.relation='projects')::int,
		  count(*) FILTER (WHERE reference.relation='observes')::int,
		  (SELECT count(*)::int FROM research_artifact_input_reference producer
		   JOIN research_artifact_version consumer ON consumer.id=producer.consumer_version_id
		   WHERE producer.workspace_id=$1::uuid AND producer.session_id=$2::uuid
		     AND producer.relation='source_producer' AND consumer.artifact_id=$3::uuid),
		  (SELECT count(*)::int FROM research_artifact_input_reference producer
		   WHERE producer.workspace_id=$1::uuid AND producer.session_id=$2::uuid
		     AND producer.relation='observation_producer'
		     AND producer.consumer_version_id IN (
		       SELECT observes.consumer_version_id
		       FROM research_artifact_input_reference observes
		       JOIN research_artifact_version source_version ON source_version.id=observes.input_version_id
		       WHERE observes.workspace_id=$1::uuid AND observes.session_id=$2::uuid
		         AND observes.relation='observes' AND source_version.artifact_id=$3::uuid
		     ))
		FROM research_artifact_input_reference reference
		JOIN research_artifact_version input_version
		  ON input_version.workspace_id=reference.workspace_id
		 AND input_version.session_id=reference.session_id
		 AND input_version.id=reference.input_version_id
		WHERE reference.workspace_id=$1::uuid AND reference.session_id=$2::uuid
		  AND input_version.artifact_id=$3::uuid
	`, fixture.workspaceID, fixture.sessionID, sourceID).Scan(
		&projectionEdges, &observationEdges, &sourceProducerEdges, &observationProducerEdges,
	); err != nil {
		t.Fatal(err)
	}
	if projectionEdges != 1 || observationEdges == 0 || sourceProducerEdges != 1 ||
		observationProducerEdges != observationEdges {
		t.Fatalf("source lineage projects=%d want=1 observes=%d producers=%d/%d",
			projectionEdges, observationEdges, sourceProducerEdges, observationProducerEdges)
	}
	var expectedClaimEdges, expectedStandardEdges, expectedEvidenceEdges int
	var actualClaimEdges, actualStandardEdges, actualEvidenceEdges int
	if err = pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*)::int FROM research_claim
		   WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_claim
		   WHERE workspace_id=$1::uuid AND session_id=$2::uuid
		     AND evidence_standard_key<>''),
		  (SELECT (count(*)*2 + count(*) FILTER (WHERE verified_by_task_id IS NOT NULL))::int
		   FROM research_claim_evidence
		   WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_artifact_input_reference
		   WHERE workspace_id=$1::uuid AND session_id=$2::uuid
		     AND relation='claim_producer'),
		  (SELECT count(*)::int FROM research_artifact_input_reference
		   WHERE workspace_id=$1::uuid AND session_id=$2::uuid
		     AND relation='claim_evidence_standard'),
		  (SELECT count(*)::int FROM research_artifact_input_reference
		   WHERE workspace_id=$1::uuid AND session_id=$2::uuid
		     AND relation IN ('evidence_claim','evidence_observation','evidence_verifier'))
	`, fixture.workspaceID, fixture.sessionID).Scan(
		&expectedClaimEdges, &expectedStandardEdges, &expectedEvidenceEdges,
		&actualClaimEdges, &actualStandardEdges, &actualEvidenceEdges,
	); err != nil {
		t.Fatal(err)
	}
	if actualClaimEdges != expectedClaimEdges || actualStandardEdges != expectedStandardEdges ||
		actualEvidenceEdges != expectedEvidenceEdges {
		t.Fatalf("claim/evidence lineage claim=%d want=%d standard=%d want=%d evidence=%d want=%d",
			actualClaimEdges, expectedClaimEdges, actualStandardEdges, expectedStandardEdges,
			actualEvidenceEdges, expectedEvidenceEdges)
	}
}
