package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
)

func TestBuildEnvDispatchCloneInputScopesExecutionModelToCaller(t *testing.T) {
	in := ProvisionEnvDispatchAgentInput{
		WorkspaceID: "ws-1",
		EnvID:       "env-1",
		ChannelID:   "channel-1",
		AgentID:     "source-agent",
	}
	cases := []struct {
		name           string
		executionModel string
		want           string
	}{
		{name: "scratch leader", executionModel: "openai/glm-5.2", want: "openai/glm-5.2"},
		{name: "shared peer", executionModel: "env-peer-2/model-b", want: "env-peer-2/model-b"},
		{name: "training", executionModel: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildEnvDispatchCloneInput(in, "binding-1", "runtime-1", tc.executionModel)
			if got.ExecutionModel != tc.want {
				t.Fatalf("ExecutionModel = %q, want %q", got.ExecutionModel, tc.want)
			}
			if got.SourceAgentID != in.AgentID || got.RuntimeID != "runtime-1" || got.BindingID != "binding-1" {
				t.Fatal("clone input lost binding identity")
			}
		})
	}
}

func TestEnvDispatchResponseSerializesAgentRunAndCompleteSandboxTriple(t *testing.T) {
	response := EnvDispatchResponse{
		ProjectID: "project-1",
		Rollouts: mapRollouts([]service.EnvRollout{{
			EnvID:      "env-1",
			ProjectID:  "project-1",
			AgentRunID: "task-1",
			AgentSandboxes: map[string]service.AgentSandboxStatus{
				"leader": {
					Status: "ready", SandboxInstanceID: "sandbox-1",
					RuntimeID: "runtime-1", DaemonID: "daemon-1",
				},
			},
		}}),
	}
	body, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	wire := string(body)
	for _, want := range []string{
		`"agent_run_id":"task-1"`, `"agent_sandboxes"`,
		`"sandbox_instance_id":"sandbox-1"`, `"runtime_id":"runtime-1"`,
		`"daemon_id":"daemon-1"`,
	} {
		if !strings.Contains(wire, want) {
			t.Fatalf("response is missing required field %s", want)
		}
	}
	for _, forbidden := range []string{`"api_key"`, `"base_url"`, "synthetic-key-must-not-appear"} {
		if strings.Contains(wire, forbidden) {
			t.Fatal("response disclosed runtime route credentials")
		}
	}
}
