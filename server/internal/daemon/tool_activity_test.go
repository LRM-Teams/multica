package daemon

import (
	"strings"
	"testing"
)

func TestCanonicalToolSemantic(t *testing.T) {
	cases := []struct {
		raw   string
		want  string
		known bool
	}{
		{"bash", "bash", true},
		{"Bash", "bash", true},
		{"exec_command", "bash", true},
		{"run_terminal_command", "bash", true},
		{"Read", "read_file", true},
		{"read_file", "read_file", true},
		{"Write", "write_file", true},
		{"Edit", "edit_file", true},
		{"str_replace", "edit_file", true},
		{"Glob", "glob", true},
		{"Grep", "grep", true},
		{"WebFetch", "web_fetch", true},
		{"WebSearch", "web_search", true},
		{"TodoWrite", "todo_write", true},
		{"send_message", "send_message", true},
		{"receive_message", "receive_message", true},
		{"mcp__multica__read_file", "read_file", true},
		{"mcp_chat_read_file", "read_file", true},
		{"tool:bash", "bash", true},
		{"cursor-agent", "cursor-agent", false},
	}
	for _, tc := range cases {
		got, known := canonicalToolSemantic(tc.raw)
		if got != tc.want || known != tc.known {
			t.Errorf("canonicalToolSemantic(%q) = (%q, %v), want (%q, %v)", tc.raw, got, known, tc.want, tc.known)
		}
	}
}

