package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAcceptedClaimAndEvidenceVersionsBindCanonicalContentAndProducerAttempt(t *testing.T) {
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
		Goal: "Output provenance", Title: "Output provenance", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	submitStoreTask(t, ctx, pool, store, fixture, "plan:1", e2eDeliveryPlan(), run.Config)
	evidence := upgradeResultToV5(e2eVerifiedEvidenceV4())
	evidence.ClientRequestID = "output-provenance-evidence"
	evidence.AnswerClaimKey = "answer-claim"
	submitStoreTask(t, ctx, pool, store, fixture, "verify-1", evidence, run.Config)

	var (
		claimID, claimTaskID, claimAttemptID, claimHash, claimOrigin, claimProvenance string
		claimKey, standardKey, claimText, significance, claimStatus, resolution       string
		claimConfidence                                                               float64
		goalVersion, planVersion                                                      int
	)
	if err = pool.QueryRow(ctx, `
		SELECT claim.id::text, claim.produced_by_task_id::text, version.produced_by_attempt_id::text,
		       version.content_hash, version.hash_origin, passport.provenance_completeness,
		       claim.client_key, claim.evidence_standard_key, claim.claim_text, claim.significance,
		       claim.confidence, claim.status, claim.goal_version, claim.plan_version, claim.resolution
		FROM research_claim claim
		JOIN research_artifact_passport passport
		  ON (passport.workspace_id,passport.session_id,passport.id)=(claim.workspace_id,claim.session_id,claim.id)
		JOIN research_artifact_version version
		  ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
		     (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
		WHERE claim.workspace_id=$1::uuid AND claim.session_id=$2::uuid AND claim.client_key='answer-claim'
	`, fixture.workspaceID, fixture.sessionID).Scan(
		&claimID, &claimTaskID, &claimAttemptID, &claimHash, &claimOrigin, &claimProvenance,
		&claimKey, &standardKey, &claimText, &significance, &claimConfidence, &claimStatus,
		&goalVersion, &planVersion, &resolution,
	); err != nil {
		t.Fatal(err)
	}
	wantClaimHash, err := ArtifactContentHash(ArtifactKindClaim, map[string]any{
		"client_key": claimKey, "evidence_standard_key": standardKey,
		"claim_text": claimText, "significance": significance, "confidence": claimConfidence,
		"status": claimStatus, "goal_version": goalVersion, "plan_version": planVersion,
		"resolution": resolution, "produced_by_task_id": claimTaskID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if claimHash != wantClaimHash || claimOrigin != string(ArtifactHashOriginProduction) ||
		claimProvenance != string(ArtifactProvenanceComplete) || claimAttemptID == "" {
		t.Fatalf("claim version hash=%q want=%q origin=%q provenance=%q attempt=%q",
			claimHash, wantClaimHash, claimOrigin, claimProvenance, claimAttemptID)
	}

	var (
		evidenceHash, evidenceOrigin, evidenceProvenance, evidenceAttemptID string
		observationID, relation, verificationStatus, verifiedByTaskID       string
		rationale                                                           string
		strength, directness, methodFit                                     float64
	)
	if err = pool.QueryRow(ctx, `
		SELECT link.observation_id::text, link.relation, link.strength, link.directness,
		       link.method_fit, link.verification_status, link.verified_by_task_id::text,
		       link.rationale, version.content_hash, version.hash_origin,
		       passport.provenance_completeness, version.produced_by_attempt_id::text
		FROM research_claim_evidence link
		JOIN research_artifact_passport passport
		  ON (passport.workspace_id,passport.session_id,passport.id)=(link.workspace_id,link.session_id,link.id)
		JOIN research_artifact_version version
		  ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
		     (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
		WHERE link.workspace_id=$1::uuid AND link.session_id=$2::uuid AND link.claim_id=$3::uuid
		ORDER BY link.id LIMIT 1
	`, fixture.workspaceID, fixture.sessionID, claimID).Scan(
		&observationID, &relation, &strength, &directness, &methodFit, &verificationStatus,
		&verifiedByTaskID, &rationale, &evidenceHash, &evidenceOrigin, &evidenceProvenance,
		&evidenceAttemptID,
	); err != nil {
		t.Fatal(err)
	}
	wantEvidenceHash, err := ArtifactContentHash(ArtifactKindEvidenceLink, map[string]any{
		"claim_id": claimID, "observation_id": observationID, "relation": relation,
		"strength": strength, "directness": directness, "method_fit": methodFit,
		"verification_status": verificationStatus, "verified_by_task_id": verifiedByTaskID,
		"rationale": rationale,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidenceHash != wantEvidenceHash || evidenceOrigin != string(ArtifactHashOriginProduction) ||
		evidenceProvenance != string(ArtifactProvenanceComplete) || evidenceAttemptID != claimAttemptID {
		t.Fatalf("evidence version hash=%q want=%q origin=%q provenance=%q attempt=%q claimAttempt=%q",
			evidenceHash, wantEvidenceHash, evidenceOrigin, evidenceProvenance, evidenceAttemptID, claimAttemptID)
	}
}
