package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestValidateOfflineTrajectoriesResolveRequest(t *testing.T) {
	assert.Error(t, validateOfflineTrajectoriesResolveRequest(OfflineTrajectoriesResolveRequest{CallIDs: []string{"C1"}}))
	assert.Error(t, validateOfflineTrajectoriesResolveRequest(OfflineTrajectoriesResolveRequest{SnapshotID: "sha256:x"}))
	assert.NoError(t, validateOfflineTrajectoriesResolveRequest(OfflineTrajectoriesResolveRequest{
		SnapshotID: "sha256:x", CallIDs: []string{},
	}))
}

func TestOfflineResolveAuthorizedRunAndSnapshotBinding(t *testing.T) {
	ws := util.MustParseUUID("70000000-0000-4000-8000-000000000301")
	other := util.MustParseUUID("70000000-0000-4000-8000-000000000302")
	assert.True(t, offlineResolveAuthorizedRun(ws, ws))
	assert.False(t, offlineResolveAuthorizedRun(ws, other))
	assert.False(t, offlineResolveAuthorizedRun(pgtype.UUID{}, ws))

	assert.True(t, offlineResolveSnapshotMatches("sha256:frozen", "sha256:frozen"))
	assert.False(t, offlineResolveSnapshotMatches("sha256:frozen", "sha256:other"))
	assert.False(t, offlineResolveSnapshotMatches("", "sha256:frozen"))
}