func TestToolActivityFact(t *testing.T) {
	cases := []struct {
		name          string
		tool          string
		input         map[string]any
		wantDetail    string
		wantNarrative string
	}{
		{
			name:          "bash command becomes the subtext",
			tool:          "Bash",
			input:         map[string]any{"command": "pnpm test"},
			wantDetail:    "running_command",
			wantNarrative: "pnpm test",
		},
		{
			name:          "codex exec_command alias",
			tool:          "exec_command",
			input:         map[string]any{"command": "ls -la"},
			wantDetail:    "running_command",
			wantNarrative: "ls -la",
		},
		{
			name:          "cmd key fallback",
			tool:          "bash",
			input:         map[string]any{"cmd": "make build"},
			wantDetail:    "running_command",
			wantNarrative: "make build",
		},
		{
			name:          "read file path",
			tool:          "Read",
			input:         map[string]any{"file_path": "/repo/main.go"},
			wantDetail:    "reading_file",
			wantNarrative: "/repo/main.go",
		},
		{
			name:          "write file path",
			tool:          "Write",
			input:         map[string]any{"file_path": "/repo/out.go"},
			wantDetail:    "writing_file",
			wantNarrative: "/repo/out.go",
		},
		{
			name:          "edit file path",
			tool:          "Edit",
			input:         map[string]any{"file_path": "/repo/out.go"},
			wantDetail:    "editing_file",
			wantNarrative: "/repo/out.go",
		},
		{
			name:          "glob pattern",
			tool:          "Glob",
			input:         map[string]any{"pattern": "**/*.go"},
			wantDetail:    "searching_files",
			wantNarrative: "**/*.go",
		},
		{
			name:          "grep pattern",
			tool:          "Grep",
			input:         map[string]any{"pattern": "toolActivityFact"},
			wantDetail:    "searching_code",
			wantNarrative: "toolActivityFact",
		},
		{
			name:          "web fetch url",
			tool:          "WebFetch",
			input:         map[string]any{"url": "https://example.com"},
			wantDetail:    "fetching_url",
			wantNarrative: "https://example.com",
		},
		{
			name:          "web search query",
			tool:          "WebSearch",
			input:         map[string]any{"query": "raft activity"},
			wantDetail:    "searching_web",
			wantNarrative: "raft activity",
		},
		{
			name:          "todo write carries no subtext",
			tool:          "TodoWrite",
			input:         map[string]any{"todos": []any{"a"}},
			wantDetail:    "updating_tasks",
			wantNarrative: "",
		},
		{
			name:          "send message target",
			tool:          "send_message",
			input:         map[string]any{"target": "#general"},
			wantDetail:    "sending_message",
			wantNarrative: "#general",
		},
		{
			name:          "send message dm",
			tool:          "send_message",
			input:         map[string]any{"dm_to": "li-wei"},
			wantDetail:    "sending_message",
			wantNarrative: "DM:@li-wei",
		},
		{
			name:          "check messages has no subtext",
			tool:          "check_messages",
			input:         nil,
			wantDetail:    "checking_messages",
			wantNarrative: "",
		},
		{
			name:          "receive message shares the checking label",
			tool:          "receive_message",
			input:         nil,
			wantDetail:    "checking_messages",
			wantNarrative: "",
		},
		{
			name:          "wait for message",
			tool:          "wait_for_message",
			input:         nil,
			wantDetail:    "waiting_for_message",
			wantNarrative: "",
		},
		{
			name:          "read history channel",
			tool:          "read_history",
			input:         map[string]any{"channel": "#eng"},
			wantDetail:    "reading_history",
			wantNarrative: "#eng",
		},
		{
			name:          "search messages query",
			tool:          "search_messages",
			input:         map[string]any{"query": "deploy"},
			wantDetail:    "searching_messages",
			wantNarrative: "deploy",
		},
		{
			name:          "list tasks channel",
			tool:          "list_tasks",
			input:         map[string]any{"channel": "#eng"},
			wantDetail:    "listing_tasks",
			wantNarrative: "#eng",
		},
		{
			name:          "claim tasks formats numbers",
			tool:          "claim_tasks",
			input:         map[string]any{"channel": "#eng", "task_numbers": []any{float64(1), float64(2)}},
			wantDetail:    "claiming_task",
			wantNarrative: "#eng #t1 #t2",
		},
		{
			name:          "unclaim task ref",
			tool:          "unclaim_task",
			input:         map[string]any{"channel": "#eng", "task_number": float64(3)},
			wantDetail:    "unclaiming_task",
			wantNarrative: "#eng #t3",
		},
		{
			name:          "schedule reminder title",
			tool:          "schedule_reminder",
			input:         map[string]any{"title": "standup"},
			wantDetail:    "scheduling_reminder",
			wantNarrative: "standup",
		},
		{
			name:          "cancel reminder id prefix",
			tool:          "cancel_reminder",
			input:         map[string]any{"reminder_id": "abcdef1234567890"},
			wantDetail:    "canceling_reminder",
			wantNarrative: "#abcdef12",
		},
		{
			name:          "update reminder id prefix from native tool",
			tool:          "update_reminder",
			input:         map[string]any{"reminder_id": "a291584bdeadbeef"},
			wantDetail:    "updating_reminder",
			wantNarrative: "#a291584b",
		},
		{
			name:          "update reminder id from CLI invocation",
			tool:          "bash",
			input:         map[string]any{"command": "multica reminder update --id a291584bdeadbeef --title ping"},
			wantDetail:    "updating_reminder",
			wantNarrative: "#a291584b",
		},
		{
			name:          "snooze reminder id from CLI invocation",
			tool:          "bash",
			input:         map[string]any{"command": "multica reminder snooze --id a291584bdeadbeef --delay-seconds 600"},
			wantDetail:    "snoozing_reminder",
			wantNarrative: "#a291584b",
		},
		{
			name:          "unknown tool is never silent",
			tool:          "cursor-agent",
			input:         map[string]any{"prompt": "do things"},
			wantDetail:    "other",
			wantNarrative: "Using cursor-agent",
		},
		{
			name:          "mcp namespaced tool resolves",
			tool:          "mcp__multica__read_file",
			input:         map[string]any{"file_path": "/x"},
			wantDetail:    "reading_file",
			wantNarrative: "/x",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			detail, narrative := toolActivityFact(tc.tool, tc.input)
			if detail != tc.wantDetail || narrative != tc.wantNarrative {
				t.Fatalf("toolActivityFact(%q, %v) = (%q, %q), want (%q, %q)",
					tc.tool, tc.input, detail, narrative, tc.wantDetail, tc.wantNarrative)
			}
		})
	}
}

func TestToolActivityFactBoundsTheCommand(t *testing.T) {
	_, narrative := toolActivityFact("bash", map[string]any{"command": strings.Repeat("x", maxActivityCommandRunes+100)})
	if got := len([]rune(narrative)); got > maxActivityCommandRunes+1 {
		t.Fatalf("command not bounded at the source: %d runes", got)
	}
	if !strings.HasSuffix(narrative, "…") {
		t.Fatalf("truncated command should carry an ellipsis: %q", narrative)
	}
}

