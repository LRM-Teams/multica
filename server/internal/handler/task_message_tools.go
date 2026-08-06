package handler

import "strings"

// taskMessageToolIsMapped keeps unrecognized runtime tool calls out of the
// user-facing Task stream. This is a Task Message safety boundary, independent
// of the retired Activity event contract.
func taskMessageToolIsMapped(messageType, tool string, input map[string]any) bool {
	if messageType != "tool_use" || strings.TrimSpace(tool) == "" {
		return true
	}
	_, known := taskMessageCanonicalToolName(tool, input)
	return known
}

func taskMessageCanonicalToolName(raw string, input map[string]any) (string, bool) {
	tool := strings.ToLower(strings.TrimSpace(raw))
	tool = strings.TrimPrefix(tool, "mcp_chat_")
	if strings.HasPrefix(tool, "mcp__") {
		parts := strings.Split(tool, "__")
		tool = parts[len(parts)-1]
	}
	tool = strings.TrimSpace(strings.TrimPrefix(tool, "tool:"))
	if canonical, ok := taskMessageToolAliases[tool]; ok {
		return canonical, true
	}
	if (tool == "running" || tool == "in_progress" || tool == "pending") && taskMessageHasShellCommand(input) {
		return "bash", true
	}
	return tool, false
}

func taskMessageHasShellCommand(input map[string]any) bool {
	for _, key := range []string{"cmd", "command", "shell_command"} {
		if value, ok := input[key].(string); ok && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

var taskMessageToolAliases = map[string]string{
	"send_message": "send_message", "message_send": "send_message",
	"check_messages": "check_messages", "wait_for_message": "wait_for_message", "receive_message": "receive_message",
	"read_messages": "read_history", "read_history": "read_history", "search_messages": "search_messages",
	"list_server": "list_server", "list_tasks": "list_tasks", "create_tasks": "create_tasks", "claim_tasks": "claim_tasks",
	"unclaim_task": "unclaim_task", "update_task_status": "update_task_status", "add_channel_member": "add_channel_member",
	"join_channel": "join_channel", "leave_channel": "leave_channel", "upload_file": "upload_file", "view_file": "view_file",
	"list_issues": "list_issues", "get_issue": "get_issue", "search_issues": "search_issues", "list_issue_comments": "list_issue_comments", "comment_issue": "comment_issue", "delete_issue_comment": "delete_issue_comment",
	"bash": "bash", "shell": "bash", "sh": "bash", "zsh": "bash", "exec": "bash", "exec_command": "bash", "command": "bash", "command_execution": "bash", "run_terminal_command": "bash", "run_shell_command": "bash", "terminal": "bash",
	"read": "read_file", "readfile": "read_file", "read_file": "read_file", "file_read": "read_file", "open": "read_file", "cat": "read_file",
	"write": "write_file", "writefile": "write_file", "write_file": "write_file", "file_write": "write_file", "create": "write_file", "createfile": "write_file", "create_file": "write_file",
	"edit": "edit_file", "editfile": "edit_file", "edit_file": "edit_file", "file_edit": "edit_file", "file_change": "edit_file", "strreplacefile": "edit_file", "str_replace": "edit_file", "multi_edit": "edit_file", "patch_apply": "edit_file",
	"glob": "glob", "search_files": "glob", "grep": "grep", "rg": "grep", "search": "grep", "search_code": "grep",
	"web_fetch": "web_fetch", "webfetch": "web_fetch", "fetchurl": "web_fetch", "fetch_url": "web_fetch", "web_search": "web_search", "websearch": "web_search", "searchweb": "web_search", "search_web": "web_search",
	"todowrite": "todo_write", "todo_write": "todo_write", "set_todo_list": "todo_write", "settodolist": "todo_write",
	"schedule_reminder": "schedule_reminder", "list_reminders": "list_reminders", "snooze_reminder": "snooze_reminder", "update_reminder": "update_reminder", "cancel_reminder": "cancel_reminder", "log_reminder": "log_reminder", "collab_tool_call": "collab_tool_call",
}
