package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestCachedRuntimeReleaseSourceUsesServerDispatchedBaseURL(t *testing.T) {
	manifest := cli.ReleaseManifest{
		TagName: "v0.4.3",
		Version: "0.4.3",
		Platforms: map[string]cli.ReleaseAsset{
			"linux-amd64": {
				URL:    "https://example.test/releases/v0.4.3/multica-cli-0.4.3-linux-amd64.tar.gz",
				SHA256: "deadbeef",
			},
		},
	}
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metainfo.json" {
			t.Errorf("release feed path = %q, want /metainfo.json", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(cli.ReleaseMetainfo{
			SchemaVersion: 1,
			Environments: map[string]cli.ReleaseManifest{
				"production": manifest,
			},
		}); err != nil {
			t.Errorf("encode release metainfo: %v", err)
		}
	}))
	defer feed.Close()

	t.Setenv("MULTICA_SERVER_RELEASE_MANIFEST_BASE_URL", feed.URL)
	t.Setenv("APP_ENV", "production")
	source := NewCachedRuntimeReleaseSource(time.Minute)
	release, err := source.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if release == nil || release.TagName != "v0.4.3" {
		t.Fatalf("Latest() = %+v, want v0.4.3", release)
	}

	behind := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "0.4.2"})
	behindResponse := runtimeToResponseWithUpdateAndRelease(behind, nil, release)
	if behindResponse.RuntimeHealth != "update_available" {
		t.Fatalf("0.4.2 runtime_health = %q, want update_available", behindResponse.RuntimeHealth)
	}
	if behindResponse.TargetVersion == nil || *behindResponse.TargetVersion != "v0.4.3" {
		t.Fatalf("0.4.2 target_version = %v, want v0.4.3", behindResponse.TargetVersion)
	}

	current := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "0.4.3"})
	currentResponse := runtimeToResponseWithUpdateAndRelease(current, nil, release)
	if currentResponse.RuntimeHealth != "ok" {
		t.Fatalf("0.4.3 runtime_health = %q, want ok", currentResponse.RuntimeHealth)
	}
	if currentResponse.TargetVersion != nil {
		t.Fatalf("0.4.3 target_version = %v, want nil", *currentResponse.TargetVersion)
	}
}

func TestCachedRuntimeReleaseSourceUsesTestEnvironmentRelease(t *testing.T) {
	productionManifest := cli.ReleaseManifest{
		TagName: "v0.4.23",
		Version: "0.4.23",
		Platforms: map[string]cli.ReleaseAsset{
			"linux-amd64": {URL: "https://example.test/0.4.23/linux-amd64.tar.gz", SHA256: "production"},
		},
	}
	testManifest := cli.ReleaseManifest{
		TagName: "v0.4.24-alpha.11",
		Version: "0.4.24-alpha.11",
		Platforms: map[string]cli.ReleaseAsset{
			"linux-amd64": {URL: "https://example.test/0.4.24-alpha.11/linux-amd64.tar.gz", SHA256: "test"},
		},
	}
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metainfo.json" {
			t.Errorf("release feed path = %q, want /metainfo.json", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(cli.ReleaseMetainfo{
			SchemaVersion: 1,
			Environments: map[string]cli.ReleaseManifest{
				"production": productionManifest,
				"test":       testManifest,
			},
		}); err != nil {
			t.Errorf("encode release metainfo: %v", err)
		}
	}))
	defer feed.Close()

	t.Setenv("APP_ENV", "test")
	t.Setenv("MULTICA_SERVER_RELEASE_MANIFEST_BASE_URL", feed.URL)
	source := NewCachedRuntimeReleaseSource(time.Minute)
	release, err := source.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if release == nil || release.TagName != "v0.4.24-alpha.11" {
		t.Fatalf("Latest() = %+v, want v0.4.24-alpha.11", release)
	}

	behind := runtimeHealthTestRuntime(t, map[string]any{"cli_version": "0.4.24-alpha.9"})
	behindResponse := runtimeToResponseWithUpdateAndRelease(behind, nil, release)
	if behindResponse.RuntimeHealth != "update_available" {
		t.Fatalf("0.4.24-alpha.9 runtime_health = %q, want update_available", behindResponse.RuntimeHealth)
	}
	if behindResponse.TargetVersion == nil || *behindResponse.TargetVersion != "v0.4.24-alpha.11" {
		t.Fatalf("0.4.24-alpha.9 target_version = %v, want v0.4.24-alpha.11", behindResponse.TargetVersion)
	}
}