func TestWriteOfflineTrajectoryNDJSON_MemberOrderModeChecksDedupeAndOmitsRawPayload(t *testing.T) {
	raw := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"q"}]}],"tools":[],"temperature":0}`)
	final := []byte(`{"role":"assistant","blocks":[{"type":"text","text":"a"}]}`)
	hashReq := serviceHash(raw)
	hashResp := serviceHash(final)
	mk := func(id, mode string, eligible bool) service.OfflineCallSource {
		return service.OfflineCallSource{
			CallID: id, TrainingMode: mode,
			Provider: "synthetic-provider", Model: "synthetic-model", APIKind: "messages",
			RawProviderRequest: raw, FinalAssistantMessage: final,
			Status: "completed", StopReason: "stop", ResponseComplete: true, TrainingEligible: eligible,
			RequestHash: hashReq, ResponseHash: hashResp,
		}
	}
	lines := service.ResolveOfflineTrajectoryLines(
		[]service.OfflineCallSource{
			mk("C2", "offline_rl", true),
			mk("C1", "online_rl", true),
			mk("C3", "none", false),
		},
		[]string{"C1", "C2", "C1", "ZX", "C3", "AY"},
	)
	require.Len(t, lines, 5)
	assert.Equal(t, []string{"C2", "C1", "C3", "AY", "ZX"}, []string{
		lines[0].CallID, lines[1].CallID, lines[2].CallID, lines[3].CallID, lines[4].CallID,
	})
	assert.Equal(t, "trajectory", lines[0].Status)
	assert.Equal(t, service.OfflineReasonWrongModeOnlineRL, lines[1].Reason)
	assert.Equal(t, service.OfflineReasonWrongModeNone, lines[2].Reason)
	assert.Equal(t, service.OfflineReasonCallNotInSnapshot, lines[3].Reason)
	assert.Equal(t, service.OfflineReasonCallNotInSnapshot, lines[4].Reason)

	recorder := httptest.NewRecorder()
	require.NoError(t, writeOfflineTrajectoryNDJSON(recorder, lines))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "application/x-ndjson", recorder.Header().Get("Content-Type"))

	body := recorder.Body.String()
	ndjsonLines := strings.Split(strings.TrimSpace(body), "\n")
	require.Len(t, ndjsonLines, 5)
	for _, line := range ndjsonLines {
		var decoded map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &decoded))
		assert.Contains(t, decoded, "call_id")
		assert.Contains(t, decoded, "status")
	}
	for _, forbidden := range []string{
		"raw_provider_request", "final_assistant_message", "authorization",
		"api_key", "Bearer ", "sse",
	} {
		assert.NotContains(t, body, forbidden)
	}
	// Exactly one result per deduplicated ID.
	seen := map[string]int{}
	for _, line := range lines {
		seen[line.CallID]++
	}
	assert.Equal(t, map[string]int{"C2": 1, "C1": 1, "C3": 1, "AY": 1, "ZX": 1}, seen)
}

func TestResolveOfflineTrajectories_RejectsMissingAuthAndWorkspace(t *testing.T) {
	h := &Handler{}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/env-dispatch/runs/x/offline-trajectories:resolve", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	h.ResolveOfflineTrajectories(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	req = newRequestAsUser("70000000-0000-4000-8000-000000000310", http.MethodPost, "/api/v1/env-dispatch/runs/x/offline-trajectories:resolve", map[string]any{
		"snapshot_id": "sha256:frozen", "call_ids": []string{"C1"},
	})
	// Authenticated but no workspace context.
	rec = httptest.NewRecorder()
	h.ResolveOfflineTrajectories(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "workspace")
}

func TestResolveOfflineTrajectories_RejectsInvalidBodyAndRunID(t *testing.T) {
	h := &Handler{}
	workspaceID := "70000000-0000-4000-8000-000000000311"
	userID := "70000000-0000-4000-8000-000000000312"

	req := newRequestAsUser(userID, http.MethodPost, "/api/v1/env-dispatch/runs/not-a-uuid/offline-trajectories:resolve", map[string]any{
		"snapshot_id": "sha256:frozen", "call_ids": []string{"C1"},
	})
	req = req.WithContext(middleware.SetMemberContext(req.Context(), workspaceID, db.Member{}))
	route := chi.NewRouteContext()
	route.URLParams.Add("runID", "not-a-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route))
	rec := httptest.NewRecorder()
	h.ResolveOfflineTrajectories(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	req = newRequestAsUser(userID, http.MethodPost, "/api/v1/env-dispatch/runs/70000000-0000-4000-8000-000000000313/offline-trajectories:resolve", map[string]any{
		"call_ids": []string{"C1"},
	})
	req = req.WithContext(middleware.SetMemberContext(req.Context(), workspaceID, db.Member{}))
	route = chi.NewRouteContext()
	route.URLParams.Add("runID", "70000000-0000-4000-8000-000000000313")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route))
	rec = httptest.NewRecorder()
	h.ResolveOfflineTrajectories(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "snapshot_id")
}

func TestWriteOfflineResolveError_MapsRequestLevelCodes(t *testing.T) {
	rec := httptest.NewRecorder()
	writeOfflineResolveError(rec, &service.OfflineResolveError{
		Code: "forbidden", Message: "workspace is not authorized for this run", Status: http.StatusForbidden,
	})
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "forbidden")

	rec = httptest.NewRecorder()
	writeOfflineResolveError(rec, &service.OfflineResolveError{
		Code: "snapshot_mismatch", Message: "snapshot_id does not match the frozen run snapshot", Status: http.StatusConflict,
	})
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "snapshot_mismatch")
}

func TestSanitizeOfflineResolveLine_DoesNotIntroduceRawFields(t *testing.T) {
	line := sanitizeOfflineResolveLine(service.OfflineResolveLine{
		CallID: "C1", Status: "trajectory",
		Trajectory: &service.NormalizedOfflineTrajectory{
			NormalizationVersion: "1",
			Messages:             []service.NormalizedMessage{{Role: "user", Blocks: []service.NormalizedBlock{{Type: "text", Text: "hi"}}}},
			Tools:                []service.NormalizedTool{},
			Output:               service.NormalizedMessage{Role: "assistant", Blocks: []service.NormalizedBlock{{Type: "text", Text: "yo"}}},
			Provider:             service.NormalizedProvider{Name: "synthetic", Model: "m", APIKind: "messages"},
			RequestHash:          "sha256:req", ResponseHash: "sha256:resp",
		},
	})
	body, err := json.Marshal(line)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "raw_provider_request")
	assert.NotContains(t, string(body), "final_assistant_message")
}

func serviceHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", sum[:])
}
