package handler

import "testing"

func TestIsCursorLikeSubagentTool(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		tool  string
		input map[string]any
		want  bool
	}{
		{name: "task", tool: "Task", want: true},
		{name: "task_v2", tool: "task_tool", want: true},
		{name: "subagent name", tool: "launch_subagent", want: true},
		{name: "best_of_n", tool: "best_of_n", want: true},
		{name: "subagent_type input", tool: "run", input: map[string]any{"subagent_type": "Explore"}, want: true},
		{name: "shell", tool: "shell", want: false},
		{name: "read", tool: "read_file", want: false},
		{name: "empty", tool: "", want: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isCursorLikeSubagentTool(tc.tool, tc.input); got != tc.want {
				t.Fatalf("isCursorLikeSubagentTool(%q) = %v, want %v", tc.tool, got, tc.want)
			}
		})
	}
}

func TestCursorSubagentStartedMessage(t *testing.T) {
	t.Parallel()
	if got := cursorSubagentStartedMessage("Task", map[string]any{"description": "Explore daemon"}); got != "Subagent started: Explore daemon" {
		t.Fatalf("got %q", got)
	}
	if got := cursorSubagentStartedMessage("Task", map[string]any{"subagent_type": "Explore"}); got != "Subagent started: Explore" {
		t.Fatalf("got %q", got)
	}
	if got := cursorSubagentStartedMessage("Task", nil); got != "Subagent started: Task" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeActivityToolSlug(t *testing.T) {
	t.Parallel()
	if got := normalizeActivityToolSlug("Best-Of_N"); got != "bestofn" {
		t.Fatalf("got %q", got)
	}
}
