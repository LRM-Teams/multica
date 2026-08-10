package cli

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReleaseChannelFetchDoesNotCrossPointers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/alpha.json":
			_, _ = w.Write([]byte(`{"tag":"v0.4.0-alpha.7","version":"0.4.0-alpha.7","platforms":{}}`))
		case "/manifest.json":
			_, _ = w.Write([]byte(`{"tag":"v0.3.9","version":"0.3.9","platforms":{}}`))
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
}

func TestMissingAlphaDoesNotFallBackToLatest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manifest.json" {
			_, _ = w.Write([]byte(`{"tag":"v9.9.9","version":"9.9.9","platforms":{}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	if _, err := FetchReleaseForChannelWithOverride(ReleaseChannelAlpha, server.URL); err == nil {
		t.Fatal("missing alpha pointer crossed into latest")
	}
}

func TestReleaseChannelDefaultsByEnvironmentButExplicitChoiceWins(t *testing.T) {
	testDefault, err := ResolveReleaseChannel(CLIConfig{Environment: "test", ServerURL: "https://test.leagent.me"})
	if err != nil || testDefault != ReleaseChannelAlpha {
		t.Fatalf("test default = %q err=%v", testDefault, err)
	}
	explicit, err := ResolveReleaseChannel(CLIConfig{Environment: "test", ServerURL: "https://test.leagent.me", ReleaseChannel: "latest"})
	if err != nil || explicit != ReleaseChannelLatest {
		t.Fatalf("explicit test channel = %q err=%v", explicit, err)
	}
}
