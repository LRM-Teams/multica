package handler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/cli"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestRuntimeToResponseWithReleaseShowsUpdateAvailable(t *testing.T) {
	rt := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "v0.3.0"})

	resp := runtimeToResponseWithUpdateAndRelease(rt, nil, &RuntimeRelease{TagName: "v0.3.1"})

	if resp.RuntimeHealth != "update_available" {
		t.Fatalf("runtime_health = %q, want update_available", resp.RuntimeHealth)
	}
	if resp.TargetVersion == nil || *resp.TargetVersion != "v0.3.1" {
		t.Fatalf("target_version = %v, want v0.3.1", resp.TargetVersion)
	}
	if resp.UpdateState != "idle" {
		t.Fatalf("update_state = %q, want idle", resp.UpdateState)
	}
}

func TestAttachDaemonTargetVersionsPrefersAvailableReleaseOverFailedSibling(t *testing.T) {
	daemonID := "daemon-s144"
	currentTarget := "v0.4.14"
	staleTarget := "v0.4.13"
	responses := []AgentRuntimeResponse{
		{
			DaemonID:      &daemonID,
			RuntimeHealth: "update_available",
			TargetVersion: &currentTarget,
		},
		{
			DaemonID:      &daemonID,
			RuntimeHealth: "failed",
			TargetVersion: &staleTarget,
		},
	}

	attachDaemonTargetVersions(responses)
	for i, response := range responses {
		if response.DaemonTargetVersion == nil || *response.DaemonTargetVersion != currentTarget {
			t.Fatalf("response %d daemon_target_version = %v, want %q", i, response.DaemonTargetVersion, currentTarget)
		}
	}
}

func TestRuntimeToResponseWithReleaseFailsClosedForInvalidSources(t *testing.T) {
	tests := []struct {
		name    string
		runtime db.AgentRuntime
		release *RuntimeRelease
		update  *UpdateRequest
		want    string
		wantTgt bool
	}{
		{
			name:    "no release",
			runtime: runtimeHealthTestRuntime(t, map[string]any{"cli_version": "v0.3.0"}),
			want:    "ok",
		},
		{
			name:    "same version",
			runtime: runtimeHealthTestRuntime(t, map[string]any{"cli_version": "v0.3.1"}),
			release: &RuntimeRelease{TagName: "v0.3.1"},
			want:    "ok",
		},
		{
			name:    "dev build",
			runtime: runtimeHealthTestRuntime(t, map[string]any{"cli_version": "v0.3.0-5-gabc1234"}),
			release: &RuntimeRelease{TagName: "v0.3.1"},
			want:    "ok",
		},
		{
			name:    "desktop managed",
			runtime: runtimeHealthTestRuntime(t, map[string]any{"cli_version": "v0.3.0", "launched_by": "desktop"}),
			release: &RuntimeRelease{TagName: "v0.3.1"},
			want:    "ok",
		},
		{
			name:    "cloud runtime",
			runtime: runtimeHealthTestRuntimeWithMode(t, "cloud", map[string]any{"cli_version": "v0.3.0"}),
			release: &RuntimeRelease{TagName: "v0.3.1"},
			want:    "ok",
		},
		{
			name:    "active update wins",
			runtime: runtimeHealthTestRuntime(t, map[string]any{"cli_version": "v0.3.0"}),
			release: &RuntimeRelease{TagName: "v0.3.2"},
			update: &UpdateRequest{
				ID:            "update-1",
				RuntimeID:     "runtime-1",
				TargetVersion: "v0.3.1",
				Status:        UpdateRunning,
			},
			want:    "updating",
			wantTgt: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := runtimeToResponseWithUpdateAndRelease(tt.runtime, tt.update, tt.release)
			if resp.RuntimeHealth != tt.want {
				t.Fatalf("runtime_health = %q, want %q", resp.RuntimeHealth, tt.want)
			}
			if tt.wantTgt {
				if resp.TargetVersion == nil || *resp.TargetVersion != tt.update.TargetVersion {
					t.Fatalf("target_version = %v, want %q", resp.TargetVersion, tt.update.TargetVersion)
				}
			} else if resp.TargetVersion != nil {
				t.Fatalf("target_version = %v, want nil", *resp.TargetVersion)
			}
		})
	}
}

