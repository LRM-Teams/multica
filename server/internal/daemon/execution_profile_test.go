package daemon

import (
	"strings"
	"testing"
)

func TestTaskExecutionProfileDefaultsToFull(t *testing.T) {
	for _, task := range []Task{{}, {ExecutionConfig: &TaskExecutionConfig{Snapshotted: true}}} {
		profile, err := taskExecutionProfile(task)
		if err != nil {
			t.Fatalf("taskExecutionProfile: %v", err)
		}
		if profile != executionProfileFull {
			t.Fatalf("profile = %q, want %q", profile, executionProfileFull)
		}
	}
}

func TestTaskExecutionProfileRejectsUnknown(t *testing.T) {
	_, err := taskExecutionProfile(Task{ExecutionConfig: &TaskExecutionConfig{ExecutionProfile: "unknown"}})
	if err == nil {
		t.Fatal("unknown execution profile was accepted")
	}
}

func TestRestrictedExecutionProfilesFailClosedOutsidePi(t *testing.T) {
	for _, profile := range []string{executionProfileAttentionProbe, executionProfileProtocolTurn} {
		if err := validateExecutionProfileProvider(profile, "pi"); err != nil {
			t.Fatalf("Pi rejected for %q: %v", profile, err)
		}
		if err := validateExecutionProfileProvider(profile, "codex"); err == nil {
			t.Fatalf("unsupported provider accepted for %q", profile)
		}
	}
}

func TestRestrictedPiExecutionNeverReusesPersistentChatRuntime(t *testing.T) {
	full := Task{ChatSessionID: "chat-1"}
	if !usesPersistentPiChatRuntime("pi", full) {
		t.Fatal("full Pi chat should reuse its persistent runtime")
	}
	restricted := Task{
		ChatSessionID: "chat-1",
		ExecutionConfig: &TaskExecutionConfig{
			ExecutionProfile: executionProfileAttentionProbe,
		},
	}
	if usesPersistentPiChatRuntime("pi", restricted) {
		t.Fatal("attention probe reused the main persistent Pi chat runtime")
	}
}

func TestRestrictTaskForExecutionProfileRemovesFullExecutionSurfaces(t *testing.T) {
	originalAgent := &AgentData{
		Instructions: strings.Repeat("i", restrictedAgentInstructionsRunes+10),
		Skills:       []SkillData{{ID: "skill-1"}},
		Memories: []MemoryData{
			{ID: "m1", Content: strings.Repeat("甲", restrictedMemoryRunes-1)},
			{ID: "m2", Content: "乙乙"},
		},
		CustomArgs: []string{"--tools", "bash"},
		McpConfig:  []byte(`{"mcpServers":{"danger":{}}}`),
	}
	task := Task{
		Agent:                   originalAgent,
		Repos:                   []RepoData{{URL: "https://example.test/repo"}},
		ProjectResources:        []ProjectResourceData{{ID: "resource-1"}},
		ChatMessageAttachments:  []ChatAttachmentMeta{{ID: "attachment-1"}},
		PriorSessionID:          "session-1",
		PriorWorkDir:            "/repo",
		WorkspaceContext:        "workspace context",
		ProvisionManagedWorkdir: true,
		ManagedWorkdirRelPath:   "managed/repo",
		ArealProxy:              &ArealProxy{APIKey: "proxy-key"},
	}

	got := restrictTaskForExecutionProfile(task, executionProfileAttentionProbe)
	if len(got.Repos) != 0 || len(got.ProjectResources) != 0 || len(got.ChatMessageAttachments) != 0 {
		t.Fatalf("restricted task retained repository/resource/attachment surfaces: %#v", got)
	}
	if got.PriorSessionID != "" || got.PriorWorkDir != "" {
		t.Fatalf("restricted task retained persistent session: %#v", got)
	}
	if got.WorkspaceContext != "" {
		t.Fatalf("restricted task retained workspace context: %q", got.WorkspaceContext)
	}
	if got.ProvisionManagedWorkdir || got.ManagedWorkdirRelPath != "" || got.ArealProxy != nil {
		t.Fatalf("restricted task retained execution overrides: %#v", got)
	}
	if got.Agent == originalAgent {
		t.Fatal("restricted task mutated the original AgentData pointer")
	}
	if len(got.Agent.Skills) != 0 || len(got.Agent.CustomArgs) != 0 || len(got.Agent.McpConfig) != 0 {
		t.Fatalf("restricted agent retained tools: %#v", got.Agent)
	}
	if len([]rune(got.Agent.Instructions)) != restrictedAgentInstructionsRunes {
		t.Fatalf("instructions runes = %d", len([]rune(got.Agent.Instructions)))
	}
	memoryRunes := 0
	for _, memory := range got.Agent.Memories {
		memoryRunes += len([]rune(memory.Content))
	}
	if memoryRunes != restrictedMemoryRunes {
		t.Fatalf("memory runes = %d, want %d", memoryRunes, restrictedMemoryRunes)
	}
	if len(originalAgent.Skills) == 0 || len(originalAgent.CustomArgs) == 0 || len(originalAgent.McpConfig) == 0 {
		t.Fatal("original AgentData was mutated")
	}
}

func TestBuildPromptUsesRestrictedProfileContract(t *testing.T) {
	task := Task{
		ChatSessionID:      "chat-1",
		ChatMessage:        "The login page is slow",
		ChatContextSummary: "A bounded recent message",
		Agent:              &AgentData{Instructions: "Own performance investigations"},
		ExecutionConfig: &TaskExecutionConfig{
			ExecutionProfile: executionProfileAttentionProbe,
		},
	}
	prompt := BuildPrompt(task, "pi", "/private/agent/root")
	for _, want := range []string{"SILENT|ANSWER|CONTRIBUTE|COORDINATE", "The login page is slow", "Own performance investigations"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("attention prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, forbidden := range []string{"multica attachment view", "/private/agent/root", "Respond to their message"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("attention prompt leaked full chat instruction %q:\n%s", forbidden, prompt)
		}
	}
}

func TestRestrictedExecutionResultCannotPublishOrReplaceChatSession(t *testing.T) {
	result := restrictResultForExecutionProfile(TaskResult{
		Status:     "completed",
		Comment:    `{"decision":"ANSWER"}`,
		BranchName: "probe-branch",
		SessionID:  "/tmp/probe.jsonl",
		WorkDir:    "/tmp/probe-workdir",
	}, executionProfileAttentionProbe)
	if result.Comment != "" || result.BranchName != "" || result.SessionID != "" || result.WorkDir != "" {
		t.Fatalf("restricted result retained public or persistent state: %#v", result)
	}
	if result.Action != "no_reply" || result.Type != "no_reply" {
		t.Fatalf("restricted result disposition = action:%q type:%q", result.Action, result.Type)
	}
}