func TestResolveMulticaCLIInvocation(t *testing.T) {
	cases := []struct {
		name         string
		command      string
		wantSemantic string
		wantSummary  string
		wantOK       bool
	}{
		{
			name:         "message send reclassifies",
			command:      `multica message send --target #general "hi there"`,
			wantSemantic: "send_message",
			wantSummary:  "#general",
			wantOK:       true,
		},
		{
			name:         "env assignment prefix is skipped",
			command:      `FOO=bar BAZ=qux multica message check`,
			wantSemantic: "check_messages",
			wantSummary:  "",
			wantOK:       true,
		},
		{
			name:         "sh -c unwraps",
			command:      `bash -c "multica task list --channel #eng"`,
			wantSemantic: "list_tasks",
			wantSummary:  "#eng",
			wantOK:       true,
		},
		{
			name:         "path-form binary resolves by basename",
			command:      `/usr/local/bin/multica issue get MUL-123`,
			wantSemantic: "get_issue",
			wantSummary:  "MUL-123",
			wantOK:       true,
		},
		{
			name:         "issue comment add",
			command:      `multica issue comment add MUL-123 --body "looks good"`,
			wantSemantic: "comment_issue",
			wantSummary:  "MUL-123",
			wantOK:       true,
		},
		{
			name:         "issue comment list",
			command:      `multica issue comment list MUL-9`,
			wantSemantic: "list_issue_comments",
			wantSummary:  "MUL-9",
			wantOK:       true,
		},
		{
			name:         "reminder schedule title",
			command:      `multica reminder schedule --title "standup"`,
			wantSemantic: "schedule_reminder",
			wantSummary:  "standup",
			wantOK:       true,
		},
		{
			name:         "task claim",
			command:      `multica task claim --channel #eng`,
			wantSemantic: "claim_tasks",
			wantSummary:  "#eng",
			wantOK:       true,
		},
		{
			name:         "option equals form",
			command:      `multica message read --channel=#eng`,
			wantSemantic: "read_history",
			wantSummary:  "#eng",
			wantOK:       true,
		},
		{
			name:    "plain shell command passes through",
			command: `ls -la`,
			wantOK:  false,
		},
		{
			name:    "bare cli without subcommand passes through",
			command: `multica`,
			wantOK:  false,
		},
		{
			name:    "unknown cli subcommand passes through",
			command: `multica workspace list`,
			wantOK:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			semantic, summary, ok := resolveMulticaCLIInvocation("bash", map[string]any{"command": tc.command})
			if ok != tc.wantOK {
				t.Fatalf("resolveMulticaCLIInvocation(%q) ok = %v, want %v", tc.command, ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if semantic != tc.wantSemantic || summary != tc.wantSummary {
				t.Fatalf("resolveMulticaCLIInvocation(%q) = (%q, %q), want (%q, %q)",
					tc.command, semantic, summary, tc.wantSemantic, tc.wantSummary)
			}
		})
	}
}

func TestResolveMulticaCLIInvocationIgnoresNonShellTools(t *testing.T) {
	if _, _, ok := resolveMulticaCLIInvocation("read_file", map[string]any{"command": "multica message check"}); ok {
		t.Fatal("non-shell tools must never be reclassified")
	}
	if _, _, ok := resolveMulticaCLIInvocation("bash", map[string]any{"file_path": "/x"}); ok {
		t.Fatal("shell tool without a command must not be reclassified")
	}
}

// TestToolActivityTablesAreComplete guards the daemon↔server contract: every
// canonical semantic an alias can resolve to must have both a summary kind
// and a wire detail kind, or toolActivityFact silently degrades.
func TestToolActivityTablesAreComplete(t *testing.T) {
	for alias, semantic := range toolSemanticAliases {
		if _, ok := toolSummaryKind[semantic]; !ok {
			t.Errorf("alias %q resolves to %q with no toolSummaryKind entry", alias, semantic)
		}
		if kind, ok := toolDetailKind[semantic]; !ok || kind == "" {
			t.Errorf("alias %q resolves to %q with no toolDetailKind entry", alias, semantic)
		}
	}
}
