package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestDaemonRegistrationCapabilities_GatesCredentialTransport(t *testing.T) {
	legacy := daemonRegistrationCapabilities(false)
	if containsString(legacy, protocol.DaemonCapabilityAgentCredentialTransport) {
		t.Fatalf("legacy capabilities must not include %q: %#v", protocol.DaemonCapabilityAgentCredentialTransport, legacy)
	}
	if !containsString(legacy, protocol.DaemonCapabilityChannelOutputActions) || !containsString(legacy, protocol.DaemonCapabilityAgentCLITransport) {
		t.Fatalf("legacy capabilities missing base entries: %#v", legacy)
	}
	if !containsString(legacy, protocol.DaemonCapabilityRestrictedExecution) {
		t.Fatalf("legacy capabilities missing restricted execution support: %#v", legacy)
	}
	if !containsString(legacy, protocol.DaemonCapabilityMemoryCrossDeviceSync) {
		t.Fatalf("legacy capabilities missing cross-device memory sync support: %#v", legacy)
	}
	if !containsString(legacy, protocol.DaemonCapabilityReminderFireRequest) {
		t.Fatalf("registration missing %q: %#v", protocol.DaemonCapabilityReminderFireRequest, legacy)
	}

	capable := daemonRegistrationCapabilities(true)
	if !containsString(capable, protocol.DaemonCapabilityAgentCredentialTransport) {
		t.Fatalf("capable registration missing %q: %#v", protocol.DaemonCapabilityAgentCredentialTransport, capable)
	}
	if !containsString(legacy, protocol.DaemonCapabilityWorkspaceDaemonAgentProcess) || !containsString(capable, protocol.DaemonCapabilityWorkspaceDaemonAgentProcess) {
		t.Fatalf("registration missing %q: legacy=%#v capable=%#v", protocol.DaemonCapabilityWorkspaceDaemonAgentProcess, legacy, capable)
	}
	if !containsString(legacy, protocol.DaemonCapabilityWorkspaceDaemonAgentReset) || !containsString(capable, protocol.DaemonCapabilityWorkspaceDaemonAgentReset) {
		t.Fatalf("registration missing %q: legacy=%#v capable=%#v", protocol.DaemonCapabilityWorkspaceDaemonAgentReset, legacy, capable)
	}
}

func TestTransportAttemptWasRecorded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transport-attempt")
	if attempted, err := transportAttemptWasRecorded(path); err != nil || attempted {
		t.Fatalf("missing marker = attempted:%v err:%v, want false/nil", attempted, err)
	}
	if err := os.WriteFile(path, []byte("attempted\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(marker): %v", err)
	}
	if attempted, err := transportAttemptWasRecorded(path); err != nil || !attempted {
		t.Fatalf("regular marker = attempted:%v err:%v, want true/nil", attempted, err)
	}
}

func TestDaemonRegister_RevokedWorkspaceBindingDoesNotFallbackToSession(t *testing.T) {
	oldDetect := detectAgentVersion
	oldCheck := checkAgentMinVersion
	detectAgentVersion = func(context.Context, string) (string, error) { return "9.9.9", nil }
	checkAgentMinVersion = func(string, string) error { return nil }
	t.Cleanup(func() {
		detectAgentVersion = oldDetect
		checkAgentMinVersion = oldCheck
	})

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/api/daemon/register" {
			t.Fatalf("path = %q, want /api/daemon/register", got)
		}
		var req struct {
			WorkspaceID  string   `json:"workspace_id"`
			OS           string   `json:"os"`
			Capabilities []string `json:"capabilities"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode register body: %v", err)
		}
		if req.WorkspaceID != "ws-1" {
			t.Fatalf("workspace_id = %q, want ws-1", req.WorkspaceID)
		}
		if req.OS != runtime.GOOS {
			t.Fatalf("os = %q, want %q", req.OS, runtime.GOOS)
		}

		switch call := calls.Add(1); call {
		case 1:
			if got := r.Header.Get("Authorization"); got != "Bearer mdt-old" {
				t.Fatalf("call 1 Authorization = %q, want stale daemon token", got)
			}
			if !containsString(req.Capabilities, protocol.DaemonCapabilityAgentCredentialTransport) {
				t.Fatalf("call 1 should advertise credential transport before token rejection: %#v", req.Capabilities)
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid daemon token"}`))
		default:
			t.Fatalf("unexpected register call %d", call)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.SetWorkspaceDaemonToken("ws-1", "mdt-old", time.Now().Add(time.Hour))
	c.SetRuntimeDaemonToken("old-rt", "mdt-old", time.Now().Add(time.Hour))

	d := &Daemon{
		cfg: Config{
			DaemonID: "daemon-1",
			Agents: map[string]AgentEntry{
				"pi": {Path: "pi"},
			},
		},
		client:        c,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		agentVersions: make(map[string]string),
		workspaces: map[string]*workspaceState{
			"ws-1": newWorkspaceState("ws-1", []string{"old-rt"}),
		},
		runtimeIndex: map[string]Runtime{
			"old-rt": {ID: "old-rt", WorkspaceID: "ws-1", Provider: "pi"},
		},
	}

	if _, err := d.registerRuntimesForWorkspace(context.Background(), "ws-1"); err == nil {
		t.Fatal("revoked Binding credential must fail closed")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("register calls = %d, want 1 (no broad-session fallback)", got)
	}
	if got := c.tokenForRuntime("old-rt"); got != "mdt-old" {
		t.Fatalf("old runtime token = %q, want rejected credential retained until explicit setup repair", got)
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func createDaemonTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", dir},
		{"-C", dir, "commit", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git setup failed: %s: %v", out, err)
		}
	}
	return dir
}

func TestNormalizeServerBaseURL(t *testing.T) {
	t.Parallel()

	got, err := NormalizeServerBaseURL("ws://localhost:8080/ws")
	if err != nil {
		t.Fatalf("NormalizeServerBaseURL returned error: %v", err)
	}
	if got != "http://localhost:8080" {
		t.Fatalf("expected http://localhost:8080, got %s", got)
	}
}

func TestProviderNeedsInlineSystemPrompt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		provider string
		want     bool
	}{
		{provider: "kiro", want: true},
		{provider: "codex", want: false},
		{provider: "claude", want: false},
		{provider: "grok", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			t.Parallel()
			if got := providerNeedsInlineSystemPrompt(tc.provider); got != tc.want {
				t.Fatalf("providerNeedsInlineSystemPrompt(%q) = %v, want %v", tc.provider, got, tc.want)
			}
		})
	}
}

// TestComposeOpenclawIncludeRoots — the Elon must-fix regression: the
// daemon must grant OpenClaw permission to follow the wrapper's $include
// link from envRoot into the user's active config dir, while preserving
// any roots the user already configured in their shell env so their own
// cross-directory layouts keep working.
func TestBuildPromptContainsIssueID(t *testing.T) {
	t.Parallel()

	issueID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	prompt := BuildPrompt(Task{
		IssueID: issueID,
		Agent: &AgentData{
			Name: "Local Codex",
			Skills: []SkillData{
				{Name: "Concise", Content: "Be concise."},
			},
		},
	}, "claude", "")

	// Prompt should contain the issue ID and CLI hint.
	for _, want := range []string{
		issueID,
		"multica issue get",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}

	// Skills should NOT be inlined in the prompt (they're in runtime config).
	for _, absent := range []string{"## Agent Skills", "Be concise."} {
		if strings.Contains(prompt, absent) {
			t.Fatalf("prompt should NOT contain %q (skills are in runtime config)", absent)
		}
	}
}

func TestBuildPromptNoIssueDetails(t *testing.T) {
	t.Parallel()

	prompt := BuildPrompt(Task{
		IssueID: "test-id",
		Agent:   &AgentData{Name: "Test"},
	}, "claude", "")

	// Prompt should not contain issue title/description (agent fetches via CLI).
	for _, absent := range []string{"**Issue:**", "**Summary:**"} {
		if strings.Contains(prompt, absent) {
			t.Fatalf("prompt should NOT contain %q — agent fetches details via CLI", absent)
		}
	}
}

func TestBuildPromptAutopilotRunOnly(t *testing.T) {
	t.Parallel()

	prompt := BuildPrompt(Task{
		AutopilotRunID:       "run-1",
		AutopilotID:          "autopilot-1",
		AutopilotTitle:       "Daily dependency check",
		AutopilotDescription: "Check dependencies and report outdated packages.",
		AutopilotSource:      "manual",
	}, "claude", "")

	for _, want := range []string{
		"run-only mode",
		"Autopilot run ID: run-1",
		"Daily dependency check",
		"Check dependencies and report outdated packages.",
		"Complete the instructions above",
		"Do not run `multica issue get`",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("autopilot prompt missing %q\n---\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "multica autopilot get") {
		t.Fatalf("autopilot CLI is retired; prompt must not suggest multica autopilot get\n---\n%s", prompt)
	}

	if strings.Contains(prompt, "Your assigned issue ID is:") {
		t.Fatalf("autopilot prompt should not use issue assignment template\n---\n%s", prompt)
	}
}

func TestBuildPromptCommentTriggered(t *testing.T) {
	t.Parallel()

	issueID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	commentID := "c1c2c3c4-d5d6-7890-abcd-ef1234567890"
	commentContent := "请把报告翻译成英文"

	prompt := BuildPrompt(Task{
		IssueID:               issueID,
		TriggerCommentID:      commentID,
		TriggerCommentContent: commentContent,
		Agent:                 &AgentData{Name: "Test"},
	}, "claude", "")

	// Prompt should contain the comment content, the trigger comment id, and
	// the full reply command with --parent. Re-emitting --parent on every turn
	// is what prevents resumed sessions from reusing the previous turn's
	// --parent UUID.
	for _, want := range []string{
		issueID,
		commentContent,
		"Focus on THIS comment",
		commentID,
		"multica issue comment add " + issueID + " --parent " + commentID,
		"do NOT reuse --parent values from previous turns",
		// Silence-as-valid-exit for agent-to-agent loops depends on the
		// reply command being framed conditionally rather than as a hard
		// requirement. Guard the phrasing so the conflict with the new
		// workflow (MUL-1323) doesn't come back.
		"If you decide to reply",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q\n---\n%s", want, prompt)
		}
	}

	// Should still contain CLI hint for fetching issue context.
	if !strings.Contains(prompt, "multica issue get") {
		t.Fatal("prompt missing CLI hint for issue context")
	}
}

// TestBuildPromptCommentTriggeredByAgent covers the agent-to-agent mention
// loop signal injected into the per-turn prompt (MUL-1323 / GH#1576). When
// the triggering comment was posted by another agent, the prompt must name
// the author, warn against sign-off @mentions, and point at silence as a
// valid exit.
func TestBuildPromptCommentTriggeredByAgent(t *testing.T) {
	t.Parallel()

	prompt := BuildPrompt(Task{
		IssueID:               "issue-1",
		TriggerCommentID:      "comment-1",
		TriggerCommentContent: "thanks, looks good!",
		TriggerAuthorType:     "agent",
		TriggerAuthorName:     "Atlas",
		Agent:                 &AgentData{Name: "Test"},
	}, "claude", "")

	for _, want := range []string{
		"Another agent (Atlas)",
		"do not @mention the other agent as a sign-off",
		"Silence is the preferred way",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q\n---\n%s", want, prompt)
		}
	}
}

