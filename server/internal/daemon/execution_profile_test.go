package daemon

import (
	"strings"
	"testing"
	"unicode/utf8"
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
	for _, profile := range []string{executionProfileProtocolTurn} {
		if err := validateExecutionProfileProvider(profile, "pi"); err != nil {
			t.Fatalf("Pi rejected for %q: %v", profile, err)
		}
		if err := validateExecutionProfileProvider(profile, "codex"); err == nil {
			t.Fatalf("unsupported provider accepted for %q", profile)
		}
	}
}

func TestRestrictedExecutionConfigRejectsUnsafeBounds(t *testing.T) {
	unsafe := []*TaskExecutionConfig{
		{ExecutionProfile: executionProfileProtocolTurn, ToolsEnabled: true},
		{ExecutionProfile: executionProfileProtocolTurn, ContextMessages: restrictedContextMessages + 1},
		{ExecutionProfile: executionProfileProtocolTurn, MemoryBudgetBytes: restrictedMemoryBytes + 1},
		{ExecutionProfile: executionProfileProtocolTurn, MaxOutputTokens: restrictedExecutionMaxOutputTokens + 1},
		{ExecutionProfile: executionProfileProtocolTurn, MaxOutputTokens: -1},
	}
	for _, config := range unsafe {
		if _, err := taskExecutionProfile(Task{ExecutionConfig: config}); err == nil {
			t.Fatalf("unsafe restricted config accepted: %#v", config)
		}
	}
}

func TestRestrictedExecutionConfigAppliesTighterBounds(t *testing.T) {
	task := Task{
		ChatContextSummary: strings.Join([]string{"m1", "m2", "m3", "m4"}, "\n"),
		Agent: &AgentData{Memories: []MemoryData{
			{Name: "one", Content: strings.Repeat("a", 32)},
			{Name: "two", Content: strings.Repeat("b", 32)},
		}},
		ExecutionConfig: &TaskExecutionConfig{
			ExecutionProfile:  executionProfileProtocolTurn,
			ContextMessages:   2,
			MemoryBudgetBytes: 24,
			MaxOutputTokens:   48,
			ToolsEnabled:      false,
		},
	}
	profile, err := taskExecutionProfile(task)
	if err != nil {
		t.Fatalf("taskExecutionProfile: %v", err)
	}
	restricted := restrictTaskForExecutionProfile(task, profile)
	if restricted.ChatContextSummary != "m3\nm4" {
		t.Fatalf("bounded context = %q", restricted.ChatContextSummary)
	}
	var memoryBytes int
	for _, memory := range restricted.Agent.Memories {
		memoryBytes += len(memory.Content)
	}
	if memoryBytes != 24 {
		t.Fatalf("memory bytes = %d, want 24", memoryBytes)
	}
	if got := restrictedOutputTokenLimitForTask(task, profile); got != 48 {
		t.Fatalf("output token limit = %d, want 48", got)
	}
}

