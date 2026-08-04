package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

func TestWriteResearchNodeCommandDenied(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeResearchNodeCommandDenied(recorder, researchrun.DenyNodeCommand(
		researchrun.NodeCmdCodeStateVersionConflict, "画布已更新，请刷新后重试"))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d", recorder.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["machine_code"] != researchrun.NodeCmdCodeStateVersionConflict {
		t.Fatalf("machine_code=%v", body["machine_code"])
	}
	if body["message_key"] != researchrun.NodeCmdCodeStateVersionConflict {
		t.Fatalf("message_key=%v", body["message_key"])
	}
	msg, _ := body["message"].(string)
	if msg == "" || strings.Contains(strings.ToLower(msg), "state_version") {
		t.Fatalf("message must be Chinese product text, got %q", msg)
	}
}

func TestResearchProjectedNodeIDStable(t *testing.T) {
	session := uuid.NewString()
	qid := uuid.NewString()
	a := researchProjectedNodeID(session, "question", qid)
	b := researchProjectedNodeID(session, "question", qid)
	if a != b {
		t.Fatalf("projected ids unstable: %s vs %s", a, b)
	}
	if a == researchProjectedNodeID(session, "task", qid) {
		t.Fatal("question/task projections must differ")
	}
}

func TestFillAnchorFromPayload(t *testing.T) {
	anchor := researchNodeAnchor{Kind: "legacy"}
	fillAnchorFromPayload(&anchor, json.RawMessage(`{
		"question_id":"q-1",
		"details":{"task_id":"t-1"}
	}`))
	if anchor.QuestionID != "q-1" || anchor.TaskID != "t-1" {
		t.Fatalf("anchor=%+v", anchor)
	}
}

func TestDecodeResearchNodeCommandRejectsUnknownFields(t *testing.T) {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/commands", strings.NewReader(`{"action":"continue","client_request_id":"r1","extra":true}`))
	var got researchNodeCommandRequest
	if decodeResearchJSON(recorder, req, &got) {
		t.Fatal("expected unknown field rejection")
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", recorder.Code)
	}
}
