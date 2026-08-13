package daemon

import (
	"fmt"
	"strings"
)

// Tool-use Activity facts, ported from Raft's shared/src/toolDisplay.ts
// (@botiverse/raft-daemon 1.0.15) so Runner Activity reads Raft-aligned:
// every tool call gets its semantic detail kind (the server projection owns
// the gerund label per kind) plus a bounded input summary as the timeline
// subtext. Commands truncate to 100 runes at the source, matching Raft.
// No redaction (Frank 2026-08-07: 对齐 Raft — Raft does not redact the
// activity summary either).

// toolSemanticAliases normalizes provider tool slugs (Claude `Read`/`Bash`,
// Codex `exec_command`, OpenCode `bash`/`glob`, Grok `read_file`, …) to the
// canonical Raft semantic. Port of the retired handler-side alias table.
var toolSemanticAliases = map[string]string{
	"send_message": "send_message",
	"message_send": "send_message",

	"check_messages":       "check_messages",
	"wait_for_message":     "wait_for_message",
	"receive_message":      "receive_message",
	"read_messages":        "read_history",
	"read_history":         "read_history",
	"search_messages":      "search_messages",
	"list_server":          "list_server",
	"list_tasks":           "list_tasks",
	"create_tasks":         "create_tasks",
	"claim_tasks":          "claim_tasks",
	"unclaim_task":         "unclaim_task",
	"update_task_status":   "update_task_status",
	"add_channel_member":   "add_channel_member",
	"join_channel":         "join_channel",
	"leave_channel":        "leave_channel",
	"upload_file":          "upload_file",
	"view_file":            "view_file",
	"list_issues":          "list_issues",
	"get_issue":            "get_issue",
	"search_issues":        "search_issues",
	"list_issue_comments":  "list_issue_comments",
	"comment_issue":        "comment_issue",
	"delete_issue_comment": "delete_issue_comment",

	"bash":                 "bash",
	"shell":                "bash",
	"sh":                   "bash",
	"zsh":                  "bash",
	"exec":                 "bash",
	"exec_command":         "bash",
	"command":              "bash",
	"command_execution":    "bash",
	"run_terminal_command": "bash",
	"run_shell_command":    "bash",
	"terminal":             "bash",

	"read":      "read_file",
	"readfile":  "read_file",
	"read_file": "read_file",
	"file_read": "read_file",
	"open":      "read_file",
	"cat":       "read_file",

	"write":       "write_file",
	"writefile":   "write_file",
	"write_file":  "write_file",
	"file_write":  "write_file",
	"create":      "write_file",
	"createfile":  "write_file",
	"create_file": "write_file",

	"edit":           "edit_file",
	"editfile":       "edit_file",
	"edit_file":      "edit_file",
	"file_edit":      "edit_file",
	"file_change":    "edit_file",
	"strreplacefile": "edit_file",
	"str_replace":    "edit_file",
	"multi_edit":     "edit_file",
	"patch_apply":    "edit_file",

	"glob":         "glob",
	"search_files": "glob",

	"grep":        "grep",
	"rg":          "grep",
	"search":      "grep",
	"search_code": "grep",

	"web_fetch":  "web_fetch",
	"webfetch":   "web_fetch",
	"fetchurl":   "web_fetch",
	"fetch_url":  "web_fetch",
	"web_search": "web_search",
	"websearch":  "web_search",
	"searchweb":  "web_search",
	"search_web": "web_search",

	"todowrite":         "todo_write",
	"todo_write":        "todo_write",
	"set_todo_list":     "todo_write",
	"settodolist":       "todo_write",
	"schedule_reminder": "schedule_reminder",
	"list_reminders":    "list_reminders",
	"snooze_reminder":   "snooze_reminder",
	"update_reminder":   "update_reminder",
	"cancel_reminder":   "cancel_reminder",
	"log_reminder":      "log_reminder",
	"collab_tool_call":  "collab_tool_call",
}

// canonicalToolSemantic folds a provider tool slug to the canonical Raft
// semantic. The boolean reports whether the slug is a known tool at all —
// unknown tools must still reach the timeline (Raft: "Using <name>…"),
// never silently dropped.
func canonicalToolSemantic(raw string) (string, bool) {
	tool := strings.ToLower(strings.TrimSpace(raw))
	tool = strings.TrimPrefix(tool, "mcp_chat_")
	if strings.HasPrefix(tool, "mcp__") {
		parts := strings.Split(tool, "__")
		tool = parts[len(parts)-1]
	}
	tool = strings.TrimSpace(strings.TrimPrefix(tool, "tool:"))
	if canonical, ok := toolSemanticAliases[tool]; ok {
		return canonical, true
	}
	return tool, false
}

