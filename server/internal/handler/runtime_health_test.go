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

func TestRuntimeReleaseFromGitHubRequiresStableChecksummedCLIRelease(t *testing.T) {
	valid := cli.GitHubRelease{
		TagName: "v0.3.1",
		Assets: []cli.GitHubReleaseAsset{
			{Name: cli.ChecksumManifestName},
			{Name: "multica-cli-0.3.1-darwin-arm64.tar.gz"},
		},
	}
	if release, err := runtimeReleaseFromGitHub(&valid); err != nil || release == nil || release.TagName != "v0.3.1" {
		t.Fatalf("runtimeReleaseFromGitHub(valid) = %+v err=%v, want v0.3.1", release, err)
	}

	for _, release := range []cli.GitHubRelease{
		{TagName: "v0.3.1-beta.1", Assets: valid.Assets},
		{TagName: "v0.3.1", Assets: []cli.GitHubReleaseAsset{{Name: "multica-cli-0.3.1-darwin-arm64.tar.gz"}}},
		{TagName: "v0.3.1", Assets: []cli.GitHubReleaseAsset{{Name: cli.ChecksumManifestName}}},
	} {
		if got, err := runtimeReleaseFromGitHub(&release); err == nil || got != nil {
			t.Fatalf("runtimeReleaseFromGitHub(%+v) = %+v err=%v, want error", release, got, err)
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
