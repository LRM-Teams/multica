package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

const (
	v6CLIRunID      = "00000000-0000-4000-8000-000000000003"
	v6CLIWorkID     = "00000000-0000-4000-8000-000000000212"
	v6CLIAttemptID  = "00000000-0000-4000-8000-000000000213"
	v6CLIArtifactID = "00000000-0000-4000-8000-000000000214"
)

func TestResearchV6CommandsAreRegistered(t *testing.T) {
	for _, name := range []string{
		"work-manifest", "work-artifact", "director-brief", "director-brief-ack",
		"work-catalog", "work-catalog-ack", "work-submit", "report-upload",
	} {
		command, _, err := researchCmd.Find([]string{name})
		if err != nil || command == nil || command.Name() != name {
			t.Fatalf("research %s is not registered: command=%#v err=%v", name, command, err)
		}
	}
}

func TestResearchV6ReadCommandsUseTaskBoundAgentRoutes(t *testing.T) {
	t.Setenv("MULTICA_TOKEN", "test-token")
	t.Setenv("MULTICA_WORKSPACE_ID", "00000000-0000-4000-8000-000000000002")
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("MULTICA_SERVER_URL", server.URL)

	manifestCmd := newResearchV6TestCommand()
	if err := runResearchV6WorkManifest(manifestCmd, []string{v6CLIRunID, v6CLIWorkID, v6CLIAttemptID}); err != nil {
		t.Fatal(err)
	}
	artifactCmd := newResearchV6TestCommand()
	if err := runResearchV6WorkArtifact(artifactCmd, []string{v6CLIRunID, v6CLIWorkID, v6CLIAttemptID, v6CLIArtifactID}); err != nil {
		t.Fatal(err)
	}
	briefCmd := newResearchV6TestCommand()
	briefCmd.Flags().String("cursor", "4", "")
	if err := runResearchV6DirectorBrief(briefCmd, []string{v6CLIRunID, v6CLIWorkID, v6CLIAttemptID}); err != nil {
		t.Fatal(err)
	}
	catalogCmd := newResearchV6TestCommand()
	catalogCmd.Flags().String("view", "same_tier", "")
	catalogCmd.Flags().String("cursor", "next page", "")
	if err := runResearchV6WorkCatalog(catalogCmd, []string{v6CLIRunID, v6CLIWorkID, v6CLIAttemptID}); err != nil {
		t.Fatal(err)
	}

	base := "/api/agent/research/sessions/" + v6CLIRunID + "/work-items/" + v6CLIWorkID + "/attempts/" + v6CLIAttemptID
	want := []string{
		"GET " + base + "/manifest",
		"GET " + base + "/artifacts/" + v6CLIArtifactID,
		"GET " + base + "/director-brief?cursor=4",
		"GET " + base + "/catalog?cursor=next+page&view=same_tier",
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests:\n%s\nwant:\n%s", strings.Join(requests, "\n"), strings.Join(want, "\n"))
	}
}

func TestResearchV6SubmissionPostsRawEnvelope(t *testing.T) {
	t.Setenv("MULTICA_TOKEN", "test-token")
	t.Setenv("MULTICA_WORKSPACE_ID", "00000000-0000-4000-8000-000000000002")
	var method, path string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"outcome":"accepted"}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("MULTICA_SERVER_URL", server.URL)
	file := t.TempDir() + "/submission.json"
	if err := os.WriteFile(file, []byte(`{"contract_kind":"director_action_proposal","schema_version":6}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newResearchV6TestCommand()
	cmd.Flags().String("file", file, "")
	if err := runResearchV6WorkSubmit(cmd, []string{v6CLIRunID, v6CLIWorkID, v6CLIAttemptID}); err != nil {
		t.Fatal(err)
	}
	base := "/api/agent/research/sessions/" + v6CLIRunID + "/work-items/" + v6CLIWorkID + "/attempts/" + v6CLIAttemptID
	if method != http.MethodPost || path != base+"/submission" || body["contract_kind"] != "director_action_proposal" {
		t.Fatalf("request=%s %s body=%v", method, path, body)
	}
}

func newResearchV6TestCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	return cmd
}
