// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// The judge endpoint accepts a valid recall report and kicks the async flow
// (202); with no judge service wired it accepts and drops silently.
func TestReportGraphMemoryJudge_AcceptsAndDropsWithoutService(t *testing.T) {
	h := &Handler{}
	payload, _ := json.Marshal(protocol.GraphMemoryJudgeKickPayload{
		TraceID: "trace-1",
		TaskID:  "task-1",
		Query:   "why retries?",
		Rounds:  2,
		Version: 1,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/daemon/graph-memory/judge", strings.NewReader(string(payload)))
	rec := httptest.NewRecorder()
	h.ReportGraphMemoryJudge(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp["status"] != "dropped" {
		t.Fatalf("response = %s, err %v; want status=dropped", rec.Body.String(), err)
	}
}

// A report without trace_id/task_id/query is rejected.
func TestReportGraphMemoryJudge_ValidatesRequiredFields(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/api/daemon/graph-memory/judge", strings.NewReader(`{"trace_id":"t"}`))
	rec := httptest.NewRecorder()
	h.ReportGraphMemoryJudge(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
