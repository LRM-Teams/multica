package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestAdoptVerifiedLegacyComputerUsesActiveTestSessionForCurrentUser(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	userID := uuid.NewString()
	workspaceID := uuid.NewString()
	computerID := uuid.NewString()
	profileRoot := filepath.Join(os.Getenv("HOME"), ".multica", "profiles", "production")
	if err := os.MkdirAll(profileRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileRoot, "daemon.id"), []byte(computerID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/me":
			_ = json.NewEncoder(w).Encode(map[string]string{"id": userID})
		case "/api/workspaces":
			_ = json.NewEncoder(w).Encode([]map[string]string{{"id": workspaceID, "slug": "lrm-team"}})
		case "/api/runtimes":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"workspace_id": workspaceID,
				"daemon_id":    computerID,
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	testConfig := cli.CLIConfig{
		Environment: string(cli.ServiceEnvironmentTest),
		ServerURL:   server.URL,
		AppURL:      server.URL,
		WorkspaceID: uuid.NewString(),
		Token:       "test-token",
		Environments: map[string]cli.ServiceEnvironmentConfig{
			string(cli.ServiceEnvironmentTest): {
				ServerURL: server.URL,
				AppURL:    server.URL,
				Token:     "test-token",
			},
		},
	}
	if err := cli.SaveCLIConfigForProfile(testConfig, ""); err != nil {
		t.Fatal(err)
	}

	previousFactory := newLegacyAdoptionAPIClient
	previousEstablish := establishLegacyWorkspaceConnection
	var currentUserBase string
	var restoredWith cli.CLIConfig
	newLegacyAdoptionAPIClient = func(baseURL, selectedWorkspaceID, token string) *cli.APIClient {
		if token == "test-token" {
			currentUserBase = baseURL
		}
		return cli.NewAPIClient(server.URL, selectedWorkspaceID, token)
	}
	establishLegacyWorkspaceConnection = func(cfg cli.CLIConfig, identity, restoredWorkspaceID, slug string) error {
		restoredWith = cfg
		return nil
	}
	t.Cleanup(func() {
		newLegacyAdoptionAPIClient = previousFactory
		establishLegacyWorkspaceConnection = previousEstablish
	})

	err := adoptVerifiedLegacyComputer(nil, []legacyProfileSnapshot{{
		Source:     "production profile",
		ComputerID: computerID,
		Config: cli.CLIConfig{
			ServerURL:   cli.OfficialCloudAPIURL,
			WorkspaceID: workspaceID,
			Token:       "production-token",
		},
	}})
	if err != nil {
		t.Fatalf("adoptVerifiedLegacyComputer: %v", err)
	}
	if currentUserBase != server.URL {
		t.Fatalf("current user was verified against %q, want active Test origin %q", currentUserBase, server.URL)
	}
	if restoredWith.ServerURL != cli.OfficialCloudAPIURL || restoredWith.Token != "production-token" {
		t.Fatalf("legacy connection restored with active Test config: %+v", restoredWith)
	}
}