// TestBuildPromptCommentTriggeredByMember guards against the agent-loop warning
// leaking into human-authored triggers — a human asking a question should not
// be pre-discouraged from getting a reply.
func TestBuildPromptCommentTriggeredByMember(t *testing.T) {
	t.Parallel()

	prompt := BuildPrompt(Task{
		IssueID:               "issue-1",
		TriggerCommentID:      "comment-1",
		TriggerCommentContent: "can you translate this?",
		TriggerAuthorType:     "member",
		TriggerAuthorName:     "Alice",
		Agent:                 &AgentData{Name: "Test"},
	}, "claude", "")

	if !strings.Contains(prompt, "A user just left a new comment") {
		t.Fatalf("member-triggered prompt should label the author as a user\n---\n%s", prompt)
	}
	if strings.Contains(prompt, "Another agent") {
		t.Fatalf("member-triggered prompt should not claim the author was another agent")
	}
	// Must NOT use the old "You MUST respond" language — that conflicts with
	// the agent-to-agent silence-as-valid-exit workflow. Even on human-authored
	// triggers, the reply command is framed conditionally for a single
	// consistent rule across turn types.
	if strings.Contains(prompt, "MUST respond") {
		t.Fatalf("prompt should not contain unconditional \"MUST respond\" language\n---\n%s", prompt)
	}
	if !strings.Contains(prompt, "If you decide to reply") {
		t.Fatalf("prompt should frame the reply command conditionally\n---\n%s", prompt)
	}
}

func TestBuildPromptCommentTriggeredNoContent(t *testing.T) {
	t.Parallel()

	// When TriggerCommentID is set but content is empty (e.g. fetch failed),
	// it should still use the comment prompt path.
	prompt := BuildPrompt(Task{
		IssueID:          "test-id",
		TriggerCommentID: "comment-id",
		Agent:            &AgentData{Name: "Test"},
	}, "claude", "")

	if !strings.Contains(prompt, "multica issue get") {
		t.Fatal("prompt missing CLI hint")
	}
}

// TestBuildPromptSquadLeaderNoActionProhibition verifies that when a squad
// leader is triggered by another agent's comment, the per-turn prompt
// explicitly forbids posting a comment whose only purpose is to announce
// no_action or "exiting silently". This is the fix for MUL-2168.
func TestBuildPromptSquadLeaderNoActionProhibition(t *testing.T) {
	t.Parallel()

	prompt := BuildPrompt(Task{
		IssueID:               "issue-1",
		TriggerCommentID:      "comment-1",
		TriggerCommentContent: "Progress update: tests passing.",
		TriggerAuthorType:     "agent",
		TriggerAuthorName:     "Worker",
		Agent: &AgentData{
			Name:         "Leader",
			Instructions: "You lead the team.\n\n## Squad Operating Protocol\n\nYou are the LEADER.",
		},
	}, "claude", "")

	for _, forbidden := range []string{
		"Squad leader no_action rule",
		"multica squad activity",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("squad product retired: prompt still contains %q\n---\n%s", forbidden, prompt)
		}
	}

	// Non-squad-leader agent should NOT get the squad leader rule.
	nonLeaderPrompt := BuildPrompt(Task{
		IssueID:               "issue-1",
		TriggerCommentID:      "comment-1",
		TriggerCommentContent: "Progress update: tests passing.",
		TriggerAuthorType:     "agent",
		TriggerAuthorName:     "Worker",
		Agent: &AgentData{
			Name:         "Regular",
			Instructions: "You are a regular agent.",
		},
	}, "claude", "")

	if strings.Contains(nonLeaderPrompt, "Squad leader no_action rule") {
		t.Fatalf("non-squad-leader prompt should NOT contain squad leader rule\n---\n%s", nonLeaderPrompt)
	}
}

func TestIsWorkspaceNotFoundError(t *testing.T) {
	t.Parallel()

	err := &requestError{
		Method:     http.MethodPost,
		Path:       "/api/daemon/register",
		StatusCode: http.StatusNotFound,
		Body:       `{"error":"workspace not found"}`,
	}
	if !isWorkspaceNotFoundError(err) {
		t.Fatal("expected workspace not found error to be recognized")
	}

	if isWorkspaceNotFoundError(&requestError{StatusCode: http.StatusInternalServerError, Body: `{"error":"workspace not found"}`}) {
		t.Fatal("did not expect 500 to be treated as workspace not found")
	}
}

func TestIsTaskNotFoundError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "404 with task not found body",
			err: &requestError{
				Method:     http.MethodPost,
				Path:       "/api/daemon/tasks/abc/messages",
				StatusCode: http.StatusNotFound,
				Body:       `{"error":"task not found"}`,
			},
			want: true,
		},
		{
			name: "404 with mixed-case body still matches",
			err: &requestError{
				StatusCode: http.StatusNotFound,
				Body:       `{"error":"Task Not Found"}`,
			},
			want: true,
		},
		{
			name: "500 with same body is not task-not-found",
			err: &requestError{
				StatusCode: http.StatusInternalServerError,
				Body:       `{"error":"task not found"}`,
			},
			want: false,
		},
		{
			name: "404 with workspace-not-found body is not task-not-found",
			err: &requestError{
				StatusCode: http.StatusNotFound,
				Body:       `{"error":"workspace not found"}`,
			},
			want: false,
		},
		{
			name: "non-requestError",
			err:  errors.New("network down"),
			want: false,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isTaskNotFoundError(tc.err); got != tc.want {
				t.Fatalf("isTaskNotFoundError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsAgentNotBoundToRuntimeError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "403 with agent-not-bound body",
			err: &requestError{
				Method:     http.MethodPost,
				Path:       "/api/daemon/runtimes/rt-1/agents/agent-1/credential",
				StatusCode: http.StatusForbidden,
				Body:       `{"error":"agent is not bound to this runtime"}`,
			},
			want: true,
		},
		{
			name: "mixed-case body still matches",
			err: &requestError{
				StatusCode: http.StatusForbidden,
				Body:       `{"error":"Agent Is Not Bound To This Runtime"}`,
			},
			want: true,
		},
		{
			name: "404 with same body must NOT match (wrong status)",
			err: &requestError{
				StatusCode: http.StatusNotFound,
				Body:       `{"error":"agent is not bound to this runtime"}`,
			},
			want: false,
		},
		{
			name: "403 with unrelated body is not a match",
			err: &requestError{
				StatusCode: http.StatusForbidden,
				Body:       `{"error":"daemon token is not bound to this runtime"}`,
			},
			want: false,
		},
		{
			name: "non-requestError is never a match",
			err:  errors.New("boom"),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAgentNotBoundToRuntimeError(tc.err); got != tc.want {
				t.Fatalf("isAgentNotBoundToRuntimeError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestIsRuntimeTransitionInProgressError covers task #38's new 403
// classification, and locks the mutual-exclusivity guarantee the two
// classifiers depend on: a body can never satisfy both
// isAgentNotBoundToRuntimeError and isRuntimeTransitionInProgressError, so a
// caller checking one first never accidentally shadows the other.
func TestIsRuntimeTransitionInProgressError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "403 with transition-in-progress body",
			err: &requestError{
				StatusCode: http.StatusForbidden,
				Body:       `{"error":"runtime_transition_in_progress"}`,
			},
			want: true,
		},
		{
			name: "mixed-case body still matches",
			err: &requestError{
				StatusCode: http.StatusForbidden,
				Body:       `{"error":"Runtime_Transition_In_Progress"}`,
			},
			want: true,
		},
		{
			name: "404 with same body must NOT match (wrong status)",
			err: &requestError{
				StatusCode: http.StatusNotFound,
				Body:       `{"error":"runtime_transition_in_progress"}`,
			},
			want: false,
		},
		{
			name: "403 terminal agent-not-bound body is not a match",
			err: &requestError{
				StatusCode: http.StatusForbidden,
				Body:       `{"error":"agent is not bound to this runtime"}`,
			},
			want: false,
		},
		{
			name: "non-requestError is never a match",
			err:  errors.New("boom"),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isRuntimeTransitionInProgressError(tc.err)
			if got != tc.want {
				t.Fatalf("isRuntimeTransitionInProgressError(%v) = %v, want %v", tc.err, got, tc.want)
			}
			// Mutual exclusivity: whichever this body matches, it must
			// never also match the other classifier.
			if got && isAgentNotBoundToRuntimeError(tc.err) {
				t.Fatalf("body matched both classifiers: %v", tc.err)
			}
		})
	}
}

func TestIsRuntimeNotFoundError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "404 with runtime not found body from heartbeat",
			err: &requestError{
				Method:     http.MethodPost,
				Path:       "/api/daemon/heartbeat",
				StatusCode: http.StatusNotFound,
				Body:       `{"error":"runtime not found"}`,
			},
			want: true,
		},
		{
			name: "404 with runtime not found body from claim",
			err: &requestError{
				Method:     http.MethodPost,
				Path:       "/api/daemon/runtimes/abc/tasks/claim",
				StatusCode: http.StatusNotFound,
				Body:       `{"error":"runtime not found"}`,
			},
			want: true,
		},
		{
			name: "mixed-case body still matches",
			err: &requestError{
				StatusCode: http.StatusNotFound,
				Body:       `{"error":"Runtime Not Found"}`,
			},
			want: true,
		},
		{
			name: "500 with same body must NOT be treated as runtime-not-found",
			err: &requestError{
				StatusCode: http.StatusInternalServerError,
				Body:       `{"error":"runtime not found"}`,
			},
			want: false,
		},
		{
			name: "404 with task-not-found body is not runtime-not-found",
			err: &requestError{
				StatusCode: http.StatusNotFound,
				Body:       `{"error":"task not found"}`,
			},
			want: false,
		},
		{
			name: "404 with workspace-not-found body is not runtime-not-found",
			err: &requestError{
				StatusCode: http.StatusNotFound,
				Body:       `{"error":"workspace not found"}`,
			},
			want: false,
		},
		{
			name: "non-requestError",
			err:  errors.New("network down"),
			want: false,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isRuntimeNotFoundError(tc.err); got != tc.want {
				t.Fatalf("isRuntimeNotFoundError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestDrainInboxTaskAttachesLease(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemon/runtimes/rt-1/agent-inbox/drain" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"events": [{
				"id": "event-123",
				"delivery_id": "delivery-123",
				"lease_token": "lease-123",
				"lease_expires_at": "2026-07-10T00:00:00Z",
				"seq_to": 42,
				"requires_wake": true,
				"task": {
					"id": "event-123",
					"agent_id": "agent-123",
					"runtime_id": "rt-1",
					"workspace_id": "ws-1",
					"chat_session_id": "chat-123"
				}
			}]
		}`))
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{
		client: NewClient(srv.URL),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	task, err := d.drainInboxTask(context.Background(), "rt-1")
	if err != nil {
		t.Fatalf("drainInboxTask: %v", err)
	}
	if task == nil || task.InboxEvent == nil {
		t.Fatalf("drainInboxTask returned %#v, want inbox-backed task", task)
	}
	if task.ID != "event-123" || task.InboxEvent.DeliveryID != "delivery-123" || task.InboxEvent.SeqTo != 42 {
		t.Fatalf("task = %#v, want attached inbox lease", task)
	}
}

// Turn-fold: same-conversation batch from drain → one Task with folded leases
// and merged seq range (Alice boundary: one exchange = one turn).
func TestDrainInboxTask_FoldsSameConversationBatch(t *testing.T) {
	t.Parallel()

	var acked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/ack") {
			// non-runnable path should not fire in this test
			parts := strings.Split(r.URL.Path, "/")
			acked = append(acked, parts[len(parts)-2])
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		if r.URL.Path != "/api/daemon/runtimes/rt-1/agent-inbox/drain" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"events": [
				{
					"id": "event-a",
					"delivery_id": "delivery-a",
					"conversation_id": "conv-1",
					"lease_token": "lease-a",
					"lease_expires_at": "2026-08-09T00:00:00Z",
					"seq_from": 10,
					"seq_to": 12,
					"requires_wake": true,
					"task": {
						"id": "event-a",
						"agent_id": "agent-1",
						"runtime_id": "rt-1",
						"workspace_id": "ws-1"
					}
				},
				{
					"id": "event-b",
					"delivery_id": "delivery-b",
					"conversation_id": "conv-1",
					"lease_token": "lease-b",
					"lease_expires_at": "2026-08-09T00:00:00Z",
					"seq_from": 13,
					"seq_to": 15,
					"requires_wake": true,
					"task": {
						"id": "event-b",
						"agent_id": "agent-1",
						"runtime_id": "rt-1",
						"workspace_id": "ws-1"
					}
				}
			],
			"has_more": false
		}`))
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{
		client: NewClient(srv.URL),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	task, err := d.drainInboxTask(context.Background(), "rt-1")
	if err != nil {
		t.Fatalf("drainInboxTask: %v", err)
	}
	if task == nil || task.InboxEvent == nil {
		t.Fatalf("drainInboxTask returned %#v", task)
	}
	if task.ID != "event-a" {
		t.Fatalf("primary task id = %q, want event-a", task.ID)
	}
	if task.InboxEvent.ID != "event-a" || task.InboxEvent.DeliveryID != "delivery-a" {
		t.Fatalf("primary lease = %#v", task.InboxEvent)
	}
	// Merged range across the conversation batch.
	if task.InboxEvent.SeqFrom != 10 || task.InboxEvent.SeqTo != 15 {
		t.Fatalf("merged seq = %d..%d, want 10..15", task.InboxEvent.SeqFrom, task.InboxEvent.SeqTo)
	}
	if len(task.FoldedInboxEvents) != 1 {
		t.Fatalf("folded count = %d, want 1", len(task.FoldedInboxEvents))
	}
	if task.FoldedInboxEvents[0].ID != "event-b" || task.FoldedInboxEvents[0].DeliveryID != "delivery-b" {
		t.Fatalf("folded lease = %#v", task.FoldedInboxEvents[0])
	}
	// Folded event keeps its own SeqTo for per-event ack (>= SeqTo).
	if task.FoldedInboxEvents[0].SeqTo != 15 {
		t.Fatalf("folded SeqTo = %d, want 15", task.FoldedInboxEvents[0].SeqTo)
	}
	if len(acked) != 0 {
		t.Fatalf("unexpected acks during fold: %v", acked)
	}
	leases := task.inboxLeases()
	if len(leases) != 2 {
		t.Fatalf("inboxLeases() = %d, want 2", len(leases))
	}
}

