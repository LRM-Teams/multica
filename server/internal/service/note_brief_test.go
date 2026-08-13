package service

import (
	"encoding/json"
	"testing"
)

func TestWithNoteBriefPreservesExistingKeys(t *testing.T) {
	t.Parallel()
	merged, err := WithNoteBrief([]byte(`{"squad_id":"s1"}`), NoteBrief{
		Version: noteBriefContextVersion,
		PageID:  "page-1",
		Title:   "Brief",
	})
	if err != nil {
		t.Fatalf("WithNoteBrief: %v", err)
	}
	var contextMap map[string]json.RawMessage
	if err := json.Unmarshal(merged, &contextMap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(contextMap["squad_id"]) != `"s1"` {
		t.Fatalf("squad_id lost: %s", contextMap["squad_id"])
	}
	brief, ok, err := NoteBriefFromContext(merged)
	if err != nil || !ok {
		t.Fatalf("NoteBriefFromContext: ok=%v err=%v", ok, err)
	}
	if brief.PageID != "page-1" || brief.Title != "Brief" || brief.Version != 1 {
		t.Fatalf("brief = %#v", brief)
	}
}

func TestWithNoteBriefRejectsEmptyPageID(t *testing.T) {
	t.Parallel()
	if _, err := WithNoteBrief(nil, NoteBrief{Version: 1, PageID: "  "}); err == nil {
		t.Fatal("expected empty page_id error")
	}
}

func TestNoteBriefFromContextAbsent(t *testing.T) {
	t.Parallel()
	if _, ok, err := NoteBriefFromContext([]byte(`{"execution_config":{}}`)); err != nil || ok {
		t.Fatalf("absent brief: ok=%v err=%v", ok, err)
	}
}

func TestNoteBriefFromContextInvalidFailsClosed(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"note_brief":{"version":1,"page_id":""}}`)
	_, ok, err := NoteBriefFromContext(raw)
	if !ok || err == nil {
		t.Fatalf("invalid brief: ok=%v err=%v", ok, err)
	}
}
