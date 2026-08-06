package handler

import "testing"

// TestRelativeWorkDir covers the privacy-safe display derivation that
// agent-transcript dialogs render in the work_dir chip. Two concerns drive
// the table:
//
//  1. Durable agent workspaces must strip the daemon's workspace root so the chip
//     doesn't expose the user's home directory or username (the bug in
//     PR #3379 that this fix replaces).
//  2. Unexpected provider paths must not leak `/Users/<name>/...`,
//     `/home/<name>/...`, or `<drive>:/Users/<name>/...`. The function strips
//     recognised home prefixes and otherwise falls back to the basename.
func TestRelativeWorkDir(t *testing.T) {
	const (
		wsID    = "a05b0e10-ee7a-4603-a72d-a548b2390cb2"
		agentID = "5c57b65b-ee7a-4603-a72d-a548b2390cb2"
	)

	tests := []struct {
		name     string
		workDir  string
		wsID     string
		agentID  string
		expected string
	}{
		{
			name:     "empty work_dir returns empty",
			workDir:  "",
			wsID:     wsID,
			agentID:  agentID,
			expected: "",
		},
		{
			name:     "durable agent path strips workspace root",
			workDir:  "/Users/alice/.multica/workspaces/" + wsID + "/agents/" + agentID,
			wsID:     wsID,
			agentID:  agentID,
			expected: wsID + "/agents/" + agentID,
		},
		{
			name:     "durable agent path with nested child preserves suffix",
			workDir:  "/Users/alice/.multica/workspaces/" + wsID + "/agents/" + agentID + "/repo",
			wsID:     wsID,
			agentID:  agentID,
			expected: wsID + "/agents/" + agentID + "/repo",
		},
		{
			name:     "unexpected path under /Users home is stripped",
			workDir:  "/Users/df007df/repos/foo",
			wsID:     wsID,
			agentID:  agentID,
			expected: "repos/foo",
		},
		{
			name:     "unexpected deep path under home keeps full remainder",
			workDir:  "/Users/df007df/code/work/projects/multica/foo",
			wsID:     wsID,
			agentID:  agentID,
			expected: "code/work/projects/multica/foo",
		},
		{
			name:     "shallow /Users home path strips username segment",
			workDir:  "/Users/alice/foo",
			wsID:     wsID,
			agentID:  agentID,
			expected: "foo",
		},
		{
			name:     "shallow Linux /home path strips username segment",
			workDir:  "/home/alice/project",
			wsID:     wsID,
			agentID:  agentID,
			expected: "project",
		},
		{
			name:     "shallow Windows /Users path strips username segment",
			workDir:  `C:\Users\alice\foo`,
			wsID:     wsID,
			agentID:  agentID,
			expected: "foo",
		},
		{
			name:     "exact home directory returns empty (would only render username)",
			workDir:  "/Users/alice",
			wsID:     wsID,
			agentID:  agentID,
			expected: "",
		},
		{
			name:     "exact home directory with trailing slash returns empty",
			workDir:  "/Users/alice/",
			wsID:     wsID,
			agentID:  agentID,
			expected: "",
		},
		{
			name:     "unexpected Windows path under home strips username",
			workDir:  `C:\Users\alice\repos\foo`,
			wsID:     wsID,
			agentID:  agentID,
			expected: "repos/foo",
		},
		{
			name:     "non-home local path falls back to basename only",
			workDir:  "/opt/foo",
			wsID:     wsID,
			agentID:  agentID,
			expected: "foo",
		},
		{
			name:     "non-home deep local path falls back to basename only",
			workDir:  "/srv/git/repo",
			wsID:     wsID,
			agentID:  agentID,
			expected: "repo",
		},
		{
			name:     "single-segment local path returns the segment",
			workDir:  "/foo",
			wsID:     wsID,
			agentID:  agentID,
			expected: "foo",
		},
		{
			name:     "Windows durable agent path is normalized",
			workDir:  `C:\Users\alice\.multica\workspaces\` + wsID + `\agents\` + agentID,
			wsID:     wsID,
			agentID:  agentID,
			expected: wsID + "/agents/" + agentID,
		},
		{
			name:     "missing workspace_id under home strips home prefix",
			workDir:  "/Users/alice/other-workspaces/" + wsID + "/old-workdir",
			wsID:     "",
			agentID:  agentID,
			expected: "other-workspaces/" + wsID + "/old-workdir",
		},
		{
			name:     "missing agent_id under home strips home prefix instead of canonical path",
			workDir:  "/Users/alice/other-workspaces/" + wsID + "/old-workdir",
			wsID:     wsID,
			agentID:  "",
			expected: "other-workspaces/" + wsID + "/old-workdir",
		},
		{
			name:     "trailing slash on canonical path is preserved in returned suffix",
			workDir:  "/Users/alice/.multica/workspaces/" + wsID + "/agents/" + agentID + "/",
			wsID:     wsID,
			agentID:  agentID,
			expected: wsID + "/agents/" + agentID + "/",
		},
		{
			name:     "wsID prefix appearing elsewhere falls back to basename when not under home",
			workDir:  "/var/" + wsID + "/something/else",
			wsID:     wsID,
			agentID:  agentID,
			expected: "else",
		},
		{
			name:     "case-insensitive /users matches the same as /Users",
			workDir:  "/users/alice/repos/foo",
			wsID:     wsID,
			agentID:  agentID,
			expected: "repos/foo",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := relativeWorkDir(tc.workDir, tc.wsID, tc.agentID)
			if got != tc.expected {
				t.Fatalf("relativeWorkDir(%q, %q, %q) = %q, want %q",
					tc.workDir, tc.wsID, tc.agentID, got, tc.expected)
			}
		})
	}
}
