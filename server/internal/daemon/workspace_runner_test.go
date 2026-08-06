package daemon

import "testing"

func TestWorkspaceRunnerURLIsScopedWithoutRuntimeIDs(t *testing.T) {
	got, err := workspaceRunnerURL("https://api.example.com/multica", "workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	const want = "wss://api.example.com/multica/api/daemon/ws?workspace_id=workspace-1"
	if got != want {
		t.Fatalf("workspaceRunnerURL() = %q, want %q", got, want)
	}
}
