package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/internal/problemevolution"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// sentinel is a string that must never appear anywhere except the sealed
// ciphertext and a capability redemption response.
const problemEvolutionSecretSentinel = "SENTINEL-HIDDEN-ANSWER-7f3a91"

func withProblemEvolutionMasterKey(t *testing.T) {
	t.Helper()
	t.Setenv(problemevolution.MasterKeyEnv,
		base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)))
	t.Setenv(problemevolution.MasterKeyIDEnv, "test:v1")
	// The sealer is resolved once per process; reset it so this test's key is
	// the one used.
	problemEvolutionSealerOnce = sync.Once{}
	problemEvolutionSealerValue = nil
	problemEvolutionSealerErr = nil
}

func createSecret(t *testing.T, runID, plaintext string) ProblemEvolutionSecretResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	req := newProblemEvolutionRequest(t, http.MethodPost,
		"/api/problem-evolution/runs/"+runID+"/secrets", map[string]any{
			"kind":      "hidden_answer",
			"label":     "answer",
			"plaintext": plaintext,
		}, "runId", runID)
	testHandler.CreateProblemEvolutionSecret(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create secret status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var secret ProblemEvolutionSecretResponse
	if err := json.NewDecoder(rec.Body).Decode(&secret); err != nil {
		t.Fatalf("decode secret: %v", err)
	}
	return secret
}

func issueCapability(t *testing.T, runID, secretID string) ProblemEvolutionCapabilityResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	req := newProblemEvolutionRequest(t, http.MethodPost,
		"/api/problem-evolution/runs/"+runID+"/capabilities", map[string]any{
			"secret_id": secretID,
			"max_uses":  1,
			"issued_to": "verifier-1",
		}, "runId", runID)
	testHandler.IssueProblemEvolutionCapability(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("issue capability status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var capability ProblemEvolutionCapabilityResponse
	if err := json.NewDecoder(rec.Body).Decode(&capability); err != nil {
		t.Fatalf("decode capability: %v", err)
	}
	return capability
}

func redeemCapability(t *testing.T, token, runID string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"token": token, "run_id": runID})
	if err != nil {
		t.Fatalf("marshal redeem body: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/daemon/problem-evolution/capabilities/redeem", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	testHandler.RedeemProblemEvolutionCapability(rec, req)
	return rec
}

func TestProblemEvolutionSecretIsNeverReturnedAsPlaintextMetadata(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	withProblemEvolutionMasterKey(t)
	runID := createSolveRun(t)

	secret := createSecret(t, runID, problemEvolutionSecretSentinel)
	if strings.Contains(secret.ContentHash, problemEvolutionSecretSentinel) {
		t.Fatal("content hash leaked the plaintext")
	}

	rec := httptest.NewRecorder()
	req := newProblemEvolutionRequest(t, http.MethodGet,
		"/api/problem-evolution/runs/"+runID+"/secrets", nil, "runId", runID)
	testHandler.ListProblemEvolutionSecrets(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list secrets status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), problemEvolutionSecretSentinel) {
		t.Fatalf("secret listing leaked the plaintext: %s", rec.Body.String())
	}
}