func TestDrainInboxTask_AcksNonRunnableThenFoldsRunnable(t *testing.T) {
	t.Parallel()

	var acked atomic.Value
	acked.Store([]string{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/ack") {
			parts := strings.Split(r.URL.Path, "/")
			cur := acked.Load().([]string)
			acked.Store(append(append([]string{}, cur...), parts[len(parts)-2]))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		if r.URL.Path != "/api/daemon/runtimes/rt-1/agent-inbox/drain" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"events": [
				{
					"id": "observe-1",
					"delivery_id": "d-obs",
					"conversation_id": "conv-1",
					"lease_token": "l-obs",
					"lease_expires_at": "2026-08-09T00:00:00Z",
					"seq_from": 1,
					"seq_to": 1,
					"requires_wake": false
				},
				{
					"id": "event-run",
					"delivery_id": "d-run",
					"conversation_id": "conv-1",
					"lease_token": "l-run",
					"lease_expires_at": "2026-08-09T00:00:00Z",
					"seq_from": 2,
					"seq_to": 3,
					"requires_wake": true,
					"task": {
						"id": "event-run",
						"agent_id": "agent-1",
						"runtime_id": "rt-1",
						"workspace_id": "ws-1"
					}
				}
			]
		}`))
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{
		client: NewClient(srv.URL),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	task, err := d.drainInboxTask(context.Background(), "rt-1")
	if err != nil {
		t.Fatalf("drainInboxTask: %v", err)
	}
	if task == nil || task.InboxEvent == nil || task.ID != "event-run" {
		t.Fatalf("task = %#v", task)
	}
	gotAcked := acked.Load().([]string)
	if len(gotAcked) != 1 || gotAcked[0] != "observe-1" {
		t.Fatalf("acked = %v, want [observe-1]", gotAcked)
	}
	if len(task.FoldedInboxEvents) != 0 {
		t.Fatalf("folded = %#v, want none (only one runnable)", task.FoldedInboxEvents)
	}
}

func TestHandleTask_InboxCompleteUsesInboxEndpoint(t *testing.T) {
	t.Parallel()

	var completeSeen atomic.Bool
	var renewSeen atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// task #60: inbox tasks now poll GetTaskStatus too (the
		// !isInboxTask() guard that used to skip this for every real task
		// was dead code left over from #1164 — see daemon.go). Serve
		// "running" so shouldInterruptAgent stays false and the rest of
		// this happy-path test proceeds unaffected.
		if strings.HasSuffix(r.URL.Path, "/status") && strings.Contains(r.URL.Path, "/api/daemon/tasks/") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"running"}`))
			return
		}
		if r.URL.Path == "/api/daemon/agent-inbox/events/event-123/renew" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode renew body: %v", err)
			}
			if body["delivery_id"] != "delivery-123" || body["lease_token"] != "lease-123" {
				t.Fatalf("renew body = %#v, want lease", body)
			}
			renewSeen.Store(true)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		if r.URL.Path == "/api/daemon/agent-inbox/events/event-123/execution" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/api/daemon/agent-inbox/events/event-123/complete" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode complete body: %v", err)
		}
		if body["delivery_id"] != "delivery-123" || body["lease_token"] != "lease-123" || body["output"] != "inbox reply" {
			t.Fatalf("complete body = %#v, want lease + output", body)
		}
		completeSeen.Store(true)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{
		client:             NewClient(srv.URL),
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:         make(map[string]*workspaceState),
		runtimeIndex:       map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		cancelPollInterval: 10 * time.Millisecond,
	}
	d.runner = taskRunnerFunc(func(_ context.Context, _ Task, _ string, _ int, _ *slog.Logger) (TaskResult, error) {
		return TaskResult{Status: "completed", Comment: "inbox reply"}, nil
	})
	d.handleTask(context.Background(), Task{
		ID:            "event-123",
		AgentID:       "agent-123",
		RuntimeID:     "rt-1",
		WorkspaceID:   "ws-1",
		ChatSessionID: "chat-123",
		Agent:         &AgentData{Name: "inbox-agent"},
		InboxEvent: &AgentInboxLease{
			ID:         "event-123",
			DeliveryID: "delivery-123",
			LeaseToken: "lease-123",
			SeqTo:      42,
		},
	}, 0)
	if !completeSeen.Load() {
		t.Fatal("inbox complete endpoint was not called")
	}
	if !renewSeen.Load() {
		t.Fatal("inbox renew endpoint was not called")
	}
}

// A delivery lease can be renewed without beginning a new provider run. The
// daemon must mint one run UUID before invoking the provider, then use that
// same UUID for every usage-report retry from this run.
func TestHandleTask_InboxUsageStartsExecutionBeforeProvider(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var executionID string
	startSeen := false
	usageCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/api/daemon/agent-inbox/events/event-usage/renew":
			w.WriteHeader(http.StatusOK)
		case "/api/daemon/agent-inbox/events/event-usage/execution":
			mu.Lock()
			startSeen = true
			executionID, _ = body["execution_id"].(string)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case "/api/daemon/agent-inbox/events/event-usage/usage":
			mu.Lock()
			got, _ := body["execution_id"].(string)
			if !startSeen || executionID == "" || got != executionID {
				t.Errorf("usage execution_id=%q, started=%v start_id=%q", got, startSeen, executionID)
			}
			usageCalls++
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case "/api/daemon/agent-inbox/events/event-usage/complete":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"acked_seq":1,"terminal_outcome":"completed","resume_unsafe":false}`))
		case "/api/daemon/agent-memory-writes":
			// Memory write telemetry is reported after a successful inbox task.
			w.WriteHeader(http.StatusOK)
		case "/api/daemon/tasks/event-usage/status":
			// task #60: the final pre-completion check now runs for inbox
			// tasks too (see daemon.go) — serve "running" so it doesn't
			// interrupt this happy path.
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"running"}`))
		default:
			t.Fatalf("unexpected inbox path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{
		client:             NewClient(srv.URL),
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:         make(map[string]*workspaceState),
		runtimeIndex:       map[string]Runtime{"rt-usage": {ID: "rt-usage", Provider: "claude"}},
		cancelPollInterval: time.Hour,
	}
	d.runner = taskRunnerFunc(func(_ context.Context, _ Task, _ string, _ int, _ *slog.Logger) (TaskResult, error) {
		mu.Lock()
		started := startSeen
		mu.Unlock()
		if !started {
			t.Fatal("provider runner started before inbox execution was persisted")
		}
		return TaskResult{Status: "completed", Usage: []TaskUsageEntry{{Provider: "anthropic", Model: "claude-test", InputTokens: 7}}}, nil
	})
	d.handleTask(context.Background(), Task{
		ID: "event-usage", AgentID: "agent-usage", RuntimeID: "rt-usage", WorkspaceID: "ws-usage", ChatSessionID: "chat-usage",
		Agent:      &AgentData{Name: "inbox-agent"},
		InboxEvent: &AgentInboxLease{ID: "event-usage", DeliveryID: "delivery-usage", LeaseToken: "lease-usage"},
	}, 0)

	mu.Lock()
	defer mu.Unlock()
	if executionID == "" || usageCalls != 1 {
		t.Fatalf("execution_id=%q usage_calls=%d, want one persisted execution and one usage report", executionID, usageCalls)
	}
}