func TestRestrictTaskForExecutionProfileRemovesFullExecutionSurfaces(t *testing.T) {
	originalAgent := &AgentData{
		Instructions: strings.Repeat("i", restrictedAgentInstructionsBytes+10),
		Skills:       []SkillData{{ID: "skill-1"}},
		Memories: []MemoryData{
			{ID: "m1", Name: "private", Content: strings.Repeat("m", restrictedMemoryBytes)},
			{ID: "m2", Content: "乙乙"},
		},
		CustomArgs: []string{"--tools", "bash"},
		McpConfig:  []byte(`{"mcpServers":{"danger":{}}}`),
	}
	task := Task{
		Agent:                  originalAgent,
		ChatMessageAttachments: []ChatAttachmentMeta{{ID: "attachment-1"}},
		PriorSessionID:         "session-1",
		PriorWorkDir:           "/repo",
		WorkspaceContext:       "workspace context",
		ArealProxy:             &ArealProxy{APIKey: "proxy-key"},
		ChatContextSummary:     strings.Join([]string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8", "m9", "m10"}, "\n"),
		ChatMessage:            strings.Repeat("问", restrictedMessageBytes),
	}

	got := restrictTaskForExecutionProfile(task, executionProfileProtocolTurn)
	if len(got.ChatMessageAttachments) != 0 {
		t.Fatalf("restricted task retained attachment surfaces: %#v", got)
	}
	if got.PriorSessionID != "" || got.PriorWorkDir != "" {
		t.Fatalf("restricted task retained persistent session: %#v", got)
	}
	if got.WorkspaceContext != "" {
		t.Fatalf("restricted task retained workspace context: %q", got.WorkspaceContext)
	}
	if got.ArealProxy != nil {
		t.Fatalf("restricted task retained execution overrides: %#v", got)
	}
	if got.Agent == originalAgent {
		t.Fatal("restricted task mutated the original AgentData pointer")
	}
	if len(got.Agent.Skills) != 0 || len(got.Agent.CustomArgs) != 0 || len(got.Agent.McpConfig) != 0 {
		t.Fatalf("restricted agent retained tools: %#v", got.Agent)
	}
	if len(got.Agent.Instructions) != restrictedAgentInstructionsBytes {
		t.Fatalf("instruction bytes = %d", len(got.Agent.Instructions))
	}
	memoryBytes := 0
	for _, memory := range got.Agent.Memories {
		memoryBytes += len(memory.Content)
		if !utf8.ValidString(memory.Content) {
			t.Fatalf("memory was truncated to invalid UTF-8: %q", memory.Content)
		}
	}
	if memoryBytes != restrictedMemoryBytes {
		t.Fatalf("memory bytes = %d, want %d", memoryBytes, restrictedMemoryBytes)
	}
	contextLines := strings.Split(got.ChatContextSummary, "\n")
	if len(contextLines) != restrictedContextMessages || contextLines[0] != "m3" || contextLines[len(contextLines)-1] != "m10" {
		t.Fatalf("bounded context = %#v", contextLines)
	}
	if len(got.ChatMessage) > restrictedMessageBytes || !utf8.ValidString(got.ChatMessage) {
		t.Fatalf("current message limit invalid: bytes=%d valid=%v", len(got.ChatMessage), utf8.ValidString(got.ChatMessage))
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
		Agent: &AgentData{
			Instructions: "Own performance investigations",
			Memories:     []MemoryData{{Name: "Prior incident", Content: "Connection pools can stall after config drift."}},
		},
		ExecutionConfig: &TaskExecutionConfig{
			ExecutionProfile: executionProfileProtocolTurn,
		},
	}
	prompt := BuildPrompt(task, "pi", "/private/agent/root")
	for _, want := range []string{"Execute one bounded collaboration protocol turn"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("protocol turn prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, forbidden := range []string{"multica attachment view", "/private/agent/root", "Respond to their message"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("protocol turn prompt leaked full chat instruction %q:\n%s", forbidden, prompt)
		}
	}
}

func TestRestrictedExecutionResultCannotPublishOrReplaceChatSession(t *testing.T) {
	result := restrictResultForExecutionProfile(TaskResult{
		Status:     "completed",
		Comment:    `{"vote":"KEEP"}`,
		BranchName: "probe-branch",
		SessionID:  "/tmp/probe.jsonl",
		WorkDir:    "/tmp/probe-workdir",
	}, executionProfileProtocolTurn)
	if result.Comment != "" || result.BranchName != "" || result.SessionID != "" || result.WorkDir != "" {
		t.Fatalf("restricted result retained public or persistent state: %#v", result)
	}
	if result.Action != "no_reply" || result.Type != "no_reply" {
		t.Fatalf("restricted result disposition = action:%q type:%q", result.Action, result.Type)
	}
	if result.OutputSuppressedReason != "restricted_execution_profile" {
		t.Fatalf("output suppressed reason = %q", result.OutputSuppressedReason)
	}

	failed := restrictResultForExecutionProfile(TaskResult{
		Status:    "blocked",
		Comment:   "invalid protocol turn JSON",
		SessionID: "/tmp/probe.jsonl",
		WorkDir:   "/tmp/work",
	}, executionProfileProtocolTurn)
	if failed.Status != "blocked" || failed.Comment != "invalid protocol turn JSON" {
		t.Fatalf("restricted failure lost internal diagnosis: %#v", failed)
	}
	if failed.SessionID != "" || failed.WorkDir != "" || failed.OutputSuppressedReason != "restricted_execution_profile" {
		t.Fatalf("restricted failure retained public/session state: %#v", failed)
	}
}

func TestProtocolTurnOutputRequiresOneNonEmptyJSONObject(t *testing.T) {
	if _, err := parseRestrictedExecutionOutput(executionProfileProtocolTurn, `{"vote":"KEEP"}`); err != nil {
		t.Fatalf("valid protocol turn rejected: %v", err)
	}
	for _, output := range []string{`{}`, `[]`, `{"vote":"KEEP"} prose`, `null`} {
		if _, err := parseRestrictedExecutionOutput(executionProfileProtocolTurn, output); err == nil {
			t.Fatalf("invalid protocol output accepted: %s", output)
		}
	}
}

func TestRestrictedOutputTokenLimit(t *testing.T) {
	if got := restrictedOutputTokenLimit(executionProfileProtocolTurn); got != 96 {
		t.Fatalf("protocol output limit = %d", got)
	}
	if got := restrictedOutputTokenLimit(executionProfileFull); got != 0 {
		t.Fatalf("full output limit = %d", got)
	}
}

func TestUnknownProfileFailureSuppressesPublicOutput(t *testing.T) {
	if suppressPublicOutputForTask(Task{}) {
		t.Fatal("historical task unexpectedly suppresses output")
	}
	if suppressPublicOutputForTask(Task{ExecutionConfig: &TaskExecutionConfig{ExecutionProfile: executionProfileFull}}) {
		t.Fatal("full profile unexpectedly suppresses output")
	}
	if !suppressPublicOutputForTask(Task{ExecutionConfig: &TaskExecutionConfig{ExecutionProfile: "future_restricted_profile"}}) {
		t.Fatal("unknown explicit profile failure could become public")
	}
}