func TestRuntimeToResponseCompletedUpdateWaitsAsUpdating(t *testing.T) {
	rt := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "v0.3.0"})
	update := &UpdateRequest{
		ID:            "update-1",
		RuntimeID:     "runtime-1",
		TargetVersion: "v0.3.1",
		Status:        UpdateCompleted,
	}

	resp := runtimeToResponseWithUpdateAndRelease(rt, update, &RuntimeRelease{TagName: "v0.3.2"})
	if resp.RuntimeHealth != "updating" {
		t.Fatalf("runtime_health = %q, want updating", resp.RuntimeHealth)
	}
	if resp.TargetVersion == nil || *resp.TargetVersion != "v0.3.1" {
		t.Fatalf("target_version = %v, want v0.3.1", resp.TargetVersion)
	}
}

func TestRuntimeToResponseCompletedUpdateTimesOutWithoutTargetRegister(t *testing.T) {
	rt := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "v0.3.0"})
	update := &UpdateRequest{
		ID:            "update-1",
		RuntimeID:     "runtime-1",
		TargetVersion: "v0.3.1",
		Status:        UpdateCompleted,
		UpdatedAt:     time.Now().Add(-(updateConfirmTimeout + time.Second)),
	}

	resp := runtimeToResponseWithUpdateAndRelease(rt, update, &RuntimeRelease{TagName: "v0.3.1"})
	if resp.RuntimeHealth != "failed" {
		t.Fatalf("runtime_health = %q, want failed", resp.RuntimeHealth)
	}
	if resp.UpdateState != "timed_out" {
		t.Fatalf("update_state = %q, want timed_out", resp.UpdateState)
	}
	if resp.UpdateError == nil || *resp.UpdateError != "old_version_reported_after_update" {
		t.Fatalf("update_error = %v, want old_version_reported_after_update", resp.UpdateError)
	}
	if resp.TargetVersion == nil || *resp.TargetVersion != "v0.3.1" {
		t.Fatalf("target_version = %v, want v0.3.1", resp.TargetVersion)
	}
}

func TestRuntimeToResponseCompletedUpdateDoesNotTimeOutAfterTargetRegister(t *testing.T) {
	rt := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "v0.3.1"})
	update := &UpdateRequest{
		ID:            "update-1",
		RuntimeID:     "runtime-1",
		TargetVersion: "v0.3.1",
		Status:        UpdateCompleted,
		UpdatedAt:     time.Now().Add(-(updateConfirmTimeout + time.Second)),
	}

	resp := runtimeToResponseWithUpdateAndRelease(rt, update, &RuntimeRelease{TagName: "v0.3.1"})
	if resp.RuntimeHealth != "ok" {
		t.Fatalf("runtime_health = %q, want ok", resp.RuntimeHealth)
	}
	if resp.UpdateState != "completed" {
		t.Fatalf("update_state = %q, want completed", resp.UpdateState)
	}
	if resp.UpdateError != nil {
		t.Fatalf("update_error = %v, want nil", *resp.UpdateError)
	}
}

func TestRuntimeToResponseRetainedFailedUpdateDoesNotMaskTargetRegister(t *testing.T) {
	rt := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "v0.3.1"})
	update := &UpdateRequest{
		ID:            "update-1",
		RuntimeID:     "runtime-1",
		TargetVersion: "v0.3.1",
		Status:        UpdateFailed,
		Error:         "binary_version_mismatch_after_update",
		UpdatedAt:     time.Now().Add(-time.Hour),
	}

	resp := runtimeToResponseWithUpdateAndRelease(rt, update, &RuntimeRelease{TagName: "v0.3.1"})
	if resp.RuntimeHealth != "ok" {
		t.Fatalf("runtime_health = %q, want ok", resp.RuntimeHealth)
	}
	if resp.UpdateState != "completed" {
		t.Fatalf("update_state = %q, want completed", resp.UpdateState)
	}
	if resp.UpdateError != nil {
		t.Fatalf("update_error = %v, want nil", *resp.UpdateError)
	}
}

func TestRuntimeToResponseCompletedMatchingUpdateCanOfferNextRelease(t *testing.T) {
	rt := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "v0.3.1"})
	update := &UpdateRequest{
		ID:            "update-1",
		RuntimeID:     "runtime-1",
		TargetVersion: "v0.3.1",
		Status:        UpdateCompleted,
	}

	resp := runtimeToResponseWithUpdateAndRelease(rt, update, &RuntimeRelease{TagName: "v0.3.2"})
	if resp.RuntimeHealth != "update_available" {
		t.Fatalf("runtime_health = %q, want update_available", resp.RuntimeHealth)
	}
	if resp.TargetVersion == nil || *resp.TargetVersion != "v0.3.2" {
		t.Fatalf("target_version = %v, want v0.3.2", resp.TargetVersion)
	}
}

