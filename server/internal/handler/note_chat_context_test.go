package handler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFormatPeriodBriefChatResidueNamesInsertedChild(t *testing.T) {
	got := formatPeriodBriefChatResidue(periodBriefChatResidue{
		RunID:         "run-1",
		Status:        "done",
		WindowLabel:   "2026-08-26→2026-09-01",
		DraftPageID:   "draft-1",
		DraftTitle:    "工作介绍 2026-08-26→2026-09-01",
		ResultPageID:  "child-1",
		ResultTitle:   "工作介绍 2026-08-26→2026-09-01",
		ResultMode:    "child",
		BriefMarkdown: "# 工作介绍 2026-W36\n\n## Summary\n成品正文",
	})
	for _, want := range []string{
		"<period_brief_residue>",
		"</period_brief_residue>",
		"run_id: run-1",
		"status: done",
		"window: 2026-08-26→2026-09-01",
		"inserted: child",
		"result_page_id: child-1",
		"result_page_title: 工作介绍 2026-08-26→2026-09-01",
		"notes get result_page_id",
		"<period_brief>",
		"成品正文",
		"</period_brief>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("residue missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "collector pack") || strings.Contains(got, "force_fresh") {
		t.Fatalf("residue leaked collect/synthesis internals:\n%s", got)
	}
}

func TestFormatPeriodBriefChatResidueUsesDraftWhenNotInserted(t *testing.T) {
	got := formatPeriodBriefChatResidue(periodBriefChatResidue{
		RunID:       "run-2",
		Status:      "awaiting_confirm",
		WindowLabel: "本周",
		DraftPageID: "draft-2",
		DraftTitle:  "工作介绍 本周",
	})
	for _, want := range []string{
		"status: awaiting_confirm",
		"inserted: no",
		"draft_page_id: draft-2",
		"notes get draft_page_id",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("residue missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "result_page_id:") {
		t.Fatalf("unpublished residue should not invent result_page_id:\n%s", got)
	}
}

func TestFormatPeriodBriefChatResidueKeepsBriefWhenResultDeleted(t *testing.T) {
	got := formatPeriodBriefChatResidue(periodBriefChatResidue{
		RunID:         "run-3",
		Status:        "done",
		WindowLabel:   "2026-W36",
		DraftPageID:   "draft-3",
		ResultMode:    "child",
		BriefMarkdown: "# 工作介绍 2026-W36\n\n删页后仍应看得到的成品",
	})
	for _, want := range []string{
		"inserted: deleted",
		"<period_brief>",
		"删页后仍应看得到的成品",
		"notes get draft_page_id",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("residue missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "result_page_id:") {
		t.Fatalf("deleted result must not keep a dead result_page_id:\n%s", got)
	}
}

func TestBuildNoteChatWakePrefixIncludesPeriodBriefResidue(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	sourcePageID := insertPeriodBriefFixtureDraft(t, "Source")
	draftID := insertPeriodBriefFixtureDraft(t, "工作介绍 本周")
	childID := insertPeriodBriefFixtureDraft(t, "工作介绍 2026-08-26→2026-09-01")
	synthID := createHandlerTestAgent(t, "Residue Synth "+uuid.NewString()[:8], nil)
	sessionID := createHandlerTestChatSession(t, synthID)

	if _, err := testPool.Exec(context.Background(), `
UPDATE chat_session SET context_note_page_id = $1 WHERE id = $2`, sourcePageID, sessionID); err != nil {
		t.Fatalf("bind session: %v", err)
	}

	folderID := insertPeriodBriefFixtureDraft(t, "工作介绍")
	insertPeriodBriefFixtureRun(t, sourcePageID, folderID, synthID, draftID, "done", time.Now().UTC())
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_period_brief_run
SET chat_session_id = $1, result_page_id = $2, result_mode = 'child', window_label = '2026-08-26→2026-09-01'
WHERE draft_page_id = $3`, sessionID, childID, draftID); err != nil {
		t.Fatalf("bind residue: %v", err)
	}

	prefix := testHandler.buildNoteChatWakePrefix(context.Background(), parseUUID(sessionID))
	for _, want := range []string{
		"<note_chat_context>",
		"context_note_page_id: " + sourcePageID,
		"<period_brief_residue>",
		"inserted: child",
		"result_page_id: " + childID,
		"notes get result_page_id",
	} {
		if !strings.Contains(prefix, want) {
			t.Fatalf("wake prefix missing %q:\n%s", want, prefix)
		}
	}
}

func TestBuildNoteChatWakePrefixOmitsResidueWithoutRun(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	sourcePageID := insertPeriodBriefFixtureDraft(t, "Source")
	synthID := createHandlerTestAgent(t, "No Residue "+uuid.NewString()[:8], nil)
	sessionID := createHandlerTestChatSession(t, synthID)
	if _, err := testPool.Exec(context.Background(), `
UPDATE chat_session SET context_note_page_id = $1 WHERE id = $2`, sourcePageID, sessionID); err != nil {
		t.Fatalf("bind session: %v", err)
	}

	prefix := testHandler.buildNoteChatWakePrefix(context.Background(), parseUUID(sessionID))
	if !strings.Contains(prefix, "<note_chat_context>") {
		t.Fatalf("expected note context:\n%s", prefix)
	}
	if strings.Contains(prefix, "<period_brief_residue>") {
		t.Fatalf("plain bubble should not invent residue:\n%s", prefix)
	}
}

func TestBuildNoteChatWakePrefixIncludesBriefWhenResultDeleted(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	sourcePageID := insertPeriodBriefFixtureDraft(t, "Source")
	draftID := insertPeriodBriefFixtureDraft(t, "工作介绍 底稿")
	childID := insertPeriodBriefFixtureDraft(t, "工作介绍 2026-W36")
	synthID := createHandlerTestAgent(t, "Deleted Result "+uuid.NewString()[:8], nil)
	sessionID := createHandlerTestChatSession(t, synthID)
	if _, err := testPool.Exec(context.Background(), `
UPDATE chat_session SET context_note_page_id = $1 WHERE id = $2`, sourcePageID, sessionID); err != nil {
		t.Fatalf("bind session: %v", err)
	}
	folderID := insertPeriodBriefFixtureDraft(t, "工作介绍")
	insertPeriodBriefFixtureRun(t, sourcePageID, folderID, synthID, draftID, "done", time.Now().UTC())
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_period_brief_run
SET chat_session_id = $1, result_page_id = $2, result_mode = 'child',
    result_markdown = $3, window_label = '2026-W36'
WHERE draft_page_id = $4`, sessionID, childID, "# 工作介绍 2026-W36\n\n活着的成品", draftID); err != nil {
		t.Fatalf("bind residue: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_page SET deleted_at = now() WHERE id = $1`, childID); err != nil {
		t.Fatalf("delete result: %v", err)
	}

	prefix := testHandler.buildNoteChatWakePrefix(context.Background(), parseUUID(sessionID))
	for _, want := range []string{
		"<period_brief_residue>",
		"inserted: deleted",
		"<period_brief>",
		"活着的成品",
		"draft_page_id: " + draftID,
	} {
		if !strings.Contains(prefix, want) {
			t.Fatalf("wake prefix missing %q:\n%s", want, prefix)
		}
	}
	if strings.Contains(prefix, "result_page_id: "+childID) {
		t.Fatalf("deleted result must not stay in residue:\n%s", prefix)
	}
}
