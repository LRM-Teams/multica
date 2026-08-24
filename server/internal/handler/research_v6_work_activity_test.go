package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

type researchV6WorkActivityRunStub struct {
	researchrun.ResearchRun
	researchrun.V6WorkActivityReader
	receivedRunID  string
	receivedWorkID string
}

func (s *researchV6WorkActivityRunStub) ProjectionV6WorkActivity(_ context.Context, _, runID, workItemID string) (researchrun.V6WorkActivity, error) {
	s.receivedRunID = runID
	s.receivedWorkID = workItemID
	return researchrun.V6WorkActivity{
		WorkItemID:  workItemID,
		AttemptID:   "00000000-0000-4000-8000-000000000213",
		AgentID:     "00000000-0000-4000-8000-000000000009",
		AgentName:   "深读手",
		InboxTaskID: "00000000-0000-4000-8000-000000000214",
		Mission:     "核查浏览器兼容性",
		Status:      "running",
		UpdatedAt:   time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC),
		Timeline: []researchrun.V6WorkActivityTimelineRow{
			{
				ID:         "00000000-0000-4000-8000-000000000215",
				OccurredAt: time.Date(2026, 8, 21, 0, 1, 0, 0, time.UTC),
			},
		},
	}, nil
}

func TestGetResearchV6WorkActivityReturnsExecutionIdentity(t *testing.T) {
	const (
		runID  = "1855232b-bb5f-49cf-9c7a-1348bc34f3c9"
		workID = "08234d35-5a10-4b24-80ab-2f9d6b8965d3"
	)
	service := &researchV6WorkActivityRunStub{}
	h := &Handler{ResearchRun: service}
	router := chi.NewRouter()
	router.Get("/api/research/v6/runs/{runId}/work-items/{workItemId}/activity", h.GetResearchV6WorkActivity)
	req := newRequest(http.MethodGet, "/api/research/v6/runs/"+runID+"/work-items/"+workID+"/activity", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if service.receivedRunID != runID || service.receivedWorkID != workID {
		t.Fatalf("received run=%q work=%q", service.receivedRunID, service.receivedWorkID)
	}
	for _, want := range []string{`"agent_name":"深读手"`, `"inbox_task_id":"00000000-0000-4000-8000-000000000214"`, `"mission":"核查浏览器兼容性"`, `"timeline":[{"id":"00000000-0000-4000-8000-000000000215"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("body missing %s: %s", want, w.Body.String())
		}
	}
}

func TestGetResearchV6WorkActivityRejectsInvalidWorkItemID(t *testing.T) {
	service := &researchV6WorkActivityRunStub{}
	h := &Handler{ResearchRun: service}
	req := withURLParams(
		newRequest(http.MethodGet, "/api/research/v6/runs/1855232b-bb5f-49cf-9c7a-1348bc34f3c9/work-items/not-a-uuid/activity", nil),
		"runId", "1855232b-bb5f-49cf-9c7a-1348bc34f3c9",
		"workItemId", "not-a-uuid",
	)
	w := httptest.NewRecorder()

	h.GetResearchV6WorkActivity(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if service.receivedWorkID != "" {
		t.Fatalf("invalid work item reached service: %q", service.receivedWorkID)
	}
}