func TestRuntimeListCoalescesUpdateStateByDaemon(t *testing.T) {
	now := time.Now()
	rtA := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "0.3.35"})
	rtA.ID = parseUUID("11111111-1111-1111-1111-111111111111")
	rtA.DaemonID = pgtype.Text{String: "daemon-one", Valid: true}
	rtB := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "0.3.35"})
	rtB.ID = parseUUID("22222222-2222-2222-2222-222222222222")
	rtB.DaemonID = pgtype.Text{String: "daemon-one", Valid: true}
	rtC := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "0.3.35"})
	rtC.ID = parseUUID("33333333-3333-3333-3333-333333333333")
	rtC.DaemonID = pgtype.Text{String: "daemon-two", Valid: true}
	update := &UpdateRequest{
		ID:            "update-one",
		RuntimeID:     uuidToString(rtA.ID),
		TargetVersion: "v0.3.36",
		Status:        UpdateCompleted,
		CreatedAt:     now.Add(-time.Minute),
		UpdatedAt:     now,
	}

	resolved := coalesceRuntimeUpdatesByDaemon([]db.AgentRuntime{rtA, rtB, rtC}, map[string]*UpdateRequest{
		uuidToString(rtA.ID): update,
	})

	if resolved[uuidToString(rtB.ID)] != update {
		t.Fatalf("sibling runtime update = %#v, want daemon update %#v", resolved[uuidToString(rtB.ID)], update)
	}
	resp := runtimeToResponseWithUpdateAndRelease(rtB, resolved[uuidToString(rtB.ID)], &RuntimeRelease{TagName: "v0.3.36"})
	if resp.RuntimeHealth != "updating" {
		t.Fatalf("sibling runtime_health = %q, want updating", resp.RuntimeHealth)
	}
	if resp.UpdateState != "completed" {
		t.Fatalf("sibling update_state = %q, want completed", resp.UpdateState)
	}
	if resp.TargetVersion == nil || *resp.TargetVersion != "v0.3.36" {
		t.Fatalf("sibling target_version = %v, want v0.3.36", resp.TargetVersion)
	}

	if resolved[uuidToString(rtC.ID)] != nil {
		t.Fatalf("other daemon update = %#v, want nil", resolved[uuidToString(rtC.ID)])
	}
	other := runtimeToResponseWithUpdateAndRelease(rtC, resolved[uuidToString(rtC.ID)], &RuntimeRelease{TagName: "v0.3.36"})
	if other.RuntimeHealth != "update_available" {
		t.Fatalf("other daemon runtime_health = %q, want update_available", other.RuntimeHealth)
	}
}

func TestRuntimeListCoalescesNewestUpdateByDaemon(t *testing.T) {
	now := time.Now()
	rtA := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "0.3.35"})
	rtA.ID = parseUUID("11111111-1111-1111-1111-111111111111")
	rtA.DaemonID = pgtype.Text{String: "daemon-one", Valid: true}
	rtB := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "0.3.35"})
	rtB.ID = parseUUID("22222222-2222-2222-2222-222222222222")
	rtB.DaemonID = pgtype.Text{String: "daemon-one", Valid: true}
	oldUpdate := &UpdateRequest{
		ID:            "old-update",
		RuntimeID:     uuidToString(rtA.ID),
		TargetVersion: "v0.3.35",
		Status:        UpdateCompleted,
		CreatedAt:     now.Add(-2 * time.Minute),
		UpdatedAt:     now.Add(-time.Minute),
	}
	newUpdate := &UpdateRequest{
		ID:            "new-update",
		RuntimeID:     uuidToString(rtB.ID),
		TargetVersion: "v0.3.36",
		Status:        UpdateRunning,
		CreatedAt:     now.Add(-30 * time.Second),
		UpdatedAt:     now,
	}

	resolved := coalesceRuntimeUpdatesByDaemon([]db.AgentRuntime{rtA, rtB}, map[string]*UpdateRequest{
		uuidToString(rtA.ID): oldUpdate,
		uuidToString(rtB.ID): newUpdate,
	})

	if resolved[uuidToString(rtA.ID)] != newUpdate {
		t.Fatalf("runtime A update = %#v, want newest daemon update %#v", resolved[uuidToString(rtA.ID)], newUpdate)
	}
	if resolved[uuidToString(rtB.ID)] != newUpdate {
		t.Fatalf("runtime B update = %#v, want newest daemon update %#v", resolved[uuidToString(rtB.ID)], newUpdate)
	}
}

