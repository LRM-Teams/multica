package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// change_reason is per-revision metadata: a provided reason lands on the live
// row and in that revision's immutable snapshot, and the next revision without
// a reason resets it instead of inheriting the previous one.
func TestChannelGoalChangeReasonRecordedInRevisions(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	channel := createGoalTestChannel(t)

	created := httptest.NewRecorder()
	testHandler.CreateChannelGoal(created, goalRequest(t, testUserID, http.MethodPost, channel.ID, map[string]any{
		"title":            "Reasoned goal",
		"objective":        "Track why requirements change",
		"success_criteria": []string{"History carries reasons"},
	}))
	if created.Code != http.StatusCreated {
		t.Fatalf("CreateChannelGoal = %d: %s", created.Code, created.Body.String())
	}
	goal := decodeGoalEnvelope(t, created).Goal

	revised := httptest.NewRecorder()
	testHandler.UpdateChannelGoal(revised, goalRequest(t, testUserID, http.MethodPatch, channel.ID, map[string]any{
		"expected_version": goal.Version,
		"objective":        "Track why requirements change, in history",
		"change_reason":    "  customer dropped the export requirement  ",
	}))
	if revised.Code != http.StatusOK {
		t.Fatalf("update with change_reason = %d: %s", revised.Code, revised.Body.String())
	}
	revisedGoal := decodeGoalEnvelope(t, revised).Goal
	if revisedGoal.ChangeReason != "customer dropped the export requirement" {
		t.Fatalf("live change_reason = %q, want trimmed reason", revisedGoal.ChangeReason)
	}

	var snapshotReason string
	if err := testPool.QueryRow(ctx, `
		SELECT change_reason FROM channel_goal_revision
		WHERE goal_id = $1 AND version = $2`, parseUUID(goal.ID), revisedGoal.Version).Scan(&snapshotReason); err != nil {
		t.Fatalf("load revision snapshot: %v", err)
	}
	if snapshotReason != "customer dropped the export requirement" {
		t.Fatalf("revision snapshot change_reason = %q", snapshotReason)
	}

	// A later revision without a reason must not inherit the old one.
	unreasoned := httptest.NewRecorder()
	testHandler.UpdateChannelGoal(unreasoned, goalRequest(t, testUserID, http.MethodPatch, channel.ID, map[string]any{
		"expected_version": revisedGoal.Version,
		"current_step":     "next step",
	}))
	if unreasoned.Code != http.StatusOK {
		t.Fatalf("update without change_reason = %d: %s", unreasoned.Code, unreasoned.Body.String())
	}
	if got := decodeGoalEnvelope(t, unreasoned).Goal.ChangeReason; got != "" {
		t.Fatalf("change_reason carried forward as %q, want reset to empty", got)
	}

	tooLong := httptest.NewRecorder()
	testHandler.UpdateChannelGoal(tooLong, goalRequest(t, testUserID, http.MethodPatch, channel.ID, map[string]any{
		"expected_version": revisedGoal.Version + 1,
		"current_step":     "another step",
		"change_reason":    strings.Repeat("x", 2001),
	}))
	if tooLong.Code != http.StatusBadRequest {
		t.Fatalf("oversized change_reason = %d, want 400", tooLong.Code)
	}
}
