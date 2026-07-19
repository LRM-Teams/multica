package daemon

import (
	"encoding/json"
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
		ChatContextSummary:      strings.Join([]string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8", "m9", "m10"}, "\n"),
		ChatMessage:             strings.Repeat("问", restrictedMessageBytes),
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
			ExecutionProfile: executionProfileAttentionProbe,
		},
	}
	prompt := BuildPrompt(task, "pi", "/private/agent/root")
	for _, want := range []string{"SILENT|ANSWER|CONTRIBUTE|COORDINATE", "The login page is slow", "Own performance investigations", "Prior incident", "model_version", "seen_up_to_seq"} {
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
	if result.OutputSuppressedReason != "restricted_execution_profile" {
		t.Fatalf("output suppressed reason = %q", result.OutputSuppressedReason)
	}

	failed := restrictResultForExecutionProfile(TaskResult{
		Status:    "blocked",
		Comment:   "invalid probe JSON",
		SessionID: "/tmp/probe.jsonl",
		WorkDir:   "/tmp/work",
	}, executionProfileAttentionProbe)
	if failed.Status != "blocked" || failed.Comment != "invalid probe JSON" {
		t.Fatalf("restricted failure lost internal diagnosis: %#v", failed)
	}
	if failed.SessionID != "" || failed.WorkDir != "" || failed.OutputSuppressedReason != "restricted_execution_profile" {
		t.Fatalf("restricted failure retained public/session state: %#v", failed)
	}
}

func TestAttentionProbeOutputRequiresStrictSchema(t *testing.T) {
	valid := `{"decision":"ANSWER","confidence":0.92,"value_type":"direct_answer","summary":"I can own this","evidence_refs":["memory:incident"],"model_version":"provider/model","seen_up_to_seq":42}`
	parsed, err := parseRestrictedExecutionOutput(executionProfileAttentionProbe, valid)
	if err != nil {
		t.Fatalf("valid attention output rejected: %v", err)
	}
	if !strings.Contains(string(parsed), `"decision":"ANSWER"`) {
		t.Fatalf("canonical output = %s", parsed)
	}

	invalid := []string{
		`{"decision":"ANSWER","confidence":0.92,"value_type":"direct_answer","summary":"","evidence_refs":[],"model_version":"m"}`,
		`{"decision":"MAYBE","confidence":0.92,"value_type":"direct_answer","summary":"","evidence_refs":[],"model_version":"m","seen_up_to_seq":1}`,
		`{"decision":"ANSWER","confidence":1.2,"value_type":"direct_answer","summary":"","evidence_refs":[],"model_version":"m","seen_up_to_seq":1}`,
		`{"decision":"ANSWER","confidence":null,"value_type":"direct_answer","summary":"","evidence_refs":[],"model_version":"m","seen_up_to_seq":1}`,
		`{"decision":"ANSWER","confidence":0.9,"value_type":"direct_answer","summary":"","evidence_refs":[null],"model_version":"m","seen_up_to_seq":1}`,
		`{"decision":"ANSWER","confidence":0.9,"value_type":"direct_answer","summary":"","evidence_refs":[],"model_version":"m","seen_up_to_seq":1,"extra":true}`,
		valid + " trailing prose",
	}
	for _, output := range invalid {
		if _, err := parseRestrictedExecutionOutput(executionProfileAttentionProbe, output); err == nil {
			t.Fatalf("invalid attention output accepted: %s", output)
		}
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
	if got := restrictedOutputTokenLimit(executionProfileAttentionProbe); got != 96 {
		t.Fatalf("attention output limit = %d", got)
	}
	if got := restrictedOutputTokenLimit(executionProfileProtocolTurn); got != 96 {
		t.Fatalf("protocol output limit = %d", got)
	}
	if got := restrictedOutputTokenLimit(executionProfileFull); got != 0 {
		t.Fatalf("full output limit = %d", got)
	}
}

func TestAttentionProbeMetadataUsesRuntimeFacts(t *testing.T) {
	raw := json.RawMessage(`{"decision":"ANSWER","confidence":0.8,"value_type":"direct_answer","summary":"","evidence_refs":[],"model_version":"model-claimed","seen_up_to_seq":1}`)
	bound, err := bindRestrictedOutputMetadata(executionProfileAttentionProbe, raw, "configured-model", []TaskUsageEntry{{Model: "actual-model"}}, Task{InboxEvent: &AgentInboxLease{SeqTo: 99}})
	if err != nil {
		t.Fatalf("bindRestrictedOutputMetadata: %v", err)
	}
	var got attentionProbeOutput
	if err := json.Unmarshal(bound, &got); err != nil {
		t.Fatalf("unmarshal bound output: %v", err)
	}
	if got.ModelVersion != "actual-model" || got.SeenUpToSeq != 99 {
		t.Fatalf("bound metadata = model:%q seq:%d", got.ModelVersion, got.SeenUpToSeq)
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