func TestRuntimeReleaseFromManifestRequiresStableReleaseWithPlatforms(t *testing.T) {
	valid := cli.ReleaseManifest{
		TagName: "v0.3.1",
		Platforms: map[string]cli.ReleaseAsset{
			"darwin-arm64": {URL: "https://example/multica-cli-0.3.1-darwin-arm64.tar.gz", SHA256: "deadbeef"},
		},
	}
	if release, err := runtimeReleaseFromManifest(&valid); err != nil || release == nil || release.TagName != "v0.3.1" {
		t.Fatalf("runtimeReleaseFromManifest(valid) = %+v err=%v, want v0.3.1", release, err)
	}

	for _, manifest := range []cli.ReleaseManifest{
		{TagName: "v0.3.1-beta.1", Platforms: valid.Platforms},
		{TagName: "v0.3.1", Platforms: map[string]cli.ReleaseAsset{}},
	} {
		if got, err := runtimeReleaseFromManifest(&manifest); err == nil || got != nil {
			t.Fatalf("runtimeReleaseFromManifest(%+v) = %+v err=%v, want error", manifest, got, err)
		}
	}
}

func runtimeHealthTestRuntime(t *testing.T, metadata map[string]any) db.AgentRuntime {
	t.Helper()
	return runtimeHealthTestRuntimeWithMode(t, "local", metadata)
}

func runtimeHealthTestRuntimeWithMode(t *testing.T, mode string, metadata map[string]any) db.AgentRuntime {
	t.Helper()
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	return db.AgentRuntime{
		ID:          parseUUID("11111111-1111-1111-1111-111111111111"),
		WorkspaceID: parseUUID("22222222-2222-2222-2222-222222222222"),
		Name:        "Runtime health test",
		RuntimeMode: mode,
		Provider:    "claude",
		Status:      "online",
		DeviceInfo:  "test-device",
		Metadata:    data,
		LastSeenAt:  now,
		CreatedAt:   now,
		UpdatedAt:   now,
		Visibility:  "private",
	}
}

func TestRuntimeToResponseRestartPendingSurfacesStagedWaitingMessage(t *testing.T) {
	rt := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "v0.3.99"})
	// Offline after staged restart — must not hide the staged window behind a bare old version.
	rt.Status = "offline"
	rt.LastSeenAt = pgtype.Timestamptz{}
	target := "v0.4.0"
	obs := &DaemonUpdateStatusResponse{
		Phase:         "restart_pending",
		LastOutcome:   "update_succeeded",
		TargetVersion: &target,
	}
	update := &UpdateRequest{
		ID:            "upd-1",
		RuntimeID:     "runtime-1",
		TargetVersion: "v0.4.0",
		Status:        UpdateReady,
	}
	resp := runtimeToResponseWithUpdateReleaseAndObservation(rt, update, nil, obs)
	if resp.UpdateState != "ready_to_apply" {
		t.Fatalf("update_state = %q, want ready_to_apply", resp.UpdateState)
	}
	if resp.TargetVersion == nil || *resp.TargetVersion != "v0.4.0" {
		t.Fatalf("target_version = %v, want v0.4.0", resp.TargetVersion)
	}
	if resp.UpdateError == nil || *resp.UpdateError != "update_staged_waiting_restart" {
		t.Fatalf("update_error = %v, want update_staged_waiting_restart", resp.UpdateError)
	}
	// Offline connectivity still wins health (do not claim online).
	if resp.RuntimeHealth != "offline" {
		t.Fatalf("runtime_health = %q, want offline", resp.RuntimeHealth)
	}
}