func TestProblemEvolutionSecretPlaintextIsNotInAnyTextColumn(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	withProblemEvolutionMasterKey(t)
	ctx := context.Background()
	runID := createSolveRun(t)
	createSecret(t, runID, problemEvolutionSecretSentinel)

	// Sweep every text-ish column of the problem-evolution tables. The sealed
	// bytea columns are excluded because that is where the ciphertext lives.
	var leaks int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT label AS value FROM problem_evolution_secret
			UNION ALL SELECT content_hash FROM problem_evolution_secret
			UNION ALL SELECT key_id FROM problem_evolution_secret
			UNION ALL SELECT reason FROM problem_evolution_secret_audit
			UNION ALL SELECT actor_id FROM problem_evolution_secret_audit
			UNION ALL SELECT issued_to FROM problem_evolution_secret_capability
			UNION ALL SELECT token_hash FROM problem_evolution_secret_capability
			UNION ALL SELECT payload::text FROM problem_evolution_event
			UNION ALL SELECT summary FROM problem_evolution_candidate
			UNION ALL SELECT problem_spec::text FROM problem_evolution_run
		) AS columns
		WHERE value LIKE '%' || $1 || '%'
	`, problemEvolutionSecretSentinel).Scan(&leaks); err != nil {
		t.Fatalf("sweep for sentinel: %v", err)
	}
	if leaks != 0 {
		t.Fatalf("sentinel appeared in %d readable columns", leaks)
	}
}

func TestProblemEvolutionCapabilityRedeemsOnceThenDenies(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	withProblemEvolutionMasterKey(t)
	ctx := context.Background()
	runID := createSolveRun(t)
	secret := createSecret(t, runID, problemEvolutionSecretSentinel)
	capability := issueCapability(t, runID, secret.ID)

	first := redeemCapability(t, capability.Token, runID)
	if first.Code != http.StatusOK {
		t.Fatalf("first redemption status = %d, want 200: %s", first.Code, first.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode redemption: %v", err)
	}
	if payload["plaintext"] != problemEvolutionSecretSentinel {
		t.Fatalf("redemption returned %v, want the sealed plaintext", payload["plaintext"])
	}

	// A single-use capability is minted per evaluation; replaying it would turn
	// it into a standing read grant.
	second := redeemCapability(t, capability.Token, runID)
	if second.Code != http.StatusForbidden {
		t.Fatalf("replay status = %d, want 403", second.Code)
	}
	denials, err := testHandler.Queries.CountProblemEvolutionSecretDenials(ctx, parseUUID(runID))
	if err != nil {
		t.Fatalf("count denials: %v", err)
	}
	if denials != 1 {
		t.Fatalf("denials = %d, want 1", denials)
	}
}

func TestProblemEvolutionCapabilityDeniedForOtherRun(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	withProblemEvolutionMasterKey(t)
	runID := createSolveRun(t)
	otherRunID := createSolveRun(t)
	secret := createSecret(t, runID, problemEvolutionSecretSentinel)
	capability := issueCapability(t, runID, secret.ID)

	rec := redeemCapability(t, capability.Token, otherRunID)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-run redemption status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), problemEvolutionSecretSentinel) {
		t.Fatal("a denied redemption leaked the plaintext")
	}
}

func TestProblemEvolutionSecretRevocationDeniesIssuedCapabilities(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	withProblemEvolutionMasterKey(t)
	runID := createSolveRun(t)
	secret := createSecret(t, runID, problemEvolutionSecretSentinel)
	capability := issueCapability(t, runID, secret.ID)

	rec := httptest.NewRecorder()
	req := newProblemEvolutionRequest(t, http.MethodPost,
		"/api/problem-evolution/runs/"+runID+"/secrets/"+secret.ID+"/revoke", nil,
		"runId", runID, "secretId", secret.ID)
	testHandler.RevokeProblemEvolutionSecret(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// Revocation is the break-glass path: capabilities already handed out must
	// stop working immediately, not at their own expiry.
	redeemed := redeemCapability(t, capability.Token, runID)
	if redeemed.Code != http.StatusForbidden {
		t.Fatalf("redemption after revocation status = %d, want 403", redeemed.Code)
	}
}

func TestProblemEvolutionCapabilityRedemptionIsAudited(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	withProblemEvolutionMasterKey(t)
	ctx := context.Background()
	runID := createSolveRun(t)
	secret := createSecret(t, runID, problemEvolutionSecretSentinel)
	capability := issueCapability(t, runID, secret.ID)
	if rec := redeemCapability(t, capability.Token, runID); rec.Code != http.StatusOK {
		t.Fatalf("redemption status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	rows, err := testHandler.Queries.ListProblemEvolutionSecretAudit(ctx, db.ListProblemEvolutionSecretAuditParams{
		RunID:       parseUUID(runID),
		ResultLimit: 50,
	})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	actions := map[string]int{}
	for _, row := range rows {
		actions[row.Action]++
		if strings.Contains(row.Reason, problemEvolutionSecretSentinel) {
			t.Fatal("audit reason leaked the plaintext")
		}
	}
	for _, action := range []string{"created", "issued", "redeemed"} {
		if actions[action] == 0 {
			t.Fatalf("audit trail is missing a %q row: %v", action, actions)
		}
	}
}

func TestProblemEvolutionRedeemRejectsMalformedToken(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	withProblemEvolutionMasterKey(t)
	runID := createSolveRun(t)
	if rec := redeemCapability(t, "not-a-capability", runID); rec.Code != http.StatusForbidden {
		t.Fatalf("malformed token status = %d, want 403", rec.Code)
	}
}