func TestHandleTask_InboxFailureUsesInboxEndpointWithClassifier(t *testing.T) {
	t.Parallel()

	var failSeen atomic.Bool
	var renewSeen atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// task #60: inbox tasks now poll GetTaskStatus too — see the
		// identical comment in TestHandleTask_InboxCompleteUsesInboxEndpoint.
		if strings.HasSuffix(r.URL.Path, "/status") && strings.Contains(r.URL.Path, "/api/daemon/tasks/") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"running"}`))
			return
		}
		if r.URL.Path == "/api/daemon/agent-inbox/events/event-123/renew" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode renew body: %v", err)
			}
			if body["delivery_id"] != "delivery-123" || body["lease_token"] != "lease-123" {
				t.Fatalf("renew body = %#v, want lease", body)
			}
			renewSeen.Store(true)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		if r.URL.Path == "/api/daemon/agent-inbox/events/event-123/execution" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/api/daemon/agent-inbox/events/event-123/fail" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode fail body: %v", err)
		}
		want := map[string]any{
			"delivery_id":    "delivery-123",
			"lease_token":    "lease-123",
			"error":          "grok first stream event timeout",
			"session_id":     "sess-1",
			"work_dir":       "/tmp/work",
			"failure_reason": "grok_first_turn_no_progress",
		}
		for k, v := range want {
			if body[k] != v {
				t.Fatalf("fail body[%s] = %#v, want %#v (body=%#v)", k, body[k], v, body)
			}
		}
		failSeen.Store(true)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{
		client:             NewClient(srv.URL),
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:         make(map[string]*workspaceState),
		runtimeIndex:       map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "grok"}},
		cancelPollInterval: 10 * time.Millisecond,
	}
	d.runner = taskRunnerFunc(func(_ context.Context, _ Task, _ string, _ int, _ *slog.Logger) (TaskResult, error) {
		return TaskResult{
			Status:        "blocked",
			Comment:       "grok first stream event timeout",
			SessionID:     "sess-1",
			WorkDir:       "/tmp/work",
			FailureReason: "grok_first_turn_no_progress",
		}, nil
	})
	d.handleTask(context.Background(), Task{
		ID:            "event-123",
		AgentID:       "agent-123",
		RuntimeID:     "rt-1",
		WorkspaceID:   "ws-1",
		ChatSessionID: "chat-123",
		Agent:         &AgentData{Name: "inbox-agent"},
		InboxEvent: &AgentInboxLease{
			ID:         "event-123",
			DeliveryID: "delivery-123",
			LeaseToken: "lease-123",
			SeqTo:      42,
		},
	}, 0)
	if !failSeen.Load() {
		t.Fatal("inbox fail endpoint was not called")
	}
	if !renewSeen.Load() {
		t.Fatal("inbox renew endpoint was not called")
	}
}

func TestAcquireAgentWakeSlotSerializesSameLaneAndAllowsDifferentLanes(t *testing.T) {
	t.Parallel()

	d := &Daemon{}
	firstRelease, err := d.acquireAgentWakeSlot(context.Background(), "agent-a:conversation")
	if err != nil {
		t.Fatalf("acquire first agent-a slot: %v", err)
	}

	secondAcquired := make(chan func(), 1)
	go func() {
		release, acquireErr := d.acquireAgentWakeSlot(context.Background(), "agent-a:conversation")
		if acquireErr == nil {
			secondAcquired <- release
		}
	}()
	select {
	case release := <-secondAcquired:
		release()
		t.Fatal("second execution for the same agent acquired before the first released")
	case <-time.After(30 * time.Millisecond):
	}

	differentRelease, err := d.acquireAgentWakeSlot(context.Background(), "agent-b:conversation")
	if err != nil {
		t.Fatalf("different agent should acquire concurrently: %v", err)
	}
	differentRelease()

	firstRelease()
	select {
	case release := <-secondAcquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("second same-agent execution did not acquire after release")
	}
}

func TestTaskWakeSerializationKeyAllowsSameAgentDifferentIssues(t *testing.T) {
	t.Parallel()

	a := Task{AgentID: "agent-a", IssueID: "issue-a"}
	b := Task{AgentID: "agent-a", IssueID: "issue-b"}
	if taskWakeSerializationKey(a) == taskWakeSerializationKey(b) {
		t.Fatal("different Issues for one Agent must use independent execution lanes")
	}
	if got := taskWakeSerializationKey(Task{AgentID: "agent-a", IssueID: "issue-a"}); got != taskWakeSerializationKey(a) {
		t.Fatal("follow-ups on the same Issue must remain serialized")
	}
	chat := taskWakeSerializationKey(Task{AgentID: "agent-a", ChatSessionID: "chat-a"})
	channel := taskWakeSerializationKey(Task{AgentID: "agent-a", ChannelID: "channel-a"})
	if chat != channel {
		t.Fatal("message wakes for one Agent must share the conversational lane")
	}
}

func TestTaskExecutionRootIsolatesIssuesAndReusesIssueLane(t *testing.T) {
	root := filepath.Join(t.TempDir(), "agent")
	a := Task{IssueID: "11111111-1111-1111-1111-111111111111"}
	b := Task{IssueID: "22222222-2222-2222-2222-222222222222"}
	if taskExecutionRoot(root, a) == taskExecutionRoot(root, b) {
		t.Fatal("different issues must not share an execution root")
	}
	if taskExecutionRoot(root, a) != taskExecutionRoot(root, a) {
		t.Fatal("the same issue must reuse its execution root")
	}
	if got := taskExecutionRoot(root, Task{}); got != root {
		t.Fatalf("non-issue task root = %q, want durable root %q", got, root)
	}
}

func TestHandleTask_InboxLeaseRejectionStopsBeforeRunner(t *testing.T) {
	t.Parallel()

	var terminalSeen atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/renew") {
			http.Error(w, `{"error":"delivery lease is no longer active"}`, http.StatusConflict)
			return
		}
		terminalSeen.Store(true)
		http.Error(w, `{"error":"unexpected terminal callback"}`, http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	var runnerCalled atomic.Bool
	d := &Daemon{
		client:             NewClient(srv.URL),
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:         make(map[string]*workspaceState),
		runtimeIndex:       map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		cancelPollInterval: 10 * time.Millisecond,
	}
	d.runner = taskRunnerFunc(func(context.Context, Task, string, int, *slog.Logger) (TaskResult, error) {
		runnerCalled.Store(true)
		return TaskResult{Status: "completed", Comment: "must not publish"}, nil
	})

	d.handleTask(context.Background(), Task{
		ID:            "event-stale",
		AgentID:       "agent-123",
		RuntimeID:     "rt-1",
		WorkspaceID:   "ws-1",
		ChatSessionID: "chat-123",
		Agent:         &AgentData{ID: "agent-123", Name: "inbox-agent"},
		InboxEvent: &AgentInboxLease{
			ID:         "event-stale",
			DeliveryID: "delivery-stale",
			LeaseToken: "lease-stale",
			SeqTo:      42,
		},
	}, 0)

	if runnerCalled.Load() {
		t.Fatal("runner started after the inbox lease was permanently rejected")
	}
	if terminalSeen.Load() {
		t.Fatal("stale inbox execution attempted a terminal callback")
	}
}

func TestHandleTask_InboxLeaseLossAfterRunnerDoesNotCancelTerminalReport(t *testing.T) {
	t.Parallel()

	var renewCount atomic.Int32
	secondRenew := make(chan struct{})
	var secondRenewOnce sync.Once
	var completeSeen atomic.Bool
	var failSeen atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/renew"):
			if renewCount.Add(1) == 1 {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"ok":true}`))
				return
			}
			secondRenewOnce.Do(func() { close(secondRenew) })
			http.Error(w, `{"error":"delivery lease is no longer active"}`, http.StatusConflict)
		case strings.HasSuffix(r.URL.Path, "/execution"):
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/complete"):
			select {
			case <-secondRenew:
			case <-time.After(time.Second):
				t.Error("second renew did not race with terminal report")
			}
			completeSeen.Store(true)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case strings.HasSuffix(r.URL.Path, "/fail"):
			failSeen.Store(true)
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/status"):
			// task #60: inbox tasks now poll GetTaskStatus too — serve
			// "running" so it doesn't race-interrupt this terminal-report test.
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"running"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{
		client:             NewClient(srv.URL),
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:         make(map[string]*workspaceState),
		runtimeIndex:       map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		cancelPollInterval: 10 * time.Millisecond,
	}
	d.runner = taskRunnerFunc(func(context.Context, Task, string, int, *slog.Logger) (TaskResult, error) {
		return TaskResult{Status: "completed", Comment: "reply"}, nil
	})
	d.handleTask(context.Background(), Task{
		ID:            "event-terminal-race",
		AgentID:       "agent-123",
		RuntimeID:     "rt-1",
		WorkspaceID:   "ws-1",
		ChatSessionID: "chat-123",
		Agent:         &AgentData{ID: "agent-123", Name: "inbox-agent"},
		InboxEvent: &AgentInboxLease{
			ID:         "event-terminal-race",
			DeliveryID: "delivery-terminal-race",
			LeaseToken: "lease-terminal-race",
			SeqTo:      42,
		},
	}, 0)

	if !completeSeen.Load() {
		t.Fatal("terminal report was cancelled by a racing lease rejection")
	}
	if failSeen.Load() {
		t.Fatal("successful terminal report fell back to fail")
	}
}

func TestHandleTask_InboxLeaseLossCancelsRunningExecutor(t *testing.T) {
	t.Parallel()

	var renewCount atomic.Int32
	var terminalSeen atomic.Bool
	var usageSeen atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/renew") {
			if renewCount.Add(1) == 1 {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"ok":true}`))
				return
			}
			http.Error(w, `{"error":"delivery lease is no longer active"}`, http.StatusConflict)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/execution") {
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/usage") {
			usageSeen.Store(true)
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/status") {
			// task #60: inbox tasks now poll GetTaskStatus too — this isn't
			// the terminal callback this test is watching for, so don't let
			// it trip the terminalSeen assertion below.
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"running"}`))
			return
		}
		terminalSeen.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	var runnerCancelled atomic.Bool
	d := &Daemon{
		client:             NewClient(srv.URL),
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:         make(map[string]*workspaceState),
		runtimeIndex:       map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		cancelPollInterval: 10 * time.Millisecond,
	}
	d.runner = taskRunnerFunc(func(ctx context.Context, _ Task, _ string, _ int, _ *slog.Logger) (TaskResult, error) {
		select {
		case <-ctx.Done():
			runnerCancelled.Store(true)
			return TaskResult{Usage: []TaskUsageEntry{{Provider: "anthropic", Model: "claude-test", InputTokens: 11}}}, ctx.Err()
		case <-time.After(time.Second):
			return TaskResult{}, errors.New("runner was not cancelled after lease loss")
		}
	})
	d.handleTask(context.Background(), Task{
		ID:            "event-running-stale",
		AgentID:       "agent-123",
		RuntimeID:     "rt-1",
		WorkspaceID:   "ws-1",
		ChatSessionID: "chat-123",
		Agent:         &AgentData{ID: "agent-123", Name: "inbox-agent"},
		InboxEvent: &AgentInboxLease{
			ID:         "event-running-stale",
			DeliveryID: "delivery-running-stale",
			LeaseToken: "lease-running-stale",
			SeqTo:      42,
		},
	}, 0)

	if !runnerCancelled.Load() {
		t.Fatal("running executor was not cancelled after lease loss")
	}
	if terminalSeen.Load() {
		t.Fatal("stale running executor attempted a terminal callback")
	}
	if !usageSeen.Load() {
		t.Fatal("lease-lost execution did not report its persisted usage")
	}
}

func TestShouldInterruptAgent(t *testing.T) {
	t.Parallel()

	notFound := &requestError{
		StatusCode: http.StatusNotFound,
		Body:       `{"error":"task not found"}`,
	}
	transient := &requestError{
		StatusCode: http.StatusBadGateway,
		Body:       `<html>...</html>`,
	}

	cases := []struct {
		name   string
		status string
		err    error
		want   bool
	}{
		{name: "status cancelled", status: "cancelled", err: nil, want: true},
		{name: "status failed (offline sweeper)", status: "failed", err: nil, want: true},
		{name: "status completed (finished elsewhere)", status: "completed", err: nil, want: true},
		{name: "task deleted (404)", status: "", err: notFound, want: true},
		{name: "running normally", status: "running", err: nil, want: false},
		{name: "dispatched keeps running", status: "dispatched", err: nil, want: false},
		{name: "transient 5xx is not a cancel signal", status: "", err: transient, want: false},
		{name: "no information yet", status: "", err: nil, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldInterruptAgent(tc.status, tc.err); got != tc.want {
				t.Fatalf("shouldInterruptAgent(%q, %v) = %v, want %v", tc.status, tc.err, got, tc.want)
			}
		})
	}
}

// TestWatchTaskCancellation_TaskDeleted reproduces the zombie-task bug:
// when the server deletes a task while it is running (issue removed,
// agent reassigned, etc.), GetTaskStatus starts returning 404. Before the
// fix the daemon kept polling and never interrupted the running agent —
// codex would keep emitting tool calls for minutes against a dead task.
//
// After the fix, watchTaskCancellation must close its channel within a
// few poll intervals so the caller can cancel the agent context.
func TestWatchTaskCancellation_TaskDeleted(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/status") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"task not found"}`))
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{client: NewClient(srv.URL), logger: slog.Default()}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cancelled := d.watchTaskCancellation(ctx, "task-deleted", "rt-1", 10*time.Millisecond, slog.Default())

	select {
	case <-cancelled:
		// Expected: the watcher detected the 404 and signalled cancellation.
	case <-time.After(2 * time.Second):
		t.Fatal("watchTaskCancellation did not signal cancellation when task was deleted (404)")
	}
}

// TestWatchTaskCancellation_StatusCancelled keeps the existing behaviour
// (server transitions task status to "cancelled") working alongside the
// new 404 path.
func TestWatchTaskCancellation_StatusCancelled(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/status") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"cancelled"}`))
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{client: NewClient(srv.URL), logger: slog.Default()}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cancelled := d.watchTaskCancellation(ctx, "task-cancelled", "rt-1", 10*time.Millisecond, slog.Default())

	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("watchTaskCancellation did not signal cancellation when status=cancelled")
	}
}