// toolSummaryKind mirrors Raft's TOOL_DISPLAY_METADATA summaryKind: which
// input field becomes the timeline subtext for each semantic.
var toolSummaryKind = map[string]string{
	"send_message":       "message_target",
	"check_messages":     "none",
	"wait_for_message":   "none",
	"receive_message":    "none",
	"read_history":       "history_target",
	"search_messages":    "query",
	"list_server":        "none",
	"list_tasks":         "channel",
	"create_tasks":       "channel",
	"claim_tasks":        "claim_tasks",
	"unclaim_task":       "task_ref",
	"update_task_status": "task_ref",
	"add_channel_member": "target",
	"join_channel":       "target",
	"leave_channel":      "target",
	"upload_file":        "file_path",
	"view_file":          "none",
	"read_file":          "file_path",
	"write_file":         "file_path",
	"edit_file":          "file_path",
	"bash":               "command",
	"glob":               "pattern",
	"grep":               "pattern",
	"web_fetch":          "url",
	"web_search":         "query",
	"todo_write":         "none",
	"schedule_reminder":  "reminder_title",
	"list_reminders":     "none",
	"cancel_reminder":    "reminder_id",
	"collab_tool_call":   "none",
	// Multica extensions with no Raft counterpart (issue tools, extra
	// reminder ops). The CLI resolver below is their usual source.
	"list_issues":          "none",
	"get_issue":            "issue",
	"search_issues":        "query",
	"list_issue_comments":  "issue",
	"comment_issue":        "issue",
	"delete_issue_comment": "issue",
	"snooze_reminder":      "reminder_id",
	"update_reminder":      "reminder_id",
	"log_reminder":         "none",
}

// toolDetailKind maps the canonical semantic to the wire detail kind. The
// server projection owns one gerund label per detail kind, so this vocabulary
// is the daemon↔server contract for tool activity.
var toolDetailKind = map[string]string{
	"bash":                 "running_command",
	"read_file":            "reading_file",
	"write_file":           "writing_file",
	"edit_file":            "editing_file",
	"glob":                 "searching_files",
	"grep":                 "searching_code",
	"web_fetch":            "fetching_url",
	"web_search":           "searching_web",
	"todo_write":           "updating_tasks",
	"send_message":         "sending_message",
	"check_messages":       "checking_messages",
	"receive_message":      "checking_messages",
	"wait_for_message":     "waiting_for_message",
	"read_history":         "reading_history",
	"search_messages":      "searching_messages",
	"list_server":          "listing_server",
	"list_tasks":           "listing_tasks",
	"create_tasks":         "creating_tasks",
	"claim_tasks":          "claiming_task",
	"unclaim_task":         "unclaiming_task",
	"update_task_status":   "updating_task_status",
	"add_channel_member":   "adding_channel_member",
	"join_channel":         "joining_channel",
	"leave_channel":        "leaving_channel",
	"upload_file":          "uploading_file",
	"view_file":            "viewing_file",
	"list_issues":          "listing_issues",
	"get_issue":            "getting_issue",
	"search_issues":        "searching_issues",
	"list_issue_comments":  "listing_issue_comments",
	"comment_issue":        "commenting_issue",
	"delete_issue_comment": "deleting_issue_comment",
	"schedule_reminder":    "scheduling_reminder",
	"list_reminders":       "listing_reminders",
	"cancel_reminder":      "canceling_reminder",
	"snooze_reminder":      "snoozing_reminder",
	"update_reminder":      "updating_reminder",
	"log_reminder":         "logging_reminder",
	"collab_tool_call":     "collaborating",
}

// maxActivityCommandRunes matches Raft's source-side command clip (100).
const maxActivityCommandRunes = 100

// toolActivityFact builds the wire fact for a tool-use observation: the
// detail kind the projection labels, and the narrative text that becomes the
// timeline subtext (empty when the tool carries no displayable input — the
// projection then renders the label alone).
func toolActivityFact(tool string, input map[string]any) (detailKind, narrative string) {
	// Raft parity: a shell command that is really a `multica` CLI call
	// (`bash -c "multica message send …"`) is reclassified to the semantic
	// tool, never shown as a raw "Running command".
	if semantic, summary, ok := resolveMulticaCLIInvocation(tool, input); ok {
		return toolDetailKind[semantic], summary
	}
	semantic, known := canonicalToolSemantic(tool)
	if !known {
		// Raft: unknown tools are never silent — "Using <name>…".
		return "other", "Using " + truncateRunes(semantic, 40)
	}
	return toolDetailKind[semantic], summarizeToolInput(semantic, input)
}

