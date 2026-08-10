package cli

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReleaseChannelFetchDoesNotCrossPointers(t *testing.T) {
	metainfoRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/metainfo.json":
			metainfoRequests++
			_, _ = w.Write([]byte(`{"schema_version":1,"environments":{"production":{"tag":"v0.3.9","version":"0.3.9","platforms":{}},"test":{"tag":"v0.4.0-alpha.7","version":"0.4.0-alpha.7","platforms":{}}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	alpha, err := FetchReleaseForChannelWithOverride(ReleaseChannelAlpha, server.URL)
	if err != nil || alpha.TagName != "v0.4.0-alpha.7" {
		t.Fatalf("alpha = %+v err=%v", alpha, err)
	}
	latest, err := FetchReleaseForChannelWithOverride(ReleaseChannelLatest, server.URL)
	if err != nil || latest.TagName != "v0.3.9" {
		t.Fatalf("latest = %+v err=%v", latest, err)
	}
	if metainfoRequests != 2 {
		t.Fatalf("metainfo requests = %d, want 2", metainfoRequests)
	}
}

func TestMissingAlphaDoesNotFallBackToLatest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metainfo.json" {
			_, _ = w.Write([]byte(`{"schema_version":1,"environments":{"production":{"tag":"v9.9.9","version":"9.9.9","platforms":{}}}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	if _, err := FetchReleaseForChannelWithOverride(ReleaseChannelAlpha, server.URL); err == nil {
		t.Fatal("missing alpha pointer crossed into latest")
	}
}

func TestMissingMetainfoFailsWithoutChannelFallback(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		http.NotFound(w, r)
	}))
	defer server.Close()

	if _, err := FetchReleaseForChannelWithOverride(ReleaseChannelAlpha, server.URL); err == nil {
		t.Fatal("missing metainfo must fail")
	}
	if len(paths) != 1 || paths[0] != "/metainfo.json" {
		t.Fatalf("request paths = %v, want only metainfo", paths)
	}
}

func TestReleaseChannelRejectsCrossChannelOrInconsistentManifest(t *testing.T) {
	tests := []struct {
		name, channelPath, body string
		channel                 ReleaseChannel
	}{
		{"stable in test", "test", `{"tag":"v1.2.3","version":"1.2.3","platforms":{}}`, ReleaseChannelAlpha},
		{"prerelease in production", "production", `{"tag":"v1.2.3-alpha.1","version":"1.2.3-alpha.1","platforms":{}}`, ReleaseChannelLatest},
		{"tag version mismatch", "test", `{"tag":"v1.2.3-alpha.2","version":"1.2.3-alpha.1","platforms":{}}`, ReleaseChannelAlpha},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/metainfo.json" {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte(`{"schema_version":1,"environments":{"` + tt.channelPath + `":` + tt.body + `}}`))
			}))
			defer server.Close()
			if _, err := FetchReleaseForChannelWithOverride(tt.channel, server.URL); err == nil {
				t.Fatal("invalid channel manifest was accepted")
			}
		})
	}
}

func TestReleaseChannelIsFixedByEnvironment(t *testing.T) {
	tests := []struct {
		name        string
		config      CLIConfig
		wantChannel ReleaseChannel
	}{
		{
			name:        "production uses stable packages",
			config:      CLIConfig{Environment: "production", ServerURL: "https://api.leagent.me"},
			wantChannel: ReleaseChannelLatest,
		},
		{
			name:        "test uses preview packages",
			config:      CLIConfig{Environment: "test", ServerURL: "https://api.test.leagent.me", AppURL: "https://test.leagent.me"},
			wantChannel: ReleaseChannelAlpha,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveReleaseChannel(tt.config)
			if err != nil || got != tt.wantChannel {
				t.Fatalf("ResolveReleaseChannel() = %q, %v; want %q", got, err, tt.wantChannel)
			}
		})
	}
}