// TestWatchTaskCancellation_RunningTaskNotInterrupted ensures the watcher
// does NOT trigger on transient errors or while the task is still running.
func TestWatchTaskCancellation_RunningTaskNotInterrupted(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"running"}`))
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{client: NewClient(srv.URL), logger: slog.Default()}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cancelled := d.watchTaskCancellation(ctx, "task-running", "rt-1", 10*time.Millisecond, slog.Default())

	select {
	case <-cancelled:
		t.Fatal("watchTaskCancellation should not signal cancellation while task is running")
	case <-time.After(150 * time.Millisecond):
	}
	if calls.Load() < 5 {
		t.Fatalf("expected the watcher to poll at least 5 times in 150ms, got %d", calls.Load())
	}
}

// TestHandleTask_CancellingAStuckInboxTaskForceKillsIt is the end-to-end
// regression guard for task #60. Before the fix, `if !task.isInboxTask()`
// (dead code left over from #1164's cutover to all-inbox dispatch, 2026-07-24)
// silently skipped watchTaskCancellation for every real task, so a human
// clicking "cancel" on a stuck issue task only flipped a DB column
// (CancelAgentTask: status='suppressed') — nothing ever told the daemon to
// stop the hung one-shot backend. This test goes through handleTask (not
// watchTaskCancellation directly) with an inbox-backed Task and a runner
// that blocks until its ctx is cancelled, simulating a genuinely stuck
// agent process. If this test is red, task #60's fix has regressed.
func TestHandleTask_CancellingAStuckInboxTaskForceKillsIt(t *testing.T) {
	t.Parallel()

	var statusCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/status"):
			statusCalls.Add(1)
			// Simulate CancelAgentTask's SQL: status='suppressed', which
			// GetTaskStatus's server handler maps to "cancelled".
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"cancelled"}`))
		case strings.HasSuffix(r.URL.Path, "/renew"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case strings.HasSuffix(r.URL.Path, "/execution"):
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/fail"):
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	var runnerObservedCancellation atomic.Bool
	d := &Daemon{
		client:             NewClient(srv.URL),
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:         make(map[string]*workspaceState),
		runtimeIndex:       map[string]Runtime{"rt-stuck": {ID: "rt-stuck", Provider: "claude"}},
		cancelPollInterval: 10 * time.Millisecond,
	}
	d.runner = taskRunnerFunc(func(ctx context.Context, _ Task, _ string, _ int, _ *slog.Logger) (TaskResult, error) {
		// A genuinely stuck agent: never returns on its own, only reacts
		// to the ctx being cancelled — exactly what exec.CommandContext's
		// default Cancel does to the underlying OS process for every
		// one-shot backend (cursor/grok/pi/claude), so cancelling this ctx
		// is equivalent to force-killing the real subprocess.
		select {
		case <-ctx.Done():
			runnerObservedCancellation.Store(true)
			return TaskResult{}, ctx.Err()
		case <-time.After(5 * time.Second):
			return TaskResult{}, errors.New("runner was never interrupted by task cancellation")
		}
	})

	task := canonicalInboxTaskForTest(Task{
		ID:          "event-stuck",
		AgentID:     "agent-stuck",
		RuntimeID:   "rt-stuck",
		WorkspaceID: "ws-stuck",
		IssueID:     "issue-stuck",
		Agent:       &AgentData{Name: "stuck-agent"},
	})

	done := make(chan struct{})
	go func() {
		d.handleTask(context.Background(), task, 0)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleTask did not return — cancelling a stuck inbox task should force-kill it within one poll interval, not hang for the runner's own timeout")
	}

	if !runnerObservedCancellation.Load() {
		t.Fatal("runner never observed ctx cancellation — cancelling a stuck inbox task did not reach the running backend")
	}
	if statusCalls.Load() == 0 {
		t.Fatal("GetTaskStatus was never polled — the cancellation watch did not run for this inbox task")
	}
}

func TestResolvedTaskAgentIDPrefersNestedAgent(t *testing.T) {
	t.Parallel()

	task := Task{
		WorkspaceID: "workspace-1",
		Agent:       &AgentData{ID: "agent-nested", Name: "Pi Agent"},
	}
	agentID := resolvedTaskAgentID(task)
	if agentID != "agent-nested" {
		t.Fatalf("resolvedTaskAgentID = %q, want nested agent id", agentID)
	}
}

func TestResolvedTaskAgentIDFallsBackToTopLevelAgent(t *testing.T) {
	t.Parallel()

	task := Task{WorkspaceID: "workspace-1", AgentID: "agent-top"}
	agentID := resolvedTaskAgentID(task)
	if agentID != "agent-top" {
		t.Fatalf("resolvedTaskAgentID = %q, want top-level agent id", agentID)
	}
}

func TestPiAgentEnvDisablesExpensiveAutomaticMemoryWork(t *testing.T) {
	t.Parallel()

	env := map[string]string{}
	addPiMemoryFastModeEnv(env)

	want := map[string]string{
		"PI_MEMORY_FINALIZE":                     "off",
		"PI_MEMORY_BACKGROUND_SHUTDOWN":          "off",
		"PI_MEMORY_LEARNING":                     "off",
		"PI_MEMORY_SKILL_DRAFTS":                 "off",
		"PI_MEMORY_QMD_UPDATE":                   "off",
		"PI_MEMORY_AUTO_SYNC":                    "0",
		"PI_MEMORY_AUTO_SYNC_PULL":               "0",
		"PI_MEMORY_AUTO_SYNC_PULL_ON_START":      "0",
		"PI_MEMORY_AUTO_SYNC_UPLOAD":             "0",
		"PI_MEMORY_AUTO_SYNC_UPLOAD_ON_SHUTDOWN": "0",
		"PI_MEMORY_NO_SEARCH":                    "1",
		"PI_MEMORY_REVIEW_STARTUP_HINT":          "0",
	}
	for key, value := range want {
		if got := env[key]; got != value {
			t.Fatalf("%s = %q, want %q", key, got, value)
		}
	}
}

func TestPiAgentEnvPreservesCustomMemorySettings(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"PI_MEMORY_FINALIZE": "on",
		"PI_MEMORY_LEARNING": "review",
	}
	addPiMemoryFastModeEnv(env)

	if got := env["PI_MEMORY_FINALIZE"]; got != "on" {
		t.Fatalf("PI_MEMORY_FINALIZE = %q, want custom value %q", got, "on")
	}
	if got := env["PI_MEMORY_LEARNING"]; got != "review" {
		t.Fatalf("PI_MEMORY_LEARNING = %q, want custom value %q", got, "review")
	}
	if got := env["PI_MEMORY_QMD_UPDATE"]; got != "off" {
		t.Fatalf("PI_MEMORY_QMD_UPDATE = %q, want default %q", got, "off")
	}
}

func TestMulticaAgentEnvUsesProviderNeutralRoot(t *testing.T) {
	t.Parallel()

	workspaceRoot := filepath.Join(t.TempDir(), "multica_workspaces")
	env := map[string]string{}
	addMulticaAgentEnv(env, Config{WorkspacesRoot: workspaceRoot, DaemonID: "daemon-1"}, "workspace-1", "agent-1")

	agentRoot := agentworkspace.Root(workspaceRoot, "workspace-1", "agent-1")
	want := map[string]string{
		"MULTICA_AGENT_ROOT": agentRoot,
	}
	for key, value := range want {
		if got := env[key]; got != value {
			t.Fatalf("%s = %q, want %q", key, got, value)
		}
	}
}

func TestBlockedEnvKeyProtectsProviderNeutralAgentRoot(t *testing.T) {
	t.Parallel()

	if !isBlockedEnvKey("MULTICA_AGENT_ROOT") {
		t.Fatal("MULTICA_AGENT_ROOT should be blocked from custom_env")
	}
	for _, key := range []string{"PI_MEMORY_FINALIZE", "PI_MEMORY_LEARNING", "PI_MEMORY_QMD_UPDATE", "PI_MEMORY_AUTO_SYNC"} {
		if isBlockedEnvKey(key) {
			t.Fatalf("%s should remain configurable via custom_env", key)
		}
	}
}

func TestInjectScopedSecretsFiltersByChannelAndProject(t *testing.T) {
	t.Parallel()

	env := map[string]string{}
	task := Task{
		ChannelID: "chan-a",
		ProjectID: "proj-a",
		Agent: &AgentData{CustomEnv: map[string]string{
			"AGENT_KEY":          "agent",
			"MULTICA_TASK_ID":    "blocked",
			"MULTICA_AGENT_ROOT": "blocked",
		}},
		ScopedSecrets: []ScopedSecret{
			{Key: "CHANNEL_A", Value: "a", Scope: "channel", ChannelID: "chan-a"},
			{Key: "CHANNEL_B", Value: "b", Scope: "channel", ChannelID: "chan-b"},
			{Key: "PROJ_A", Value: "pa", Scope: "project", ProjectID: "proj-a"},
			{Key: "PROJ_B", Value: "pb", Scope: "project", ProjectID: "proj-b"},
		},
	}
	injectScopedSecrets(env, task, slog.Default())
	if env["AGENT_KEY"] != "agent" || env["CHANNEL_A"] != "a" || env["PROJ_A"] != "pa" {
		t.Fatalf("expected allowed secrets, got %#v", env)
	}
	for _, key := range []string{"CHANNEL_B", "PROJ_B", "MULTICA_TASK_ID", "MULTICA_AGENT_ROOT"} {
		if _, ok := env[key]; ok {
			t.Fatalf("leaked or blocked key %q present: %#v", key, env)
		}
	}
}

func TestMergeUsage(t *testing.T) {
	t.Parallel()

	a := map[string]agent.TokenUsage{
		"model-a": {InputTokens: 10, OutputTokens: 5},
	}
	b := map[string]agent.TokenUsage{
		"model-a": {InputTokens: 20, OutputTokens: 10, CacheReadTokens: 3},
		"model-b": {InputTokens: 100},
	}
	merged := mergeUsage(a, b)

	if got := merged["model-a"]; got.InputTokens != 30 || got.OutputTokens != 15 || got.CacheReadTokens != 3 {
		t.Fatalf("model-a: expected {30,15,3,0}, got %+v", got)
	}
	if got := merged["model-b"]; got.InputTokens != 100 {
		t.Fatalf("model-b: expected InputTokens=100, got %+v", got)
	}

	if got := mergeUsage(nil, b); len(got) != 2 {
		t.Fatal("mergeUsage(nil, b) should return b")
	}
	if got := mergeUsage(a, nil); len(got) != 1 {
		t.Fatal("mergeUsage(a, nil) should return a")
	}
}

func TestRuntimeStatsFromUsage(t *testing.T) {
	t.Parallel()

	if got := runtimeStatsFromUsage("cursor", nil); got != nil {
		t.Fatalf("nil usage → nil stats, got %+v", got)
	}
	if got := runtimeStatsFromUsage("cursor", map[string]agent.TokenUsage{
		"empty": {},
	}); got != nil {
		t.Fatalf("zero usage → nil stats, got %+v", got)
	}

	got := runtimeStatsFromUsage("cursor", map[string]agent.TokenUsage{
		"gpt-5": {InputTokens: 100, OutputTokens: 40, CacheReadTokens: 10},
	})
	if got == nil {
		t.Fatal("expected stats")
	}
	if got.Provider != "cursor" || got.Model != "gpt-5" {
		t.Fatalf("provider/model = %s/%s", got.Provider, got.Model)
	}
	if got.InputTokens != 100 || got.OutputTokens != 40 || got.CacheReadTokens != 10 || got.TotalTokens != 150 {
		t.Fatalf("tokens = %+v", got)
	}

	// Multi-model: aggregate counts, pick model with highest total.
	multi := runtimeStatsFromUsage("cursor", map[string]agent.TokenUsage{
		"small": {InputTokens: 1, OutputTokens: 1},
		"big":   {InputTokens: 50, OutputTokens: 50},
	})
	if multi == nil || multi.Model != "big" || multi.TotalTokens != 102 {
		t.Fatalf("multi = %+v", multi)
	}
}

// fakeBackend is a test double for agent.Backend that returns preconfigured
// results. Each call to Execute pops the next entry from the results slice.
type fakeBackend struct {
	calls   []agent.ExecOptions
	results []agent.Result
	errors  []error
	idx     atomic.Int32
}