// summarizeToolInput is Raft's summaryKind switch: pick the one input field
// worth showing for the semantic, bounded at the source.
func summarizeToolInput(semantic string, input map[string]any) string {
	switch toolSummaryKind[semantic] {
	case "file_path":
		return firstNonEmptyString(stringFromInput(input, "file_path"), stringFromInput(input, "path"))
	case "command":
		return truncateRunes(commandFromToolInput(input), maxActivityCommandRunes)
	case "pattern":
		return firstNonEmptyString(stringFromInput(input, "pattern"), stringFromInput(input, "query"))
	case "query":
		return stringFromInput(input, "query")
	case "url":
		return stringFromInput(input, "url")
	case "message_target":
		if target := firstNonEmptyString(stringFromInput(input, "target"), stringFromInput(input, "channel")); target != "" {
			return target
		}
		if dm := stringFromInput(input, "dm_to"); dm != "" {
			return "DM:@" + dm
		}
		return ""
	case "history_target":
		return firstNonEmptyString(stringFromInput(input, "target"), stringFromInput(input, "channel"))
	case "channel":
		return stringFromInput(input, "channel")
	case "claim_tasks":
		channel := stringFromInput(input, "channel")
		if tasks := formatTaskNumbers(input["task_numbers"]); channel != "" && tasks != "" {
			return channel + " " + tasks
		}
		return channel
	case "task_ref":
		channel := stringFromInput(input, "channel")
		if channel == "" {
			return ""
		}
		if number, ok := input["task_number"].(float64); ok {
			return fmt.Sprintf("%s #t%d", channel, int64(number))
		}
		return ""
	case "target":
		return stringFromInput(input, "target")
	case "reminder_title":
		return truncateRunes(stringFromInput(input, "title"), 40)
	case "reminder_id":
		// Raft: `#${id.slice(0, 8)}` — a plain prefix, no ellipsis.
		if id := stringFromInput(input, "reminder_id"); id != "" {
			runes := []rune(id)
			if len(runes) > 8 {
				runes = runes[:8]
			}
			return "#" + string(runes)
		}
		return ""
	case "issue":
		return firstNonEmptyString(
			stringFromInput(input, "issue"),
			stringFromInput(input, "issue_id"),
			stringFromInput(input, "id"),
			stringFromInput(input, "target"),
		)
	default:
		return ""
	}
}

func formatTaskNumbers(value any) string {
	list, ok := value.([]any)
	if !ok {
		return ""
	}
	numbers := make([]string, 0, len(list))
	for _, item := range list {
		if number, ok := item.(float64); ok {
			numbers = append(numbers, fmt.Sprintf("#t%d", int64(number)))
		}
	}
	return strings.Join(numbers, " ")
}

// resolveMulticaCLIInvocation reclassifies a shell command that is really a
// `multica` (or raft/slock) CLI call into its semantic tool, so the timeline
// shows "Sending message… · #general" instead of "Running command… · multica
// message send …". Port of the retired handler-side resolveRaftCLIInvocation.
func resolveMulticaCLIInvocation(tool string, input map[string]any) (semantic, summary string, ok bool) {
	if canonical, _ := canonicalToolSemantic(tool); canonical != "bash" {
		return "", "", false
	}
	rawCommand := commandFromToolInput(input)
	if rawCommand == "" {
		return "", "", false
	}
	tokens := shellCommandTokens(rawCommand)
	executableIndex := 0
	for executableIndex < len(tokens) && isEnvAssignmentToken(tokens[executableIndex]) {
		executableIndex++
	}
	if executableIndex >= len(tokens) {
		return "", "", false
	}
	if isShellExecutableToken(tokens[executableIndex]) {
		if inner := shellCCommand(tokens[executableIndex+1:]); inner != "" {
			return resolveMulticaCLIInvocation(tool, map[string]any{"command": inner})
		}
	}
	cliIndex := findCLIExecutableIndex(tokens)
	if cliIndex < 0 || cliIndex+2 >= len(tokens) {
		return "", "", false
	}
	args := tokens[cliIndex+1:]
	resource, action, rest := args[0], args[1], args[2:]
	channelOrTarget := func() string {
		return firstNonEmptyString(optionValue(rest, "--channel"), optionValue(rest, "--target"))
	}
	switch resource + " " + action {
	case "message send":
		return "send_message", optionValue(rest, "--target"), true
	case "message check":
		return "check_messages", "", true
	case "message read":
		return "read_history", channelOrTarget(), true
	case "message search":
		return "search_messages", optionValue(rest, "--query"), true
	case "server info":
		return "list_server", "", true
	case "task list":
		return "list_tasks", channelOrTarget(), true
	case "task create":
		return "create_tasks", channelOrTarget(), true
	case "task claim":
		return "claim_tasks", channelOrTarget(), true
	case "task unclaim":
		return "unclaim_task", channelOrTarget(), true
	case "task update":
		return "update_task_status", channelOrTarget(), true
	case "channel add-member":
		return "add_channel_member", optionValue(rest, "--target"), true
	case "channel join":
		return "join_channel", optionValue(rest, "--target"), true
	case "channel leave":
		return "leave_channel", optionValue(rest, "--target"), true
	case "attachment upload":
		return "upload_file", optionValue(rest, "--path"), true
	case "attachment view":
		return "view_file", "", true
	case "reminder schedule":
		return "schedule_reminder", optionValue(rest, "--title"), true
	case "reminder list":
		return "list_reminders", "", true
	case "reminder snooze":
		return "snooze_reminder", summarizeToolInput("snooze_reminder", map[string]any{"reminder_id": optionValue(rest, "--id")}), true
	case "reminder update":
		return "update_reminder", summarizeToolInput("update_reminder", map[string]any{"reminder_id": optionValue(rest, "--id")}), true
	case "reminder cancel":
		return "cancel_reminder", optionValue(rest, "--id"), true
	case "reminder log":
		return "log_reminder", "", true
	case "issue list":
		return "list_issues", "", true
	case "issue get":
		return "get_issue", positionalArg(rest, 0), true
	case "issue search":
		return "search_issues", positionalArg(rest, 0), true
	case "issue comment":
		switch positionalArg(rest, 0) {
		case "list":
			return "list_issue_comments", positionalArg(rest, 1), true
		case "add":
			return "comment_issue", positionalArg(rest, 1), true
		case "delete":
			return "delete_issue_comment", positionalArg(rest, 1), true
		}
	}
	return "", "", false
}