func TestRuntimeToResponseAutoUpdateSucceededWithoutRowStillStages(t *testing.T) {
	rt := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "0.3.99"})
	target := "v0.4.0"
	obs := &DaemonUpdateStatusResponse{
		Phase:         "restart_pending",
		LastOutcome:   "update_succeeded",
		TargetVersion: &target,
	}
	resp := runtimeToResponseWithUpdateReleaseAndObservation(rt, nil, nil, obs)
	if resp.UpdateState != "ready_to_apply" {
		t.Fatalf("update_state = %q, want ready_to_apply", resp.UpdateState)
	}
	if resp.TargetVersion == nil || *resp.TargetVersion != "v0.4.0" {
		t.Fatalf("target_version = %v", resp.TargetVersion)
	}
	if resp.UpdateError == nil || *resp.UpdateError != "update_staged_waiting_restart" {
		t.Fatalf("update_error = %v", resp.UpdateError)
	}
}

func TestRuntimeToResponseExposesOfflineReason(t *testing.T) {
	rt := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "v0.3.99"})
	rt.Status = "offline"
	rt.OfflineReason = pgtype.Text{String: "daemon_deregistered", Valid: true}
	resp := runtimeToResponseWithUpdateAndRelease(rt, nil, nil)
	if resp.OfflineReason == nil || *resp.OfflineReason != "daemon_deregistered" {
		t.Fatalf("offline_reason = %v, want daemon_deregistered", resp.OfflineReason)
	}
}

func TestRuntimeToResponseNoStagedMessageWhenAlreadyOnTarget(t *testing.T) {
	rt := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "v0.4.0"})
	target := "v0.4.0"
	obs := &DaemonUpdateStatusResponse{
		Phase:         "restart_pending",
		LastOutcome:   "update_succeeded",
		TargetVersion: &target,
	}
	resp := runtimeToResponseWithUpdateReleaseAndObservation(rt, nil, nil, obs)
	if resp.UpdateError != nil {
		t.Fatalf("update_error = %v, want nil when already on target", *resp.UpdateError)
	}
}

func TestRuntimeToResponseEqualTargetDoesNotOfferUpgrade(t *testing.T) {
	// P0: current 0.4.0 + stale completed/ready target 0.4.0 must not light upgrade.
	rt := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "0.4.0"})
	update := &UpdateRequest{
		ID:            "update-1",
		RuntimeID:     "runtime-1",
		TargetVersion: "v0.4.0",
		Status:        UpdateCompleted,
	}
	// latest still 0.4.0 (stale cache) or equal — must not show upgrade to same version
	resp := runtimeToResponseWithUpdateAndRelease(rt, update, &RuntimeRelease{TagName: "v0.4.0"})
	if resp.RuntimeHealth != "ok" {
		t.Fatalf("runtime_health = %q, want ok", resp.RuntimeHealth)
	}
	if resp.TargetVersion != nil {
		t.Fatalf("target_version = %v, want nil", *resp.TargetVersion)
	}
}

func TestRuntimeToResponseEqualTargetOffersNewerLatest(t *testing.T) {
	rt := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "0.4.0"})
	update := &UpdateRequest{
		ID:            "update-1",
		RuntimeID:     "runtime-1",
		TargetVersion: "v0.4.0",
		Status:        UpdateCompleted,
	}
	resp := runtimeToResponseWithUpdateAndRelease(rt, update, &RuntimeRelease{TagName: "v0.4.1"})
	if resp.RuntimeHealth != "update_available" {
		t.Fatalf("runtime_health = %q, want update_available", resp.RuntimeHealth)
	}
	if resp.TargetVersion == nil || *resp.TargetVersion != "v0.4.1" {
		t.Fatalf("target_version = %v, want v0.4.1", resp.TargetVersion)
	}
}

func TestRuntimeToResponseReadyToApplyEqualCurrentClearsUpgrade(t *testing.T) {
	// ready_to_apply with current already on target (should collapse to completed then clear)
	rt := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "0.4.0"})
	update := &UpdateRequest{
		ID:            "update-1",
		RuntimeID:     "runtime-1",
		TargetVersion: "0.4.0",
		Status:        UpdateReady,
	}
	resp := runtimeToResponseWithUpdateAndRelease(rt, update, &RuntimeRelease{TagName: "0.4.0"})
	if resp.RuntimeHealth != "ok" {
		t.Fatalf("runtime_health = %q, want ok", resp.RuntimeHealth)
	}
	if resp.TargetVersion != nil {
		t.Fatalf("target_version = %v, want nil", *resp.TargetVersion)
	}
}