func (b *fakeBackend) Execute(_ context.Context, _ string, opts agent.ExecOptions) (*agent.Session, error) {
	i := int(b.idx.Add(1)) - 1
	b.calls = append(b.calls, opts)
	if i < len(b.errors) && b.errors[i] != nil {
		return nil, b.errors[i]
	}
	msgCh := make(chan agent.Message)
	resCh := make(chan agent.Result, 1)
	close(msgCh)
	resCh <- b.results[i]
	return &agent.Session{Messages: msgCh, Result: resCh}, nil
}

func newTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return &Daemon{
		client: NewClient(srv.URL),
		logger: slog.Default(),
	}
}

func TestGateResumeToReusedWorkdir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		sessionID   string
		priorDir    string
		envDir      string
		wantSession string
		wantReused  bool
	}{
		{
			name:        "same workdir keeps session",
			sessionID:   "sess-1",
			priorDir:    "/ws/task-a/workdir",
			envDir:      "/ws/task-a/workdir",
			wantSession: "sess-1",
			wantReused:  true,
		},
		{
			name:        "fresh workdir drops session",
			sessionID:   "sess-1",
			priorDir:    "/ws/task-a/workdir",
			envDir:      "/ws/task-b/workdir",
			wantSession: "",
			wantReused:  false,
		},
		{
			name:        "session without recorded workdir drops session",
			sessionID:   "sess-1",
			priorDir:    "",
			envDir:      "/ws/task-b/workdir",
			wantSession: "",
			wantReused:  false,
		},
		{
			name:        "no prior session is a no-op",
			sessionID:   "",
			priorDir:    "/ws/task-a/workdir",
			envDir:      "/ws/task-b/workdir",
			wantSession: "",
			wantReused:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := Task{PriorSessionID: tt.sessionID, PriorWorkDir: tt.priorDir}
			taskCtx := execenv.TaskContextForEnv{PriorSessionResumed: tt.sessionID != ""}

			reused := gateResumeToReusedWorkdir(&task, &taskCtx, tt.envDir, slog.Default())

			if reused != tt.wantReused {
				t.Fatalf("reused = %v, want %v", reused, tt.wantReused)
			}
			if task.PriorSessionID != tt.wantSession {
				t.Fatalf("PriorSessionID = %q, want %q", task.PriorSessionID, tt.wantSession)
			}
			if taskCtx.PriorSessionResumed != (tt.wantSession != "") {
				t.Fatalf("PriorSessionResumed = %v, want %v", taskCtx.PriorSessionResumed, tt.wantSession != "")
			}
		})
	}
}

func TestApplyForceFreshSessionDropsPrior(t *testing.T) {
	t.Parallel()
	task := Task{PriorSessionID: "poisoned-pi", ForceFreshSession: true}
	applyForceFreshSession(&task, slog.Default())
	if task.PriorSessionID != "" {
		t.Fatalf("PriorSessionID = %q, want empty", task.PriorSessionID)
	}
}

func TestApplyForceFreshSessionNoopWhenUnset(t *testing.T) {
	t.Parallel()
	task := Task{PriorSessionID: "keep-me"}
	applyForceFreshSession(&task, slog.Default())
	if task.PriorSessionID != "keep-me" {
		t.Fatalf("PriorSessionID = %q, want keep-me", task.PriorSessionID)
	}
}

func TestExecuteAndDrain_ResumeFailureFallback(t *testing.T) {
	t.Parallel()

	d := newTestDaemon(t)
	ctx := context.Background()
	taskLog := slog.Default()

	fb := &fakeBackend{
		results: []agent.Result{
			{Status: "failed", Error: "session not found", Usage: map[string]agent.TokenUsage{
				"m1": {InputTokens: 5},
			}},
			{Status: "completed", Output: "done", SessionID: "new-sess", Usage: map[string]agent.TokenUsage{
				"m1": {InputTokens: 10, OutputTokens: 20},
			}},
		},
	}

	// First attempt: resume fails (no SessionID in result).
	opts := agent.ExecOptions{ResumeSessionID: "stale-id"}
	result, _, err := d.executeAndDrain(ctx, fb, "prompt", opts, taskLog, "task-1")
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}
	if result.Status != "failed" || result.SessionID != "" {
		t.Fatalf("expected failed result with empty SessionID, got %+v", result)
	}

	// Simulate the retry logic from runTask.
	if result.Status == "failed" && result.SessionID == "" {
		firstUsage := result.Usage
		opts.ResumeSessionID = ""
		retryResult, _, retryErr := d.executeAndDrain(ctx, fb, "prompt", opts, taskLog, "task-1")
		if retryErr != nil {
			t.Fatalf("retry error: %v", retryErr)
		}
		result = retryResult
		result.Usage = mergeUsage(firstUsage, result.Usage)
	}

	if result.Status != "completed" || result.Output != "done" {
		t.Fatalf("expected completed result, got %+v", result)
	}
	if result.SessionID != "new-sess" {
		t.Fatalf("expected new-sess, got %s", result.SessionID)
	}
	// Usage should be merged.
	if u := result.Usage["m1"]; u.InputTokens != 15 || u.OutputTokens != 20 {
		t.Fatalf("expected merged usage {15,20}, got %+v", u)
	}
	// Second call should NOT have ResumeSessionID.
	if fb.calls[1].ResumeSessionID != "" {
		t.Fatal("retry should not have ResumeSessionID")
	}
}

func TestExecuteAndDrain_NoRetryWhenSessionEstablished(t *testing.T) {
	t.Parallel()

	d := newTestDaemon(t)

	fb := &fakeBackend{
		results: []agent.Result{
			{Status: "failed", Error: "model error", SessionID: "valid-sess"},
		},
	}

	opts := agent.ExecOptions{ResumeSessionID: "some-id"}
	result, _, err := d.executeAndDrain(context.Background(), fb, "p", opts, slog.Default(), "t")
	if err != nil {
		t.Fatal(err)
	}

	// SessionID is set → session was established → should NOT retry.
	shouldRetry := result.Status == "failed" && result.SessionID == ""
	if shouldRetry {
		t.Fatal("should not retry when SessionID is present")
	}
	if int(fb.idx.Load()) != 1 {
		t.Fatalf("expected 1 call, got %d", fb.idx.Load())
	}
}

// statusStreamBackend is a test double for agent.Backend that streams a
// configurable number of MessageStatus events (each carrying sessionID) down
// the message channel before completing successfully. fakeBackend can't
// exercise this: its Execute closes the message channel immediately with no
// streamed messages, so the `case agent.MessageStatus:` branch in
// executeAndDrainForTask (task #105's PinTaskSession wiring) never runs
// against it.
type statusStreamBackend struct {
	sessionID   string
	statusCount int // number of MessageStatus events to stream
}

func (b statusStreamBackend) Execute(_ context.Context, _ string, _ agent.ExecOptions) (*agent.Session, error) {
	// msgCh is deliberately unbuffered and fed from a background goroutine
	// (mirroring how real backends stream messages after Execute returns)
	// rather than pre-filled and closed synchronously. executeAndDrainForTask
	// races its own return (on session.Result) against a drain goroutine
	// reading session.Messages; with a pre-filled buffered channel both
	// session.Messages and session.Result are ready the instant Execute
	// returns, so Go's select can — nondeterministically — pick the drain
	// goroutine's ctx-done case over its message-receive case before a
	// single message is ever read, silently starving PinTaskSession. An
	// unbuffered send only completes once the drain goroutine actually
	// receives it, and Result is only sent after every message send has
	// completed, so the drain goroutine is guaranteed to receive (and start
	// processing) every streamed message before executeAndDrainForTask can
	// possibly return.
	msgCh := make(chan agent.Message)
	resCh := make(chan agent.Result, 1)
	go func() {
		for i := 0; i < b.statusCount; i++ {
			msgCh <- agent.Message{Type: agent.MessageStatus, Status: "running", SessionID: b.sessionID}
		}
		close(msgCh)
		resCh <- agent.Result{Status: "completed", SessionID: b.sessionID}
	}()
	return &agent.Session{Messages: msgCh, Result: resCh}, nil
}

// TestExecuteAndDrain_PinsTaskSessionOnStatusMessage is the task #105
// regression test Parker required before the drain-loop wiring lands: a
// single MessageStatus carrying a session id must actually call
// PinTaskSession. This is the important half of the coverage — an
// idempotency-only test (see the sibling test below) would still pass even
// if the whole `if msg.SessionID != "" && sessionPinned.CompareAndSwap(...)`
// block were deleted outright.
//
// PinTaskSession fires from the drain loop's background goroutine, which is
// deliberately fire-and-forget with respect to executeAndDrainForTask's
// return (see the "Best-effort and bounded" comment in daemon.go) — the
// backend's Result can arrive and unblock the caller before the goroutine
// has processed the buffered MessageStatus at all. So this test cannot just
// check the call count immediately after executeAndDrain returns; it must
// wait (bounded) for the HTTP call to actually land.

// messageStreamBackend streams arbitrary runtime messages before completion.
type messageStreamBackend struct {
	messages []agent.Message
}

func (b messageStreamBackend) Execute(_ context.Context, _ string, _ agent.ExecOptions) (*agent.Session, error) {
	msgCh := make(chan agent.Message)
	resCh := make(chan agent.Result, 1)
	go func() {
		for _, msg := range b.messages {
			msgCh <- msg
		}
		close(msgCh)
		resCh <- agent.Result{Status: "completed"}
	}()
	return &agent.Session{Messages: msgCh, Result: resCh}, nil
}