// commandFromToolInput extracts the shell command from a tool-use input map.
// Providers normalize command-family tools (bash/exec/exec_command) to a
// `command` string; `cmd`/`shell_command` cover providers that pass a native
// key through.
func commandFromToolInput(input map[string]any) string {
	for _, key := range []string{"command", "cmd", "shell_command"} {
		if value := stringFromInput(input, key); value != "" {
			return value
		}
	}
	return ""
}

func stringFromInput(input map[string]any, key string) string {
	value, _ := input[key].(string)
	return strings.TrimSpace(value)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// truncateRunes bounds s to max runes, appending an ellipsis when clipped
// (Raft's source-side clip).
func truncateRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// shellCommandTokens splits a command line honoring single/double quotes and
// backslash escapes. Port of the retired handler-side tokenizer.
func shellCommandTokens(command string) []string {
	var tokens []string
	var current strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	for _, r := range command {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			flush()
			continue
		}
		current.WriteRune(r)
	}
	flush()
	return tokens
}

func isEnvAssignmentToken(token string) bool {
	eq := strings.Index(token, "=")
	return eq > 0 && !strings.Contains(token[:eq], "/") && !strings.HasPrefix(token, "-")
}

func isShellExecutableToken(token string) bool {
	switch basenameToken(token) {
	case "sh", "bash", "zsh", "fish":
		return true
	default:
		return false
	}
}

func shellCCommand(args []string) string {
	for i, arg := range args {
		if arg == "-c" || (strings.HasPrefix(arg, "-") && strings.Contains(arg, "c")) {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		if !strings.HasPrefix(arg, "-") {
			return ""
		}
	}
	return ""
}

func findCLIExecutableIndex(tokens []string) int {
	for i, token := range tokens {
		switch basenameToken(token) {
		case "raft", "slock", "multica":
			return i
		}
	}
	return -1
}

func basenameToken(token string) string {
	token = strings.TrimSpace(strings.Trim(token, `"'`))
	parts := strings.FieldsFunc(token, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	if len(parts) == 0 {
		return strings.ToLower(token)
	}
	return strings.ToLower(parts[len(parts)-1])
}

func optionValue(args []string, flag string) string {
	for i := len(args) - 1; i >= 0; i-- {
		arg := args[i]
		if strings.HasPrefix(arg, flag+"=") {
			return strings.TrimSpace(arg[len(flag)+1:])
		}
		if arg == flag && i+1 < len(args) {
			return strings.TrimSpace(args[i+1])
		}
	}
	return ""
}

func positionalArg(args []string, index int) string {
	if index < 0 {
		return ""
	}
	position := 0
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		// Positional identifiers precede flags in every supported CLI grammar.
		// Stop at the option boundary so an option value can never be mistaken
		// for a user-facing issue or comment target.
		if strings.HasPrefix(arg, "-") {
			break
		}
		if position == index {
			return arg
		}
		position++
	}
	return ""
}