func TestExecuteAndDrain_DoesNotEmitEmptyThinkingPhase(t *testing.T) {
	var mu sync.Mutex
	var reported []TaskMessageData
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemon/agent-inbox/events/task-thinking/messages" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Messages []TaskMessageData `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode task messages: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		mu.Lock()
		reported = append(reported, body.Messages...)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{client: NewClient(srv.URL), logger: slog.Default()}
	backend := messageStreamBackend{messages: []agent.Message{
		{Type: agent.MessageThinking, Content: "internal plan"},
		{Type: agent.MessageToolUse, Tool: "read_file", CallID: "call-1"},
		{Type: agent.MessageToolResult, Tool: "read_file", CallID: "call-1", Output: "contents"},
		{Type: agent.MessageText, Content: "final response"},
	}}
	result, tools, err := d.executeAndDrain(context.Background(), backend, "prompt", agent.ExecOptions{}, slog.Default(), "task-thinking")
	if err != nil {
		t.Fatalf("executeAndDrain: %v", err)
	}
	if result.Status != "completed" || tools != 1 {
		t.Fatalf("result=%+v tools=%d, want completed with one tool", result, tools)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		got := append([]TaskMessageData(nil), reported...)
		mu.Unlock()
		if len(got) == 4 {
			if got[0].Type != "thinking" || got[0].Content != "internal plan" {
				t.Fatalf("contentful thinking = %+v", got[0])
			}
			if got[1].Type != "tool_use" || got[1].Tool != "read_file" || got[2].Type != "tool_result" || got[2].Tool != "read_file" {
				t.Fatalf("tool activity changed: %+v", got)
			}
			for _, msg := range got {
				if msg.Type == "thinking" && strings.TrimSpace(msg.Content) == "" {
					t.Fatalf("empty thinking phase was emitted: %+v", got)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("reported messages = %+v, want contentful thinking + tool use/result + text", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestExecuteAndDrainDoesNotPublishResidentActivity(t *testing.T) {
	// Inbox/issue tasks share a live resident, but their stream must not
	// steal the Activity tab. Working/tool rows belong to handoffIdleMessages.
	var activities []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", time.Now, func(payload protocol.AgentActivityPayload) {
		activities = append(activities, payload)
	})
	d := New(Config{}, nil)
	d.runnerInstanceID = "daemon-1"
	runner := installTestAgentActivityProducer(t, d, "workspace-1", producer)
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	_, err := runner.processes.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := producer.SetManaged(
		"instance-a",
		protocol.AgentStatusPayload{AgentID: "agent-a", Status: protocol.AgentStatusActive},
		protocol.AgentSessionPayload{AgentID: "agent-a"},
	); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	d.client = NewClient(srv.URL)

	backend := messageStreamBackend{messages: []agent.Message{
		{Type: agent.MessageText, Content: "pong71"},
		{Type: agent.MessageToolUse, Tool: "read_file", CallID: "call-1", Input: map[string]any{"path": "MEMORY.md"}},
	}}
	if _, _, err := d.executeAndDrainForTask(context.Background(), backend, "prompt", agent.ExecOptions{}, slog.Default(), canonicalInboxTaskForTest(Task{
		ID: "task-no-activity", AgentID: "agent-a", RuntimeID: "runtime-1", WorkspaceID: "workspace-1",
	})); err != nil {
		t.Fatalf("executeAndDrainForTask: %v", err)
	}
	if len(activities) != 0 {
		t.Fatalf("task path published resident Activity: %+v", activities)
	}
}

func TestExecuteAndDrain_PinsTaskSessionOnStatusMessage(t *testing.T) {
	t.Parallel()

	type pinCall struct {
		path string
		body map[string]any
	}
	pinned := make(chan pinCall, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		pinned <- pinCall{path: r.URL.Path, body: body}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{client: NewClient(srv.URL), logger: slog.Default()}
	d.client.SetRuntimeDaemonToken("rt-pin", "mdt-runtime", time.Now().Add(time.Hour))

	backend := statusStreamBackend{sessionID: "sess-pin-1", statusCount: 1}
	opts := agent.ExecOptions{Cwd: "/work/task-pin"}
	result, _, err := d.executeAndDrainForTask(context.Background(), backend, "prompt", opts, slog.Default(), canonicalInboxTaskForTest(Task{
		ID:        "task-pin",
		RuntimeID: "rt-pin",
	}))
	if err != nil {
		t.Fatalf("executeAndDrain error: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("result status = %q, want completed", result.Status)
	}

	select {
	case call := <-pinned:
		wantPath := "/api/daemon/tasks/task-pin/session"
		if call.path != wantPath {
			t.Fatalf("pin request path = %q, want %q", call.path, wantPath)
		}
		if call.body["session_id"] != "sess-pin-1" {
			t.Fatalf("pin request session_id = %v, want sess-pin-1 (body=%#v)", call.body["session_id"], call.body)
		}
		if call.body["work_dir"] != "/work/task-pin" {
			t.Fatalf("pin request work_dir = %v, want /work/task-pin (body=%#v)", call.body["work_dir"], call.body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PinTaskSession was never called for a MessageStatus carrying a non-empty SessionID")
	}
}

func TestExecuteAndDrain_DoesNotRecordIssueRunSessionIntoResidentCache(t *testing.T) {
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root}, slog.Default())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	d.client = NewClient(srv.URL)

	agentID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	runtimeID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	if err := d.agentRuntimeSessions.Put(agentID, runtimeID, "resident-session"); err != nil {
		t.Fatal(err)
	}
	backend := statusStreamBackend{sessionID: "issue-run-session", statusCount: 1}
	_, _, err := d.executeAndDrainForTask(context.Background(), backend, "prompt", agent.ExecOptions{Cwd: "/work/issue-run"}, slog.Default(), Task{
		ID:        "task-live-session",
		AgentID:   agentID,
		RuntimeID: runtimeID,
	})
	if err != nil {
		t.Fatalf("executeAndDrainForTask: %v", err)
	}

	got, err := d.agentRuntimeSessions.Get(agentID, runtimeID)
	if err != nil {
		t.Fatalf("read session store: %v", err)
	}
	if got != "resident-session" {
		t.Fatalf("resident cache = %q, want resident-session (issue-run must not overwrite start.config.sessionId)", got)
	}
}

// TestExecuteAndDrain_PinsTaskSessionOnlyOnce covers the
// sessionPinned.CompareAndSwap(false, true) idempotency guard: backends may
// repeat MessageStatus on every turn (e.g. Claude Code CLI streams a status
// update per turn), and PinTaskSession must fire only once per task.
func TestExecuteAndDrain_PinsTaskSessionOnlyOnce(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{client: NewClient(srv.URL), logger: slog.Default()}

	backend := statusStreamBackend{sessionID: "sess-pin-2", statusCount: 2}
	result, _, err := d.executeAndDrain(context.Background(), backend, "prompt", agent.ExecOptions{Cwd: "/work/task-pin-2"}, slog.Default(), "task-pin-2")
	if err != nil {
		t.Fatalf("executeAndDrain error: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("result status = %q, want completed", result.Status)
	}

	// Wait for the first call, then hold for a grace period to give a
	// wrongly-unguarded second call a real chance to land before asserting.
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)

	if got := calls.Load(); got != 1 {
		t.Fatalf("PinTaskSession calls = %d, want exactly 1 across two MessageStatus events on the same task", got)
	}
}

func TestExecuteAndDrain_CodexInactivityReportsToolResultTranscript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX-only")
	}

	fakePath := filepath.Join(t.TempDir(), "codex")
	script := "#!/bin/sh\n" +
		`read line` + "\n" +
		`echo '{"jsonrpc":"2.0","id":1,"result":{}}'` + "\n" +
		`read line` + "\n" +
		`read line` + "\n" +
		`echo '{"jsonrpc":"2.0","id":2,"result":{"thread":{"id":"thr-drain"}}}'` + "\n" +
		`read line` + "\n" +
		`echo '{"jsonrpc":"2.0","id":3,"result":{}}'` + "\n" +
		`echo '{"jsonrpc":"2.0","method":"turn/started","params":{"threadId":"thr-drain","turn":{"id":"turn-drain"}}}'` + "\n" +
		`echo '{"jsonrpc":"2.0","method":"item/started","params":{"threadId":"thr-drain","item":{"type":"commandExecution","id":"cmd-1","command":"git status"}}}'` + "\n" +
		`echo '{"jsonrpc":"2.0","method":"item/completed","params":{"threadId":"thr-drain","item":{"type":"commandExecution","id":"cmd-1","aggregatedOutput":"clean"}}}'` + "\n" +
		`sleep 5` + "\n"
	if err := os.WriteFile(fakePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	if err := os.Chmod(fakePath, 0o755); err != nil {
		t.Fatalf("chmod fake codex: %v", err)
	}

	var mu sync.Mutex
	var reported []TaskMessageData
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemon/agent-inbox/events/task-stale/messages" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Messages []TaskMessageData `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode task messages: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		mu.Lock()
		reported = append(reported, body.Messages...)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	backend, err := agent.New("codex", agent.Config{ExecutablePath: fakePath, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new codex backend: %v", err)
	}
	d := &Daemon{client: NewClient(srv.URL), logger: slog.Default()}
	result, tools, err := d.executeAndDrain(context.Background(), backend, "prompt", agent.ExecOptions{
		Timeout:                   5 * time.Second,
		SemanticInactivityTimeout: 100 * time.Millisecond,
	}, slog.Default(), "task-stale")
	if err != nil {
		t.Fatalf("executeAndDrain: %v", err)
	}
	if result.Status != "timeout" {
		t.Fatalf("expected timeout, got status=%q error=%q", result.Status, result.Error)
	}
	if tools != 1 {
		t.Fatalf("expected one tool use, got %d", tools)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		var gotToolUse, gotToolResult bool
		for _, msg := range reported {
			if msg.Seq == 1 && msg.Type == "tool_use" && msg.Tool == "exec_command" && msg.CallID == "cmd-1" {
				gotToolUse = true
			}
			if msg.Seq == 2 && msg.Type == "tool_result" && msg.Tool == "exec_command" && msg.CallID == "cmd-1" && msg.Output == "clean" {
				gotToolResult = true
			}
		}
		mu.Unlock()
		if gotToolUse && gotToolResult {
			return
		}
		if time.Now().After(deadline) {
			mu.Lock()
			defer mu.Unlock()
			t.Fatalf("expected tool_use seq=1 and tool_result seq=2 in transcript, got %+v", reported)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// blockingBackend returns a Session whose Result channel is never written to,
// so executeAndDrain can only exit via the drainCtx.Done() path.
type blockingBackend struct{}

func (blockingBackend) Execute(_ context.Context, _ string, _ agent.ExecOptions) (*agent.Session, error) {
	msgCh := make(chan agent.Message)
	resCh := make(chan agent.Result)
	close(msgCh)
	return &agent.Session{Messages: msgCh, Result: resCh}, nil
}

func TestExecuteAndDrain_ContextCancelled_ReportsCancelled(t *testing.T) {
	t.Parallel()

	d := newTestDaemon(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, _, err := d.executeAndDrain(ctx, blockingBackend{}, "p", agent.ExecOptions{}, slog.Default(), "t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "cancelled" {
		t.Fatalf("expected status=cancelled when parent ctx is cancelled, got %q (err=%q)", result.Status, result.Error)
	}
}

func TestShellArgsFromEnv(t *testing.T) {
	t.Setenv("MULTICA_CLAUDE_ARGS", `--max-turns 60 --append-system-prompt "multi word"`)
	got, err := shellArgsFromEnv("MULTICA_CLAUDE_ARGS")
	if err != nil {
		t.Fatalf("shellArgsFromEnv: %v", err)
	}
	want := []string{"--max-turns", "60", "--append-system-prompt", "multi word"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestShellArgsFromEnvEmptyIsNil(t *testing.T) {
	t.Setenv("MULTICA_CODEX_ARGS", "   ")
	got, err := shellArgsFromEnv("MULTICA_CODEX_ARGS")
	if err != nil {
		t.Fatalf("shellArgsFromEnv: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for empty env, got %#v", got)
	}
}

func TestDefaultArgsForProvider(t *testing.T) {
	cfg := Config{ClaudeArgs: []string{"--max-turns", "60"}, CodexArgs: []string{"--sandbox", "workspace-write"}}
	if got := defaultArgsForProvider(cfg, "claude"); strings.Join(got, " ") != "--max-turns 60" {
		t.Fatalf("unexpected claude args: %#v", got)
	}
	if got := defaultArgsForProvider(cfg, "codex"); strings.Join(got, " ") != "--sandbox workspace-write" {
		t.Fatalf("unexpected codex args: %#v", got)
	}
	if got := defaultArgsForProvider(cfg, "gemini"); got != nil {
		t.Fatalf("expected nil for unsupported provider, got %#v", got)
	}
}

// reportTaskResultRecorder captures which terminal endpoint
// (.../complete or .../fail) reportTaskResult hits and the body it
// posts, so the tests can assert the disposition (success vs fail)
// independently of the rest of handleTask.
type reportTaskResultRecorder struct {
	mu      sync.Mutex
	path    string
	method  string
	payload map[string]any
}

func (r *reportTaskResultRecorder) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var payload map[string]any
		if len(body) > 0 {
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("decode body: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		r.mu.Lock()
		r.path = req.URL.Path
		r.method = req.Method
		r.payload = payload
		r.mu.Unlock()
		if strings.HasSuffix(req.URL.Path, "/complete") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"terminal_outcome":"replied","resume_unsafe":false}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

func TestReportTaskResult_CompletedHitsCompleteEndpoint(t *testing.T) {
	t.Parallel()

	rec := &reportTaskResultRecorder{}
	srv := httptest.NewServer(rec.handler(t))
	t.Cleanup(srv.Close)

	d := &Daemon{client: NewClient(srv.URL), logger: slog.Default()}
	d.reportTaskResultForTask(context.Background(), canonicalInboxTaskForTest(Task{ID: "task-1"}), TaskResult{
		Status:  "completed",
		Comment: "all good",
		Parts: []protocol.MessagePart{{
			Type:      protocol.MessagePartTypeSticker,
			PackID:    "builtin",
			StickerID: "hi",
			Alt:       "Hi",
		}},
		SessionID: "ses-1",
		WorkDir:   "/tmp/foo",
	}, slog.Default())

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.path != "/api/daemon/agent-inbox/events/task-1/complete" {
		t.Fatalf("expected /complete endpoint, got %s", rec.path)
	}
	if rec.payload["output"] != "all good" {
		t.Errorf("output: got %v", rec.payload["output"])
	}
	if rec.payload["session_id"] != "ses-1" {
		t.Errorf("session_id: got %v", rec.payload["session_id"])
	}
	parts, ok := rec.payload["parts"].([]any)
	if !ok || len(parts) != 1 {
		t.Fatalf("parts: got %#v, want one structured part", rec.payload["parts"])
	}
	part, ok := parts[0].(map[string]any)
	if !ok {
		t.Fatalf("parts[0]: got %#v", parts[0])
	}
	if part["type"] != "sticker" || part["sticker_id"] != "hi" || part["pack_id"] != "builtin" {
		t.Errorf("parts[0]: got %#v, want builtin hi sticker", part)
	}
}

// Pins the GitHub multica#1952 fail-closed behaviour: a task whose
// agent run never produced a real result (blocked, cancelled, or any
// future status we forget to enumerate) MUST go through FailTask, so
// the UI never shows a green "Completed" badge for a run that didn't
// actually do anything (e.g. provider 429 / out-of-credit).
func TestReportTaskResult_NonCompletedHitsFailEndpoint(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name              string
		status            string
		comment           string
		failureReasonIn   string
		wantFailureReason string
	}{
		{
			name:              "blocked with explicit reason preserves it",
			status:            "blocked",
			comment:           "rate limit reached",
			failureReasonIn:   "iteration_limit",
			wantFailureReason: "iteration_limit",
		},
		{
			// MUL-2946: when the daemon doesn't supply a refined
			// reason, the comment text is run through
			// taskfailure.Classify so the failure_reason column
			// lands in the canonical refined taxonomy instead of
			// the legacy "agent_error" coarse bucket.
			name:              "blocked without reason classifies comment as rate-limit",
			status:            "blocked",
			comment:           "rate limit reached",
			failureReasonIn:   "",
			wantFailureReason: "agent_error.provider_capacity_or_rate_limit",
		},
		{
			name:              "blocked without reason and unrecognized comment lands in agent_error.unknown",
			status:            "blocked",
			comment:           "the agent gave up for reasons we don't recognize",
			failureReasonIn:   "",
			wantFailureReason: "agent_error.unknown",
		},
		{
			name:              "cancelled defaults to cancelled reason regardless of comment",
			status:            "cancelled",
			comment:           "rate limit reached",
			failureReasonIn:   "",
			wantFailureReason: "cancelled",
		},
		{
			name:              "unknown status routes through classifier",
			status:            "weird_new_status",
			comment:           "rate limit reached",
			failureReasonIn:   "",
			wantFailureReason: "agent_error.provider_capacity_or_rate_limit",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &reportTaskResultRecorder{}
			srv := httptest.NewServer(rec.handler(t))
			t.Cleanup(srv.Close)

			d := &Daemon{client: NewClient(srv.URL), logger: slog.Default()}
			d.reportTaskResultForTask(context.Background(), canonicalInboxTaskForTest(Task{ID: "task-x"}), TaskResult{
				Status:        tc.status,
				Comment:       tc.comment,
				SessionID:     "ses-x",
				WorkDir:       "/tmp/x",
				FailureReason: tc.failureReasonIn,
			}, slog.Default())

			rec.mu.Lock()
			defer rec.mu.Unlock()
			if rec.path != "/api/daemon/agent-inbox/events/task-x/fail" {
				t.Fatalf("expected /fail endpoint for status=%q, got %s", tc.status, rec.path)
			}
			if rec.payload["error"] != tc.comment {
				t.Errorf("error body: got %v", rec.payload["error"])
			}
			if got := rec.payload["failure_reason"]; got != tc.wantFailureReason {
				t.Errorf("failure_reason: got %v, want %q", got, tc.wantFailureReason)
			}
			if rec.payload["session_id"] != "ses-x" {
				t.Errorf("session_id should be forwarded on failure paths so chat resume keeps working, got %v", rec.payload["session_id"])
			}
		})
	}
}

// Regression test for the MUL-2780 incident: a short 502 burst on the
// /complete callback used to (a) drop the task at the first failure and
// (b) wrongly fall back to /fail, surfacing a successful run as red.
// With the retry helper in place, a transient 502 followed by a 200 must
// resolve via /complete without ever touching /fail.
func TestReportTaskResult_RetriesTransientCompleteThenSucceeds(t *testing.T) {
	defer noSleepRetry(t)()

	var completeCalls, failCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/complete"):
			n := completeCalls.Add(1)
			if n == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"terminal_outcome":"replied","resume_unsafe":false}`))
		case strings.HasSuffix(req.URL.Path, "/fail"):
			failCalls.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{client: NewClient(srv.URL), logger: slog.Default()}
	d.reportTaskResultForTask(context.Background(), canonicalInboxTaskForTest(Task{ID: "task-retry"}), TaskResult{
		Status:  "completed",
		Comment: "ok",
	}, slog.Default())

	if got := completeCalls.Load(); got != 2 {
		t.Fatalf("expected 2 complete attempts (one 502, one 200), got %d", got)
	}
	if got := failCalls.Load(); got != 0 {
		t.Fatalf("transient 502 must not fall back to /fail (would lose successful result), got %d /fail calls", got)
	}
}

// Pins the new "don't downgrade success to failure on transient errors"
// rule: when /complete is 502 across the entire retry schedule, we must
// NOT fall through to /fail — that would surface a real success as a
// failure in the UI. The task is left in running for a future recovery
// path to pick up.
func TestReportTaskResult_TransientCompleteExhaustedDoesNotFallback(t *testing.T) {
	defer noSleepRetry(t)()

	prevSchedule := defaultTerminalRetrySchedule
	defaultTerminalRetrySchedule = []time.Duration{time.Nanosecond, time.Nanosecond}
	t.Cleanup(func() { defaultTerminalRetrySchedule = prevSchedule })

	var completeCalls, failCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/complete"):
			completeCalls.Add(1)
			w.WriteHeader(http.StatusBadGateway)
		case strings.HasSuffix(req.URL.Path, "/fail"):
			failCalls.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{client: NewClient(srv.URL), logger: slog.Default()}
	d.reportTaskResultForTask(context.Background(), canonicalInboxTaskForTest(Task{ID: "task-stuck"}), TaskResult{
		Status:  "completed",
		Comment: "ok",
	}, slog.Default())

	if got := completeCalls.Load(); got != int32(len(defaultTerminalRetrySchedule)+1) {
		t.Fatalf("expected %d complete attempts, got %d", len(defaultTerminalRetrySchedule)+1, got)
	}
	if got := failCalls.Load(); got != 0 {
		t.Fatalf("exhausted transient retries must NOT fall back to /fail; got %d /fail calls", got)
	}
}

// On permanent 4xx from /complete (e.g. 400 bad body, 404 task not found)
// the helper bails immediately and the daemon falls back to /fail so the
// UI shows a concrete failure rather than a perpetually-running task.
func TestReportTaskResult_PermanentCompleteFallsBackToFail(t *testing.T) {
	defer noSleepRetry(t)()

	var completeCalls, failCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/complete"):
			completeCalls.Add(1)
			w.WriteHeader(http.StatusBadRequest)
		case strings.HasSuffix(req.URL.Path, "/fail"):
			failCalls.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{client: NewClient(srv.URL), logger: slog.Default()}
	d.reportTaskResultForTask(context.Background(), canonicalInboxTaskForTest(Task{ID: "task-bad"}), TaskResult{
		Status:  "completed",
		Comment: "ok",
	}, slog.Default())

	if got := completeCalls.Load(); got != 1 {
		t.Fatalf("permanent 400 should not retry, got %d complete attempts", got)
	}
	if got := failCalls.Load(); got != 1 {
		t.Fatalf("permanent /complete should fall back to /fail exactly once, got %d", got)
	}
}

// TestHandleTask_ReportsInboxUsageBeforeCompletion pins the canonical
// execution-ledger order: usage is persisted before the terminal callback.
func TestHandleTask_ReportsInboxUsageBeforeCompletion(t *testing.T) {
	t.Parallel()

	var callOrder []string
	var mu sync.Mutex
	recordCall := func(name string) {
		mu.Lock()
		callOrder = append(callOrder, name)
		mu.Unlock()
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/usage"):
			recordCall("usage")
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/complete"):
			recordCall("complete")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"terminal_outcome":"replied","resume_unsafe":false}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{
		client:             NewClient(srv.URL),
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:         make(map[string]*workspaceState),
		runtimeIndex:       map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		cancelPollInterval: time.Hour,
	}

	// Inject a fake runner that returns a result with usage tokens, bypassing
	// real agent process execution.
	d.runner = taskRunnerFunc(func(_ context.Context, _ Task, _ string, _ int, _ *slog.Logger) (TaskResult, error) {
		return TaskResult{
			Status: "completed",
			Usage: []TaskUsageEntry{
				{Provider: "anthropic", Model: "claude-opus-4-6", InputTokens: 100, OutputTokens: 50},
			},
		}, nil
	})

	task := canonicalInboxTaskForTest(Task{
		ID:        "task-abc",
		RuntimeID: "rt-1",
		IssueID:   "issue-xyz",
		Agent:     &AgentData{Name: "test-agent"},
	})

	d.handleTask(context.Background(), task, 0)

	mu.Lock()
	order := make([]string, len(callOrder))
	copy(order, callOrder)
	mu.Unlock()

	usageIdx, completeIdx := -1, -1
	for i, name := range order {
		switch name {
		case "usage":
			usageIdx = i
		case "complete":
			completeIdx = i
		}
	}

	if usageIdx == -1 {
		t.Fatal("canonical inbox usage endpoint was never called")
	}
	if completeIdx == -1 {
		t.Fatal("canonical inbox complete endpoint was never called")
	}
	if usageIdx > completeIdx {
		t.Fatalf("usage was reported after completion (order: %v)", order)
	}
}

// TestHandleTask_ReportsUsageWhenInboxLeaseIsLost verifies that usage from the
// immutable provider execution is retained even when the delivery lease is
// lost and the stale result itself is discarded.
func TestHandleTask_ReportsUsageWhenInboxLeaseIsLost(t *testing.T) {
	t.Parallel()

	var callOrder []string
	var mu sync.Mutex
	recordCall := func(name string) {
		mu.Lock()
		callOrder = append(callOrder, name)
		mu.Unlock()
	}

	var renewCalls atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/usage"):
			recordCall("usage")
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/renew"):
			if renewCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusOK)
			} else {
				recordCall("lease-lost")
				http.Error(w, "lease lost", http.StatusConflict)
			}
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{
		client:             NewClient(srv.URL),
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:         make(map[string]*workspaceState),
		runtimeIndex:       map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		cancelPollInterval: 10 * time.Millisecond, // fire quickly so test is fast
	}

	// Block until lease loss cancels the provider context, then return the
	// usage already accumulated by the provider.
	d.runner = taskRunnerFunc(func(runCtx context.Context, _ Task, _ string, _ int, _ *slog.Logger) (TaskResult, error) {
		<-runCtx.Done()
		return TaskResult{
			Status: "aborted",
			Usage: []TaskUsageEntry{
				{Provider: "anthropic", Model: "claude-opus-4-6", InputTokens: 200, OutputTokens: 80},
			},
		}, nil
	})

	task := canonicalInboxTaskForTest(Task{
		ID:        "task-poll",
		RuntimeID: "rt-1",
		IssueID:   "issue-poll",
		Agent:     &AgentData{Name: "test-agent"},
	})

	d.handleTask(context.Background(), task, 0)

	mu.Lock()
	order := make([]string, len(callOrder))
	copy(order, callOrder)
	mu.Unlock()

	leaseLostIdx := -1
	usageIdx := -1
	for i, name := range order {
		switch name {
		case "lease-lost":
			leaseLostIdx = i
		case "usage":
			usageIdx = i
		}
	}
	if leaseLostIdx == -1 {
		t.Fatalf("lease renewal was never rejected (order: %v)", order)
	}
	if usageIdx == -1 {
		t.Fatalf("usage was not recorded after lease loss (order: %v)", order)
	}
	if usageIdx < leaseLostIdx {
		t.Fatalf("usage reported before lease loss (order: %v)", order)
	}
}
