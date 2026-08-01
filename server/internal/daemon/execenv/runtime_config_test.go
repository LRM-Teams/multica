package execenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestAssignmentSnapshotBriefAvoidsRedundantRoundTrips(t *testing.T) {
	for _, tc := range []struct {
		name            string
		commentCount    int64
		wantCommentRead bool
	}{
		{name: "zero comments", commentCount: 0, wantCommentRead: false},
		{name: "existing comments", commentCount: 3, wantCommentRead: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := TaskContextForEnv{
				IssueID: "issue-assignment",
				AssignmentSnapshot: &protocol.IssueAssignmentSnapshot{
					Title:              "Frozen title",
					AcceptanceCriteria: []string{},
					Status:             "in_progress",
					Metadata:           map[string]any{},
					CommentCount:       tc.commentCount,
				},
			}
			out := buildMetaSkillContent("claude", ctx)
			for _, forbidden := range []string{
				"Run `multica issue get issue-assignment --output json`",
				"Run `multica issue metadata list issue-assignment --output json`",
			} {
				if strings.Contains(out, forbidden) {
					t.Errorf("snapshot brief contains redundant read %q\n--- output ---\n%s", forbidden, out)
				}
			}
			hasCommentRead := strings.Contains(out, "`multica issue comment list issue-assignment --output json`")
			if hasCommentRead != tc.wantCommentRead {
				t.Errorf("comment read present=%v, want %v\n--- output ---\n%s", hasCommentRead, tc.wantCommentRead, out)
			}
			if !strings.Contains(out, "claim-time current status") {
				t.Errorf("snapshot brief does not identify current status semantics\n--- output ---\n%s", out)
			}
			issueContext := renderIssueContext("claude", ctx)
			if strings.Contains(issueContext, "multica issue get") {
				t.Errorf("snapshot issue_context contains redundant issue get\n--- output ---\n%s", issueContext)
			}
		})
	}
}

func TestTerminalAssignmentSnapshotStopsWithoutIssueCommands(t *testing.T) {
	ctx := TaskContextForEnv{
		IssueID: "issue-terminal",
		AssignmentSnapshot: &protocol.IssueAssignmentSnapshot{
			Title:              "Frozen title",
			AcceptanceCriteria: []string{},
			Status:             "cancelled",
			Metadata:           map[string]any{},
			CommentCount:       5,
		},
	}
	out := buildMetaSkillContent("claude", ctx)
	for _, want := range []string{
		"already `cancelled` at claim time",
		"stale assignment wake",
		"do not perform issue work",
		"one concise terminal-state result",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("terminal snapshot brief missing %q\n--- output ---\n%s", want, out)
		}
	}
	for _, forbidden := range []string{
		"multica issue get issue-terminal",
		"multica issue metadata list issue-terminal",
		"multica issue comment list issue-terminal",
		"multica issue status issue-terminal",
		"multica issue comment add issue-terminal",
		"Complete the task **to its acceptance criteria",
		"## Sub-issue Creation",
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("terminal snapshot brief contains forbidden instruction %q\n--- output ---\n%s", forbidden, out)
		}
	}
}

// Sub-issue Creation section — after MUL-2538 the platform posts the
// child-done parent notification itself, so the brief no longer carries
// any parent-notification rule (per Bohan's call on PR #3055: delete the
// guidance entirely, do not replace it with a "do not post one" sentence
// — the agent should not be thinking about parent comments at all). All
// that remains is the `--status todo` vs `--status backlog` rule for
// creating sub-issues, which is unrelated to the notification path.

func TestSubIssueCreationSectionPresentForIssueRuns(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ctx  TaskContextForEnv
	}{
		{
			name: "assignment-triggered",
			ctx:  TaskContextForEnv{IssueID: "11111111-2222-3333-4444-555555555555"},
		},
		{
			name: "comment-triggered",
			ctx: TaskContextForEnv{
				IssueID:          "22222222-3333-4444-5555-666666666666",
				TriggerCommentID: "33333333-4444-5555-6666-777777777777",
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := buildMetaSkillContent("claude", tc.ctx)

			if !strings.Contains(out, "## Sub-issue Creation") {
				t.Fatalf("expected Sub-issue Creation section in %s brief", tc.name)
			}
			for _, want := range []string{
				"**Choosing `--status` when creating sub-issues.**",
				"`--status todo` = **start now**",
				"`--status backlog` = **wait**",
				"`multica issue status <child-id> todo`",
				"all `--status todo`",
				"`--status backlog` from the start",
				// Reference images must be viewed (or flagged), not silently guessed.
				"fetch and actually look at them before doing UI/visual work",
				"Dropping a provided visual reference and shipping a blind approximation is a defect",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("[%s] section missing %q", tc.name, want)
				}
			}
		})
	}
}

// The brief must no longer carry any parent-notification guidance. PR
// #2918 added a "Tell the parent when you finish a child" rule that
// turned into noise (self-mention loops, planner ack ping-pong,
// hardcoded `MUL-` prefix). PR #3055 first downgraded it to a "do NOT
// post one" guardrail, but Bohan's product call was to remove the
// guidance entirely rather than substitute a new prohibition. These
// canaries lock that in: any wording that re-introduces the
// parent-comment concept — positive, negative, or descriptive — must
// not come back through future edits.
func TestBriefHasNoParentNotificationGuidance(t *testing.T) {
	t.Parallel()
	cases := []TaskContextForEnv{
		{IssueID: "11111111-2222-3333-4444-555555555555"},
		{
			IssueID:          "22222222-3333-4444-5555-666666666666",
			TriggerCommentID: "33333333-4444-5555-6666-777777777777",
		},
	}
	for _, ctx := range cases {
		ctx := ctx
		out := buildMetaSkillContent("claude", ctx)

		// The pre-MUL-2538 phrasing instructed the agent to compose a
		// parent comment by hand — including a hardcoded `MUL-` prefix
		// and an assignee mention. The intermediate revision (PR #3055
		// before Bohan's call) instead told the agent NOT to post one.
		// Both framings must stay out.
		for _, banned := range []string{
			// Old "do it yourself" framing (PR #2918).
			"## Parent / Sub-issue Protocol",
			"**Tell the parent when you finish a child.**",
			"multica issue comment add <parent-id>",
			"with NO `--parent`",
			"link the child as `[MUL-",
			"`@mention` the parent's assignee",
			"`mention://agent/<id>`",
			"`mention://member/<id>`",
			"`mention://squad/<id>`",
			// Intermediate "do NOT do it yourself" framing (PR #3055
			// before Bohan's call) — also out per product direction.
			"**Do NOT post your own parent-notification comment.**",
			"Do NOT post your own parent-notification comment",
			"parent-notification comment",
			"system comment on the parent fires from the status transition",
			"re-trigger the parent's assignee for nothing",
			"platform posts a top-level system comment on the parent",
			// Earlier revisions split rules by trigger type or used
			// table/subsection layouts. None of those structures should
			// come back either.
			"| Parent assignee | Parent status |",
			"The same agent as yourself",
			"| Member or squad |",
			"### A. Notify the parent",
			"### B. Choose",
			"When this issue has `parent_issue_id`:",
			"**Closing out child work** (only if this issue has `parent_issue_id`)",
			"**Notify the parent** (only if this issue has `parent_issue_id`",
			"**Creating sub-issues** (applies to any issue-bound run)",
			"For parent/child work, use these best-effort rules",
			// The protocol must no longer emit a placeholder
			// `<this-issue-id>` status flip — the workflow above owns
			// that command with the real issue id substituted.
			"`multica issue status <this-issue-id> in_review`",
			// Non-existent CLI form Elon's earlier review flagged.
			"issue list --parent",
		} {
			if strings.Contains(out, banned) {
				t.Errorf("expected %q to be removed from the brief", banned)
			}
		}
	}
}

// Comment-triggered briefs must NOT carry any unconditional status-flip
// command targeting the current issue. Previous revisions had a
// dedicated protocol step that wrote `multica issue status <this-issue-id> in_review`;
// the comment-triggered workflow rule "Do NOT change the issue status
// unless the comment explicitly asks for it" must remain the source of
// truth (Elon's blocking review on PR #2918).
func TestCommentTriggeredProtocolDoesNotForceInReview(t *testing.T) {
	t.Parallel()
	ctx := TaskContextForEnv{
		IssueID:          "55555555-6666-7777-8888-999999999999",
		TriggerCommentID: "66666666-7777-8888-9999-aaaaaaaaaaaa",
	}
	out := buildMetaSkillContent("claude", ctx)

	if strings.Contains(out, "`multica issue status <this-issue-id> in_review`") {
		t.Errorf("comment-triggered brief must not contain a placeholder `<this-issue-id> in_review` flip — that conflicts with the comment-triggered \"do not change status unless asked\" rule")
	}

	const guardrail = "Do NOT change the issue status unless the comment explicitly asks for it"
	if !strings.Contains(out, guardrail) {
		t.Errorf("expected the comment-triggered workflow guardrail %q to be present", guardrail)
	}
}

// The CLAUDE.md workflow surface must carry the same issue-wide since-delta
// new-comment hint as the per-turn prompt. PR #2816 requires the two surfaces
// stay in sync.
func TestCommentTriggeredBriefCarriesNewCommentsHint(t *testing.T) {
	t.Parallel()
	const (
		issueID = "55555555-6666-7777-8888-999999999999"
		since   = "2026-05-28T11:00:00Z"
	)
	ctx := TaskContextForEnv{
		IssueID:          issueID,
		TriggerCommentID: "reply-abc",
		NewCommentCount:  4,
		NewCommentsSince: since,
	}
	out := buildMetaSkillContent("claude", ctx)

	// Issue-wide count.
	if !strings.Contains(out, "4 new comment(s) on this issue since your last run") {
		t.Errorf("comment brief must report the issue-wide new-comment count, got:\n%s", out)
	}
	if !strings.Contains(out, "blindly") {
		t.Errorf("comment brief must discourage blindly reading every new comment, got:\n%s", out)
	}
	// Parent thread first.
	if !strings.Contains(out, "--thread reply-abc --since "+since+" --output json") {
		t.Errorf("comment brief must point at the triggering (parent) thread --since read first, got:\n%s", out)
	}
	if !strings.Contains(out, "--tail 30") {
		t.Errorf("comment brief must offer the full-thread (--tail 30) option, got:\n%s", out)
	}
	// Issue-wide catch-up demoted to an only-if-needed fallback.
	if !strings.Contains(out, "multica issue comment list "+issueID+" --since "+since+" --output json") {
		t.Errorf("comment brief must keep the issue-wide --since catch-up fallback, got:\n%s", out)
	}
	// The removed resolve step must not reappear.
	if strings.Contains(out, "multica comment resolve") {
		t.Errorf("comment brief must not carry the dropped resolve step, got:\n%s", out)
	}
}

// Cold start (no prior run → no since anchor) must point the agent at the
// triggering CONVERSATION (--thread <trigger> --tail 30) instead of the flat
// timeline dump or the since-delta hint.
func TestCommentTriggeredBriefColdStartThreadRead(t *testing.T) {
	t.Parallel()
	const issueID = "55555555-6666-7777-8888-999999999999"
	ctx := TaskContextForEnv{
		IssueID:          issueID,
		TriggerCommentID: "trigger-1",
		TriggerThreadID:  "thread-root-1",
		NewCommentCount:  0,
		NewCommentsSince: "",
	}
	out := buildMetaSkillContent("claude", ctx)
	if strings.Contains(out, "new comment(s) since your last run") {
		t.Errorf("no since-delta hint should render on cold start, got:\n%s", out)
	}
	if !strings.Contains(out, "multica issue comment list "+issueID+" --thread thread-root-1 --tail 30 --output json") {
		t.Errorf("cold start must point at the triggering thread read, got:\n%s", out)
	}
}

// A resumed comment session with no since-delta should not fall back to the
// cold-start "read the triggering conversation first" instruction. The trigger
// body is already embedded in the per-turn prompt and the resumed session should
// carry prior thread context, so the thread read is only a fallback.
func TestCommentTriggeredBriefResumedNoDeltaSkipsDefaultThreadRead(t *testing.T) {
	t.Parallel()
	const issueID = "55555555-6666-7777-8888-999999999999"
	ctx := TaskContextForEnv{
		IssueID:             issueID,
		TriggerCommentID:    "trigger-1",
		TriggerThreadID:     "thread-root-1",
		PriorSessionResumed: true,
		NewCommentCount:     0,
		NewCommentsSince:    "",
	}
	out := buildMetaSkillContent("claude", ctx)

	for _, want := range []string{
		"triggering comment is already included above",
		"No other new comments on this issue since your last run",
		"active thread anchor `thread-root-1` and triggering comment ID `trigger-1`",
		"If your reply depends on thread context",
		"do not rely only on resumed session memory",
		"multica issue comment list " + issueID + " --thread thread-root-1 --tail 30 --output json",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("resumed/no-delta brief missing %q\n--- output ---\n%s", want, out)
		}
	}
	if strings.Contains(out, "scoped to the triggering thread") {
		t.Errorf("resumed/no-delta brief must not claim the delta is thread-scoped, got:\n%s", out)
	}
	if strings.Contains(out, "Read the triggering conversation first") {
		t.Errorf("resumed/no-delta brief must not use the cold-start forced-read wording, got:\n%s", out)
	}
}

// Assignment-triggered briefs are the high-risk path for role conflicts:
// non-executor agents still need issue context, but the runtime workflow must
// not turn status changes, investigation, implementation, or delegation into
// permissions that override Agent Identity.
func TestAssignmentTriggeredProtocolHonorsAgentIdentity(t *testing.T) {
	t.Parallel()
	const issueID = "77777777-8888-9999-aaaa-bbbbbbbbbbbb"
	ctx := TaskContextForEnv{IssueID: issueID}
	out := buildMetaSkillContent("claude", ctx)

	for _, want := range []string{
		"## Instruction Precedence",
		"Agent Identity instructions have priority over the assignment workflow below.",
		"If a workflow step conflicts with Agent Identity, skip the conflicting action",
		"Never treat this runtime workflow as permission to change issue status, investigate, implement",
		"Run `multica issue status " + issueID + " in_progress` unless your Agent Identity forbids issue status changes; if it does, skip this step.",
		"Complete the task **to its acceptance criteria / definition of done** within your Agent Identity boundaries",
		"self-verify before you treat it as done",
		"Ship to acceptance criteria, not a shallow pass.",
		"Harness swap does not rewrite Multica kernel semantics",
		"Durable Multica memory stays on `MULTICA_AGENT_ROOT`",
		"Issue description + acceptance criteria + attachments = spec.",
		"Challenge a bad spec with its owner",
		"Do not investigate, implement, create issues, update issues, or delegate if your Agent Identity forbids that action",
		"When done, run `multica issue status " + issueID + " in_review` unless your Agent Identity forbids issue status changes; if it does, skip this step.",
		"If blocked, run `multica issue status " + issueID + " blocked` unless your Agent Identity forbids issue status changes.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("assignment-triggered brief missing identity-bound workflow text %q\n---\n%s", want, out)
		}
	}

	for _, banned := range []string{
		"4. Run `multica issue status " + issueID + " in_progress`\n",
		"5. Follow your Skills and Agent Identity to complete the task (write code, investigate, etc.)",
		"8. When done, run `multica issue status " + issueID + " in_review`\n",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("assignment-triggered brief still contains unconditional legacy workflow text %q\n---\n%s", banned, out)
		}
	}
}

func TestRuntimeBriefHasOneContractWithoutModeIdentity(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ctx  TaskContextForEnv
	}{
		{name: "issue", ctx: TaskContextForEnv{IssueID: "issue-1"}},
		{name: "channel", ctx: TaskContextForEnv{ChatSessionID: "chat-1", ChannelID: "channel-1"}},
		{name: "standalone", ctx: TaskContextForEnv{ChatSessionID: "chat-1"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := buildMetaSkillContent("codex", tc.ctx)
			lower := strings.ToLower(out)
			if strings.Contains(lower, "chat"+" mode") {
				t.Fatalf("brief still assigns a mode identity\n---\n%s", out)
			}
			for _, heading := range []string{
				"## Pinned Rules",
				"## Available Commands",
				"## Attachments",
				"## Important: Always Use the `multica` CLI",
				"## Output",
			} {
				if got := strings.Count(out, heading); got != 1 {
					t.Fatalf("%s heading count=%d want 1\n---\n%s", heading, got, out)
				}
			}
			for _, banned := range []string{
				"Pinned rules are high-frequency or safety-critical",
				"Use them only for intentional notification, escalation, or delegation.",
			} {
				if strings.Contains(out, banned) {
					t.Fatalf("brief retains deleted meta/no-op rule %q\n---\n%s", banned, out)
				}
			}
			for _, want := range []string{
				"All Multica platform I/O via `multica` CLI. No raw HTTP.",
				"`--output json` for structured reads",
				"`--full-id` when canonical UUIDs matter",
				"Issue writes require claim/Agent Identity authority.",
				"Never self-approve `in_review -> done`",
				"Agent-authored issue comments: never inline `--content`",
				"Ship to acceptance criteria, not a shallow pass.",
				"build · run · exercise behavior · test · UI screenshot vs target",
			} {
				if !strings.Contains(out, want) {
					t.Fatalf("brief missing shared rule %q\n---\n%s", want, out)
				}
			}
			switch tc.name {
			case "issue":
				for _, want := range []string{
					"Issue description + acceptance criteria + attachments = spec.",
					"Challenge a bad spec with its owner",
				} {
					if !strings.Contains(out, want) {
						t.Fatalf("issue brief missing scoped rule %q\n---\n%s", want, out)
					}
				}
			case "channel":
				for _, want := range []string{
					"Thread attention is explicit.",
					"`ChannelID` target present",
					"task-scoped Multica CLI transport",
					"final assistant output, is not delivered",
				} {
					if !strings.Contains(out, want) {
						t.Fatalf("channel brief missing scoped rule %q\n---\n%s", want, out)
					}
				}
			case "standalone":
				for _, want := range []string{
					"Thread attention is explicit.",
					"No `ChannelID` target",
					"final assistant output is delivered to the current session",
					"search for a target, or invent one",
				} {
					if !strings.Contains(out, want) {
						t.Fatalf("targetless brief missing scoped rule %q\n---\n%s", want, out)
					}
				}
			}
		})
	}
}

func TestChatRuntimeBriefIsLeanButKeepsFastChatPaths(t *testing.T) {
	t.Parallel()
	ctx := TaskContextForEnv{
		ChatSessionID: "chat-1",
		ChannelID:     "channel-1",
		AgentSkills: []SkillContextForEnv{{
			Name:        "Issue Triage",
			Description: "Use when organizing issue work.",
			Content:     "Full skill body should live on disk.",
		}},
		Repos:        []RepoContextForEnv{{URL: "https://github.com/acme/app.git", Description: "main app"}},
		ProjectTitle: "Launch Project",
		ProjectResources: []ProjectResourceForEnv{{
			ResourceType: "github_repo",
			ResourceRef:  []byte(`{"url":"https://github.com/acme/app.git"}`),
		}},
	}
	out := buildMetaSkillContent("codex", ctx)

	for _, want := range []string{
		"## Delivery",
		"task-scoped Multica CLI transport",
		"Context boundaries:",
		"Common chat command forms are listed here so you can use them directly",
		"Do NOT run `multica message send --help`",
		"Common capability index",
		"Delivery boundary: only successful chat send/react commands deliver visible chat output.",
		"Text outside those commands, including final assistant output, is never delivered.",
		"Chat output: use `multica message send --target <target>`",
		"explicit target",
		"#channel:<threadId>",
		"dm:@handle:<threadId>",
		"--message \"short text\"",
		"--message-stdin",
		"--message-file <path>",
		"--sticker <id>",
		"--voice",
		"explicitly asks for a spoken/voice reply",
		"`--voice` is the only supported voice-reply path",
		"do not synthesize, encode, upload, or attach an audio file",
		"Common sticker fast path",
		"greeting `hi`",
		"multica sticker search <query>",
		"do not list the whole sticker catalog",
		"Freshness holds:",
		"Message held by freshness check",
		"CLI also exits non-zero",
		"same turn",
		"bounded `heldMessages`",
		"explicit `contextWindow`",
		"`decisionCommands.revisedSend`",
		"`decisionCommands.sendDraft`",
		"choose not to send anything",
		"inferred as an `abandoned` resolution",
		"Do not reconstruct either command",
		"a freshness `held` result exits non-zero",
		"multica message react --message-id <id>",
		"multica message read [--target ...] [--limit N] --output json",
		"multica message search \"query\" [--target ...] --output json",
		"Issues/comments: `multica issue list|get|search|comment ...`",
		"issue list --mine --output json",
		"must not self-approve `in_review -> done`",
		"Issue metadata: `multica issue metadata list|set|delete ...`",
		"multica repo checkout <url>",
		"multica attachment view <id> --output <path>",
		"## Repositories",
		"## Project Context",
		"## Skills",
		"$CODEX_HOME/skills/issue-triage/SKILL.md",
		"After the command succeeds",
		compactCloseoutStatusInstruction,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("chat brief missing %q\n---\n%s", want, out)
		}
	}

	for _, banned := range []string{
		"progressively load exact flags",
		"run the relevant help command before low-frequency or destructive operations",
		"## Comment Formatting",
		"## Issue Metadata",
		"## Sub-issue Creation",
		"## Mentions\n\n",
		"## Mention Safety",
		"### Workflow",
		"You are responsible for managing the issue status throughout your work",
		"Run `multica issue status ",
		"Final results MUST be delivered",
		"For issue comments, always use `--content-stdin` with a HEREDOC",
		"Post your final results as a comment",
		"omit `--target`",
		"omit --target",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("chat brief should not contain issue-task contract text %q\n---\n%s", banned, out)
		}
	}
}

func TestChatRuntimeBriefPinsRaftThreadUnfollowDecisionBoundary(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		directed bool
	}{
		{name: "ambient", directed: false},
		{name: "directed", directed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := buildMetaSkillContent("codex", TaskContextForEnv{
				ChatSessionID: "chat-1",
				Directed:      tc.directed,
			})

			for _, want := range []string{
				"Thread attention is explicit.",
				"Unfollow only after work and every handoff/review/decision/reply/follow-up completes.",
				"CI/deploy/human wait/reminder/idle/task-done/mute are not completion.",
				"Personal @mentions still pierce; posting re-follows.",
				"only under the explicit thread-attention boundary pinned above",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("%s chat brief missing thread-unfollow boundary %q\n---\n%s", tc.name, want, out)
				}
			}
		})
	}
}

func TestChatRuntimeBriefPinsReminderDecisionBoundary(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		directed bool
	}{
		{name: "ambient", directed: false},
		{name: "directed", directed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := buildMetaSkillContent("codex", TaskContextForEnv{
				ChatSessionID: "chat-1",
				Directed:      tc.directed,
			})

			for _, want := range []string{
				"cannot close the work now because it depends on a future time or external state",
				"CI or deployment completion",
				"a human reply",
				"a daemon reconnect",
				"a scheduled recheck",
				"a periodic report",
				"can finish in the current run",
				"within about one minute",
				"briefly poll instead",
				"anchored to the current message or thread",
				"owned by this agent",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("%s chat brief missing reminder decision boundary %q\n---\n%s", tc.name, want, out)
				}
			}
		})
	}
}

func TestRuntimeBriefMakesFreshCanonicalSessionExplicit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason string
		ctx    TaskContextForEnv
	}{
		{
			name:   "cutover issue wake",
			reason: "cutover",
			ctx:    TaskContextForEnv{IssueID: "issue-1"},
		},
		{
			name:   "reset chat wake",
			reason: "reset",
			ctx:    TaskContextForEnv{ChatSessionID: "chat-1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.ctx.FreshSessionNoticeReason = tc.reason
			out := buildMetaSkillContent("pi", tc.ctx)
			for _, want := range []string{
				"## Fresh Provider Session",
				"Your provider session is brand new.",
				"Historical sessions are archived read-only",
				"your workspace files remain",
				"Retrieve historical conclusions from issue comments or chat history when needed.",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("fresh-session brief missing %q\n---\n%s", want, out)
				}
			}
			if strings.Count(out, "## Fresh Provider Session") != 1 {
				t.Errorf("fresh-session notice must render exactly once\n---\n%s", out)
			}
		})
	}

	out := buildMetaSkillContent("pi", TaskContextForEnv{IssueID: "issue-1"})
	if strings.Contains(out, "## Fresh Provider Session") {
		t.Errorf("ordinary wake must not receive fresh-session notice\n---\n%s", out)
	}
}

func TestChatRuntimeBriefRendersReplyRequirementForDirectedRun(t *testing.T) {
	t.Parallel()
	ctx := TaskContextForEnv{
		ChatSessionID: "chat-1",
		ChannelID:     "channel-1",
		Directed:      true,
	}
	out := buildMetaSkillContent("codex", ctx)

	for _, want := range []string{
		"### Reply Requirement (READ FIRST",
		"Visible reply required for human DM/@mention/direct question/task/continuation.",
		"Agent channel @mention without an immediate deliverable/review/decision/direct answer → silence.",
		"short `multica message send` acknowledgment before substantive tools",
		"state understanding + next step",
		"Then work + separate result.",
		"Attention operation (follow/unfollow/mute/unmute): act first.",
		"--message-id <triggering-message-id> --emoji \"✅\"",
		"no text",
		"Unsure about a human → reply.",
		"Reply Requirement",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("directed brief missing must-reply block %q\n---\n%s", want, out)
		}
	}

	// Also verify the output section still mentions multica message send (CLI path).
	if !strings.Contains(out, "multica message send") {
		t.Errorf("directed brief should contain CLI send instruction")
	}
	for _, want := range []string{"multica reminder schedule", "durable self-wake", "--repeat RULE", "--message-id <id>", "does not infer one from task text", "weekly:days@HH:MM", "reminder schedule|list|snooze|update|cancel|log"} {
		if !strings.Contains(out, want) {
			t.Errorf("directed brief missing reminder capability %q", want)
		}
	}
}

func TestChatRuntimeBriefOmitsReplyRequirementForAmbientRun(t *testing.T) {
	t.Parallel()
	ctx := TaskContextForEnv{
		ChatSessionID: "chat-1",
		ChannelID:     "channel-1",
		Directed:      false, // ambient run
	}
	out := buildMetaSkillContent("codex", ctx)

	// Must NOT contain the Reply Requirement block.
	for _, banned := range []string{
		"Reply Requirement",
		"Work-before-feedback rule",
		"Human DMs, human @mentions, direct questions, assigned tasks",
		"Not responding is **not** an option",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("ambient brief should NOT contain must-reply section %q\n---\n%s", banned, out)
		}
	}

	// Ambient conversation delivery still carries the CLI instructions.
	for _, want := range []string{
		"## Delivery",
		"task-scoped Multica CLI transport",
		"multica message send",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("ambient brief missing expected section %q\n---\n%s", want, out)
		}
	}
}

func TestChatRuntimeBriefAlwaysAdvertisesTaskScopedCLITransport(t *testing.T) {
	t.Parallel()
	ctx := TaskContextForEnv{
		ChatSessionID:                    "chat-1",
		ChannelID:                        "channel-1",
		Directed:                         true,
		Repos:                            []RepoContextForEnv{{URL: "https://github.com/acme/app.git"}},
		AgentSkills:                      []SkillContextForEnv{{Name: "multica-stickers", Description: "Use for short social chat beats."}},
		RequestingUserName:               "Frank",
		RequestingUserProfileDescription: "Product owner",
	}
	out := buildMetaSkillContent("codex", ctx)

	for _, want := range []string{
		"## Delivery",
		"task-scoped Multica CLI transport",
		"multica message send",
		"multica message react",
		"--message \"short text\"",
		"multica message react --message-id",
		"multica message read",
		"multica message search",
		"--sticker",
		"Freshness holds:",
		"Message held by freshness check",
		"CLI also exits non-zero",
		"same turn",
		"`decisionCommands.revisedSend`",
		"`decisionCommands.sendDraft`",
		"choose not to send anything",
		"`abandoned` resolution",
		"multica reminder schedule",
		"multica-stickers",
		"Use for short social chat beats",
		"For visible chat replies, run `multica message send`",
		"After the command succeeds",
		"Issues/comments: `multica issue list|get|search|comment ...`",
		"issue list --mine --output json",
		"## Repositories",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("compat chat brief missing %q\n---\n%s", want, out)
		}
	}

	for _, banned := range []string{
		"This runtime has no chat CLI transport.",
		"No visible chat reply can be delivered without the task-scoped CLI transport.",
		"Do not try to find, install, or discuss chat send/react commands",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("chat brief should not contain removed transport-unavailable path %q\n---\n%s", banned, out)
		}
	}
}

func TestTargetlessRuntimeBriefUsesAutomaticCompletionDelivery(t *testing.T) {
	t.Parallel()
	ctx := TaskContextForEnv{
		ChatSessionID: "standalone-chat-1",
		Directed:      true,
	}
	out := buildMetaSkillContent("codex", ctx)

	const stickerEnvelope = `{"action":"message_send","parts":[{"type":"sticker","sticker_id":"hi"}]}`
	for _, want := range []string{
		"no `ChannelID`",
		"final assistant output, which the current session delivers automatically",
		"Do not run `multica message send` or `multica message react` for this reply",
		"search for a target, or invent one",
		stickerEnvelope,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("targetless brief missing %q\n---\n%s", want, out)
		}
	}

	for _, banned := range []string{
		"Visible chat output is delivered only by the task-scoped Multica CLI transport",
		"For visible chat replies, run `multica message send` or `multica message react`",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("targetless brief contains channel transport rule %q\n---\n%s", banned, out)
		}
	}
}

func TestIssueRuntimeBriefKeepsIssueWorkflowContract(t *testing.T) {
	t.Parallel()
	out := buildMetaSkillContent("claude", TaskContextForEnv{IssueID: "11111111-2222-3333-4444-555555555555"})
	for _, want := range []string{
		"## Pinned Rules",
		"All Multica platform I/O via `multica` CLI. No raw HTTP.",
		"## Available Commands",
		"multica issue comment add",
		"## Comment Formatting",
		"## Issue Metadata",
		"## Sub-issue Creation",
		"## Lazy References",
		"CLI details: inspect `multica ... --help`",
		"Final results MUST be delivered via `multica issue comment add`",
		"You are responsible for managing the issue status throughout your work",
		compactCloseoutStatusInstruction,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("issue brief missing %q", want)
		}
	}
	if strings.Contains(out, "## Mentions") {
		t.Errorf("issue brief still contains deleted mention-loop section")
	}
}

func TestChatBackedIssueUsesSemanticIssueWorkflow(t *testing.T) {
	t.Parallel()
	out := buildMetaSkillContent("pi", TaskContextForEnv{
		ChatSessionID: "resident-session-1",
		IssueID:       "11111111-2222-3333-4444-555555555555",
		ProjectID:     "project-1",
		ProjectTitle:  "Launch Project",
	})
	for _, want := range []string{
		"## Pinned Rules",
		"## Issue Metadata",
		"This issue belongs to **Launch Project**.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("chat-backed issue brief missing semantic issue content %q", want)
		}
	}
	if strings.Contains(out, "## Chat Mode") || strings.Contains(out, "**You are in chat mode.**") {
		t.Fatalf("chat-backed issue must not select a separate chat workflow\n---\n%s", out)
	}
}

func TestCloseoutStatusInstructionStaysCompact(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ctx  TaskContextForEnv
	}{
		{name: "issue", ctx: TaskContextForEnv{IssueID: "11111111-2222-3333-4444-555555555555"}},
		{name: "chat", ctx: TaskContextForEnv{ChatSessionID: "chat-1"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := buildMetaSkillContent("codex", tc.ctx)
			count := strings.Count(out, compactCloseoutStatusInstruction)
			if count != 1 {
				t.Fatalf("closeout status instruction count = %d, want 1\n---\n%s", count, out)
			}
			if strings.Contains(out, "## Closeout") || strings.Contains(out, "## Handoff") {
				t.Fatalf("closeout guidance must stay as one compact line, not a new prompt section\n---\n%s", out)
			}
		})
	}
}

func TestInstructionPrecedenceOnlyAppliesToAssignmentWorkflow(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ctx  TaskContextForEnv
	}{
		{
			name: "comment-triggered",
			ctx: TaskContextForEnv{
				IssueID:          "11111111-2222-3333-4444-555555555555",
				TriggerCommentID: "22222222-3333-4444-5555-666666666666",
			},
		},
		{
			name: "chat",
			ctx:  TaskContextForEnv{ChatSessionID: "chat-1"},
		},
		{
			name: "quick-create",
			ctx:  TaskContextForEnv{QuickCreatePrompt: "create me an issue"},
		},
		{
			name: "autopilot run-only",
			ctx:  TaskContextForEnv{AutopilotRunID: "run-1"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := buildMetaSkillContent("claude", tc.ctx)
			for _, banned := range []string{
				"## Instruction Precedence",
				"assignment workflow below",
				"Never treat this runtime workflow as permission to change issue status",
			} {
				if strings.Contains(out, banned) {
					t.Errorf("%s brief must not inherit assignment-only precedence text %q\n---\n%s", tc.name, banned, out)
				}
			}
		})
	}
}

// The sub-issue creation rule must reach top-level parents that have no
// `parent_issue_id` of their own — that is where the `todo` vs `backlog`
// decision matters most. The section must not gate on this issue being
// a child, and must not even mention `parent_issue_id`.
func TestSubIssueCreationSectionIsUnconditional(t *testing.T) {
	t.Parallel()
	ctx := TaskContextForEnv{
		IssueID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
	}
	out := buildMetaSkillContent("claude", ctx)

	const header = "## Sub-issue Creation"
	start := strings.Index(out, header)
	if start == -1 {
		t.Fatalf("sub-issue creation section missing")
	}
	rest := out[start:]
	end := strings.Index(rest[len(header):], "\n## ")
	var section string
	if end == -1 {
		section = rest
	} else {
		section = rest[:len(header)+end]
	}

	if strings.Contains(section, "parent_issue_id") {
		t.Errorf("Sub-issue Creation section must not reference `parent_issue_id` — it applies to any issue-bound run, including top-level parents:\n%s", section)
	}
}

// Workspace Context block: workspace.context (the per-workspace system prompt
// owners set in Settings → General) must reach the brief as `## Workspace
// Context` for every task kind so agents see a consistent shared system prompt
// regardless of how they were triggered. Empty content must skip the heading
// entirely — bare headings would just add noise.
func TestWorkspaceContextRenderedAcrossTaskKinds(t *testing.T) {
	t.Parallel()
	const wsContext = "All comments must be in English. Prefer concise PR descriptions."
	cases := []struct {
		name string
		ctx  TaskContextForEnv
	}{
		{
			name: "assignment-triggered",
			ctx: TaskContextForEnv{
				IssueID:          "11111111-2222-3333-4444-555555555555",
				WorkspaceContext: wsContext,
			},
		},
		{
			name: "comment-triggered",
			ctx: TaskContextForEnv{
				IssueID:          "22222222-3333-4444-5555-666666666666",
				TriggerCommentID: "33333333-4444-5555-6666-777777777777",
				WorkspaceContext: wsContext,
			},
		},
		{
			name: "chat",
			ctx: TaskContextForEnv{
				ChatSessionID:    "chat-1",
				WorkspaceContext: wsContext,
			},
		},
		{
			name: "quick-create",
			ctx: TaskContextForEnv{
				QuickCreatePrompt: "create me an issue",
				WorkspaceContext:  wsContext,
			},
		},
		{
			name: "autopilot run-only",
			ctx: TaskContextForEnv{
				AutopilotRunID:   "run-1",
				WorkspaceContext: wsContext,
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := buildMetaSkillContent("claude", tc.ctx)

			if !strings.Contains(out, "## Workspace Context") {
				t.Fatalf("[%s] expected `## Workspace Context` heading", tc.name)
			}
			if !strings.Contains(out, wsContext) {
				t.Errorf("[%s] brief missing workspace context body %q", tc.name, wsContext)
			}
			// The block must precede Available Commands so it acts as
			// background framing, not a footer hidden below CLI usage.
			ctxIdx := strings.Index(out, "## Workspace Context")
			cmdsIdx := strings.Index(out, "## Available Commands")
			if ctxIdx == -1 || cmdsIdx == -1 || ctxIdx > cmdsIdx {
				t.Errorf("[%s] `## Workspace Context` must appear above `## Available Commands` (ctx=%d, cmds=%d)", tc.name, ctxIdx, cmdsIdx)
			}
		})
	}
}

func TestPinnedRulesAndLazyReferencesRenderForChat(t *testing.T) {
	t.Parallel()
	out := buildMetaSkillContent("pi", TaskContextForEnv{
		ChatSessionID: "chat-1",
		ChannelID:     "channel-1",
		ProjectID:     "project-1",
		ProjectTitle:  "Demo Project",
		ProjectResources: []ProjectResourceForEnv{{
			ResourceType: "github_repo",
			ResourceRef:  []byte(`{"url":"https://github.com/LRM-Teams/multica","default_branch_hint":"dev"}`),
			Label:        "main app",
		}},
		AgentSkills: []SkillContextForEnv{{Name: "multica-stickers", Description: "Use for short social chat beats."}},
	})

	for _, want := range []string{
		"## Pinned Rules",
		"All Multica platform I/O via `multica` CLI. No raw HTTP.",
		"Treat the injected conversation context as scoped to the current DM, channel, or thread surface",
		"## Project Context",
		"Pinned project resources (full structured payload is in `.multica/project/resources.json`)",
		"default branch: `dev`",
		"## Skills",
		"## Lazy References",
		"Chat history: use `multica message read` or `multica message search`",
		"Project resources: read `.multica/project/resources.json`",
		"Skills: open the relevant `SKILL.md` only after its name/description matches the task",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("chat brief missing pinned/lazy guidance %q\n---\n%s", want, out)
		}
	}
}

func TestRenderProjectContextUsesTruthfulTaskKindWording(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ctx  TaskContextForEnv
		want string
	}{
		{name: "issue", ctx: TaskContextForEnv{IssueID: "issue-1"}, want: "This issue belongs to **Project A**."},
		{name: "chat", ctx: TaskContextForEnv{ChatSessionID: "chat-1"}, want: "This conversation is associated with **Project A**."},
		{name: "quick create", ctx: TaskContextForEnv{QuickCreatePrompt: "create"}, want: "The requested issue will be created in **Project A**."},
		{name: "autopilot", ctx: TaskContextForEnv{AutopilotRunID: "run-1"}, want: "This automation run is associated with **Project A**."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			tt.ctx.ProjectID = "project-1"
			tt.ctx.ProjectTitle = "Project A"
			renderProjectContext(&b, tt.ctx)
			if got := b.String(); !strings.Contains(got, tt.want) {
				t.Fatalf("project context missing %q:\n%s", tt.want, got)
			}
			if tt.name != "issue" && strings.Contains(b.String(), "This issue belongs") {
				t.Fatalf("non-issue context used issue wording:\n%s", b.String())
			}
		})
	}
}

func TestMulticaMemoryScopeRenderedForPiProvider(t *testing.T) {
	t.Parallel()
	ctx := TaskContextForEnv{
		ChatSessionID:       "chat-1",
		AgentRoot:           "/tmp/multica/workspace-1/.multica/agents/agent-1",
		AgentMemoryDir:      "/tmp/multica/workspace-1/.multica/agents/agent-1/memory",
		AgentSkillDir:       "/tmp/multica/workspace-1/.multica/agents/agent-1/skills",
		AgentSkillDraftsDir: "/tmp/multica/workspace-1/.multica/agents/agent-1/skills/drafts",
	}
	out := buildMetaSkillContent("pi", ctx)

	for _, want := range []string{
		"## Multica Agent Memory Scope",
		"Agent root (`MULTICA_AGENT_ROOT`): `/tmp/multica/workspace-1/.multica/agents/agent-1`",
		"Pi agent root (`PI_AGENT_ROOT`): `/tmp/multica/workspace-1/.multica/agents/agent-1`",
		"Memory root (`MULTICA_AGENT_MEMORY_DIR`): `/tmp/multica/workspace-1/.multica/agents/agent-1/memory`",
		"Pi memory root (`PI_MEMORY_DIR`): `/tmp/multica/workspace-1/.multica/agents/agent-1/memory`",
		"Skill root: `/tmp/multica/workspace-1/.multica/agents/agent-1/skills`",
		"Pi skill drafts root (`PI_SKILL_DRAFTS_DIR`): `/tmp/multica/workspace-1/.multica/agents/agent-1/skills/drafts`",
		"report these Multica agent paths, not host-global runtime paths",
		"Do not read or write `~/.pi/agent/memory`, `~/.codex/memories`",
		"### Harness boundary (kernel vs shell)",
		"Multica kernel (not swappable with the coding harness)",
		"Same-machine runtime switch",
		"Multica memory follows **Agent ID**",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Multica memory scope missing %q\n%s", want, out)
		}
	}

	scopeIdx := strings.Index(out, "## Multica Agent Memory Scope")
	cmdsIdx := strings.Index(out, "## Available Commands")
	if scopeIdx == -1 || cmdsIdx == -1 || scopeIdx > cmdsIdx {
		t.Errorf("Multica memory scope must appear above Available Commands (scope=%d, cmds=%d)", scopeIdx, cmdsIdx)
	}
}

func TestMemoryOperatingGuidePrioritizesExplicitUserPreferences(t *testing.T) {
	t.Parallel()
	ctx := TaskContextForEnv{
		ChatSessionID:    "chat-1",
		Directed:         true,
		AgentRoot:        "/tmp/multica/workspace-1/.multica/agents/agent-1",
		AgentMemoryDir:   "/tmp/multica/workspace-1/.multica/agents/agent-1/memory",
		UserMemoryDir:    "/tmp/multica/workspace-1/.multica/agents/agent-1/users/member-1",
		ProjectMemoryDir: "/tmp/multica/workspace-1/.multica/agents/agent-1/projects/project-1",
		ChannelMemoryDir: "/tmp/multica/workspace-1/.multica/agents/agent-1/channels/channel-1",
	}
	out := buildMetaSkillContent("codex", ctx)

	for _, want := range []string{
		"### Memory Operating Guide (v0.11)",
		"Entry gates (platform-enforced)",
		"group-chat wakes do not inject personal",
		"Use high-strength auto-write for human preferences and durable work arrangements",
		"treat **human speech and peer-agent durable statements** as high-signal by default",
		"A verbal acknowledgment such as \"got it\" / \"记住了\" does not count as remembering",
		"You judge the right destination",
		"current user's isolated `USER.md`",
		"likely to matter in a future run",
		"Write target map",
		"MULTICA_AGENT_MEMORY_DIR/daily/YYYY-MM-DD.md",
		"only the most important long-lived cross-project rules",
		"memory/MEMORY.md",
		"should follow this agent into unrelated DMs, channels, and projects",
		"If no project directory is present, do not create or infer one",
		"MULTICA_USER_MEMORY_DIR",
		"current member's durable preferences and standing working style",
		"Relationships, handoffs, and work arrangements",
		"RELATIONSHIP.md",
		"notes/relationship-map.md",
		"not only at welcome/introduction",
		"Pure social greetings",
		"Feedback from humans and peer agents",
		"Do not treat agent-to-agent talk as disposable chatter",
		"Peer-agent messages can be remembered",
		"Claiming \"记住了\"",
		"After solving a problem, remember it yourself",
		"do not wait for anyone to say \"记住\"",
		"Closing with only \"fixed\" and no durable write is wrong",
		"memory-signal.jsonl",
		"Daily journal (hot path) vs self-review/curator (cold path)",
		"peer-agent durable lessons",
		"reusable problem-resolution lessons still write immediately",
		"memory/daily/YYYY-MM-DD.md",
		"Do **not** run a full self-review or curator-style promotion inside every chat/task turn",
		"Only promote the most durable, broadly reusable facts out of Daily into `MEMORY.md`",
		"project-specific durable facts belong in project files, not agent-global memory",
		"must not block chat latency",
		"Source is not scope",
		"who said a memory is provenance",
		"do not discard a rule merely because a peer agent stated it",
		"agents addressed by the current message",
		"Do not add a workspace/shared candidate merely to fan the preference out",
		"memory/STATE.md",
		"status, TTL/expiry, or reset date",
		"memory/REVIEW.md",
		"MULTICA_AGENT_SYNC_QUEUE_DIR/memory-candidates.jsonl",
		"remember this",
		"defaults to agent-global `memory/MEMORY.md`",
		"Collective intent does not require the exact words \"all agents\"",
		"都给我记住",
		"separate inbox delivery for each eligible channel agent",
		"including offline runtimes",
		"do not create a workspace/shared candidate merely to redeliver",
		"agents beyond the current message recipients",
		"explicitly canonical workspace/team knowledge",
		"current task remain authoritative",
		"Do not record guesses",
		"never to copy private memory across agents directly",
		"never silently rewrite instructions",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("memory operating guide missing %q\n%s", want, out)
		}
	}
	for _, old := range []string{
		"agents that were absent can receive it",
		"collective wording tells the addressed agents or team",
	} {
		if strings.Contains(out, old) {
			t.Fatalf("memory guide retained obsolete collective fanout rule %q:\n%s", old, out)
		}
	}
}

func TestPromotedMemorySnapshotIncludesOnlyServerSelectedMemories(t *testing.T) {
	t.Parallel()
	out := buildMetaSkillContent("codex", TaskContextForEnv{
		InitiatorType: "member",
		InitiatorID:   "11111111-1111-1111-1111-111111111111",
		InitiatorName: "Frank",
		AgentMemories: []MemoryContextForEnv{{
			Name: "Feedback before work", Content: "Acknowledge before substantive tool work.",
			Scope: "user", SubjectType: "member", SubjectID: "11111111-1111-1111-1111-111111111111",
		}},
	})
	for _, want := range []string{
		"Stable member ID for preference attribution",
		"## Effective Promoted Memory Snapshot",
		"Feedback before work",
		"subject: `member:11111111-1111-1111-1111-111111111111`",
		"> Acknowledge before substantive tool work.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("promoted memory snapshot missing %q:\n%s", want, out)
		}
	}
}

func TestMemoryOperatingGuideRequiresAgentLocalScope(t *testing.T) {
	t.Parallel()
	withoutScope := buildMetaSkillContent("codex", TaskContextForEnv{ChatSessionID: "chat-1"})
	if strings.Contains(withoutScope, "Memory Operating Guide") {
		t.Fatalf("memory operating guide must not render without an agent-local root:\n%s", withoutScope)
	}

	withRoot := buildMetaSkillContent("codex", TaskContextForEnv{
		ChatSessionID: "chat-1",
		AgentRoot:     "/tmp/multica/workspace-1/.multica/agents/agent-1",
	})
	if !strings.Contains(withRoot, "### Memory Operating Guide (v0.11)") {
		t.Fatalf("memory operating guide missing when an agent-local root exists:\n%s", withRoot)
	}

	skillsOnly := buildMetaSkillContent("codex", TaskContextForEnv{
		ChatSessionID: "chat-1",
		AgentSkillDir: "/tmp/multica/workspace-1/.multica/agents/agent-1/skills",
	})
	if strings.Contains(skillsOnly, "Memory Operating Guide") {
		t.Fatalf("memory operating guide must not render for a skills-only scope:\n%s", skillsOnly)
	}
}

func TestMulticaMemoryScopeRenderedForNonPiProvider(t *testing.T) {
	t.Parallel()
	ctx := TaskContextForEnv{
		AgentRoot:      "/tmp/multica/workspace-1/.multica/agents/agent-1",
		AgentMemoryDir: "/tmp/multica/workspace-1/.multica/agents/agent-1/memory",
	}
	out := buildMetaSkillContent("codex", ctx)
	for _, want := range []string{
		"## Multica Agent Memory Scope",
		"Agent root (`MULTICA_AGENT_ROOT`): `/tmp/multica/workspace-1/.multica/agents/agent-1`",
		"Memory root (`MULTICA_AGENT_MEMORY_DIR`): `/tmp/multica/workspace-1/.multica/agents/agent-1/memory`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Codex memory scope missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "PI_MEMORY_DIR") {
		t.Errorf("non-Pi provider must not emit Pi-specific env names")
	}
}

func TestWorkspaceContextHeadingSkippedWhenEmpty(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ctx  TaskContextForEnv
	}{
		{
			name: "empty string",
			ctx: TaskContextForEnv{
				IssueID:          "11111111-2222-3333-4444-555555555555",
				WorkspaceContext: "",
			},
		},
		{
			name: "whitespace only",
			ctx: TaskContextForEnv{
				IssueID:          "11111111-2222-3333-4444-555555555555",
				WorkspaceContext: "   \n\t  \r\n",
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := buildMetaSkillContent("claude", tc.ctx)
			if strings.Contains(out, "## Workspace Context") {
				t.Errorf("[%s] empty workspace context must NOT emit the heading", tc.name)
			}
		})
	}
}

func TestMetaSkillDocumentsCanonicalHumanDMTransport(t *testing.T) {
	t.Parallel()
	out := buildMetaSkillContent("claude", TaskContextForEnv{ChatSessionID: "chat-1"})

	for _, want := range []string{
		"multica message send --target dm:@<human-handle> --message-stdin",
		"there is no recipient fallback",
		"Unknown or agent handles are rejected",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("runtime brief missing DM guidance %q", want)
		}
	}
	if strings.Contains(out, "multica dm") {
		t.Fatal("runtime brief must not advertise retired multica dm")
	}
}

func TestSubIssueCreationSectionSkippedForNonIssueModes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ctx  TaskContextForEnv
	}{
		{
			name: "chat",
			ctx:  TaskContextForEnv{ChatSessionID: "chat-1"},
		},
		{
			name: "quick-create",
			ctx:  TaskContextForEnv{QuickCreatePrompt: "create me an issue"},
		},
		{
			name: "autopilot run-only",
			ctx:  TaskContextForEnv{AutopilotRunID: "run-1"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := buildMetaSkillContent("claude", tc.ctx)
			if strings.Contains(out, "## Sub-issue Creation") {
				t.Errorf("%s mode must NOT emit the Sub-issue Creation section", tc.name)
			}
		})
	}
}

// writeRuntimeConfigFile is the safe replacement for the previous
// unconditional os.WriteFile of CLAUDE.md / AGENTS.md / GEMINI.md. The three
// states it must handle correctly are: file missing, file present without
// markers (user-authored content already there — the regression case from
// MUL-2753), and file present with markers (idempotent second-run replace).

func TestWriteRuntimeConfigFileCreatesMissingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	const brief = "# Multica Agent Runtime\n\nbrief body line"

	if err := writeRuntimeConfigFile(path, brief); err != nil {
		t.Fatalf("writeRuntimeConfigFile returned error: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back file: %v", err)
	}
	s := string(got)
	if !strings.HasPrefix(s, runtimeMarkerBegin+"\n") {
		t.Errorf("output should start with begin marker, got:\n%s", s)
	}
	if !strings.Contains(s, brief) {
		t.Errorf("output should contain brief body, got:\n%s", s)
	}
	if !strings.Contains(s, "\n"+runtimeMarkerEnd+"\n") {
		t.Errorf("output should contain end marker followed by newline, got:\n%s", s)
	}
}

func TestWriteRuntimeConfigFilePreservesUserContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	const userContent = "# User repo CLAUDE.md\n\n- rule one\n- rule two\n"
	if err := os.WriteFile(path, []byte(userContent), 0o644); err != nil {
		t.Fatalf("seed user file: %v", err)
	}

	const brief = "## Multica brief\n\ninjected body"
	if err := writeRuntimeConfigFile(path, brief); err != nil {
		t.Fatalf("writeRuntimeConfigFile returned error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back file: %v", err)
	}
	s := string(got)
	// The user's original content must be untouched and appear before the
	// injected marker block; this is the core regression case from MUL-2753.
	if !strings.HasPrefix(s, userContent) {
		t.Errorf("user content must be preserved verbatim at the top of the file, got:\n%s", s)
	}
	beginIdx := strings.Index(s, runtimeMarkerBegin)
	endIdx := strings.Index(s, runtimeMarkerEnd)
	if beginIdx < 0 || endIdx <= beginIdx {
		t.Fatalf("expected a well-formed marker block in:\n%s", s)
	}
	if beginIdx < len(userContent) {
		t.Errorf("begin marker must appear after user content, beginIdx=%d userLen=%d", beginIdx, len(userContent))
	}
	if !strings.Contains(s, brief) {
		t.Errorf("brief body missing from output:\n%s", s)
	}
}

func TestWriteRuntimeConfigFileReplacesExistingBlock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	const userBefore = "# User AGENTS.md\n\nuser line above\n"
	const userAfter = "\nuser line below the block\n"
	original := userBefore +
		runtimeMarkerBegin + "\n" +
		"OLD BRIEF CONTENT THAT MUST GO AWAY\n" +
		runtimeMarkerEnd + "\n" +
		userAfter
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const newBrief = "## New Multica brief\n\nfresh body"
	if err := writeRuntimeConfigFile(path, newBrief); err != nil {
		t.Fatalf("writeRuntimeConfigFile returned error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back file: %v", err)
	}
	s := string(got)
	if !strings.HasPrefix(s, userBefore) {
		t.Errorf("content above the marker block must be preserved, got:\n%s", s)
	}
	if !strings.HasSuffix(s, userAfter) {
		t.Errorf("content below the marker block must be preserved, got:\n%s", s)
	}
	if strings.Contains(s, "OLD BRIEF CONTENT THAT MUST GO AWAY") {
		t.Errorf("previous block body must be replaced, got:\n%s", s)
	}
	if !strings.Contains(s, newBrief) {
		t.Errorf("new brief body missing from output:\n%s", s)
	}
	if strings.Count(s, runtimeMarkerBegin) != 1 || strings.Count(s, runtimeMarkerEnd) != 1 {
		t.Errorf("there must be exactly one begin/end marker pair, got:\n%s", s)
	}
}

func TestWriteRuntimeConfigFileIsIdempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	const userContent = "# User CLAUDE.md\n\nimportant rules\n"
	if err := os.WriteFile(path, []byte(userContent), 0o644); err != nil {
		t.Fatalf("seed user file: %v", err)
	}

	const brief = "## Multica brief\n\nbody"
	for i := 0; i < 5; i++ {
		if err := writeRuntimeConfigFile(path, brief); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back file: %v", err)
	}
	s := string(got)
	if strings.Count(s, runtimeMarkerBegin) != 1 {
		t.Errorf("repeated runs must not duplicate the begin marker, count=%d, file:\n%s", strings.Count(s, runtimeMarkerBegin), s)
	}
	if strings.Count(s, runtimeMarkerEnd) != 1 {
		t.Errorf("repeated runs must not duplicate the end marker, count=%d, file:\n%s", strings.Count(s, runtimeMarkerEnd), s)
	}
	if strings.Count(s, brief) != 1 {
		t.Errorf("repeated runs must not duplicate the brief body, count=%d, file:\n%s", strings.Count(s, brief), s)
	}
	if !strings.HasPrefix(s, userContent) {
		t.Errorf("user content must remain intact at the top of the file, got:\n%s", s)
	}
}

// InjectRuntimeConfig is the production entry point — verify the marker
// semantics propagate through it for each provider's target filename.
func TestInjectRuntimeConfigPreservesUserContent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		provider string
		filename string
	}{
		{"claude", "CLAUDE.md"},
		{"codex", "AGENTS.md"},
		{"copilot", "AGENTS.md"},
		{"opencode", "AGENTS.md"},
		{"openclaw", "AGENTS.md"},
		{"hermes", "AGENTS.md"},
		{"pi", "AGENTS.md"},
		{"cursor", "AGENTS.md"},
		{"kimi", "AGENTS.md"},
		{"kiro", "AGENTS.md"},
		{"antigravity", "AGENTS.md"},
		{"grok", "AGENTS.md"},
		{"gemini", "GEMINI.md"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.provider, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, tc.filename)
			const userContent = "# User-authored file\n\ndon't touch this\n"
			if err := os.WriteFile(path, []byte(userContent), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}

			content, err := InjectRuntimeConfig(dir, tc.provider, TaskContextForEnv{
				IssueID: "11111111-2222-3333-4444-555555555555",
			})
			if err != nil {
				t.Fatalf("InjectRuntimeConfig: %v", err)
			}
			if content == "" {
				t.Fatalf("returned brief content must be non-empty")
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			s := string(got)
			if !strings.HasPrefix(s, userContent) {
				t.Errorf("[%s] user content must be preserved verbatim at the top of %s, got:\n%s", tc.provider, tc.filename, s)
			}
			if !strings.Contains(s, runtimeMarkerBegin) || !strings.Contains(s, runtimeMarkerEnd) {
				t.Errorf("[%s] %s must contain the runtime marker block, got:\n%s", tc.provider, tc.filename, s)
			}
		})
	}
}

func TestInjectRuntimeConfigUnknownProviderSkipsWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Seed all three candidate filenames so we can verify none of them get
	// written when the provider is unknown.
	for _, name := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("untouched\n"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	if _, err := InjectRuntimeConfig(dir, "totally-unknown-provider", TaskContextForEnv{
		IssueID: "11111111-2222-3333-4444-555555555555",
	}); err != nil {
		t.Fatalf("InjectRuntimeConfig: %v", err)
	}
	for _, name := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != "untouched\n" {
			t.Errorf("unknown provider must not write %s; got:\n%s", name, string(got))
		}
	}
}

// Parser hardening: the end marker must be found strictly after the begin
// marker so a stray end marker that appears earlier in user content (e.g.
// a documentation snippet showing what the wire format looks like) doesn't
// trick writeRuntimeConfigFile into thinking the file is malformed and
// appending another block on every run.
func TestWriteRuntimeConfigFileIgnoresStrayEndMarkerBeforeBegin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")

	// Seed a file whose user-authored portion documents the marker format
	// (so the *end* marker appears before any *begin* marker), then has a
	// real block authored by an earlier Multica run below.
	const userDoc = "# Repo CLAUDE.md\n\nExample of what Multica writes:\n" +
		runtimeMarkerEnd + "\n\n# Real config below\n"
	original := userDoc +
		runtimeMarkerBegin + "\nFIRST BRIEF\n" + runtimeMarkerEnd + "\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const newBrief = "SECOND BRIEF"
	if err := writeRuntimeConfigFile(path, newBrief); err != nil {
		t.Fatalf("writeRuntimeConfigFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	s := string(got)

	// The user's stray end marker line plus surrounding doc text must still
	// be present, and the file must contain exactly one begin marker and
	// one *additional* end marker (so two end markers total — the stray
	// one and the one closing our block).
	if !strings.Contains(s, userDoc) {
		t.Errorf("user doc with stray end marker must be preserved verbatim, got:\n%s", s)
	}
	if got, want := strings.Count(s, runtimeMarkerBegin), 1; got != want {
		t.Errorf("expected exactly %d begin markers, got %d:\n%s", want, got, s)
	}
	if got, want := strings.Count(s, runtimeMarkerEnd), 2; got != want {
		t.Errorf("expected exactly %d end markers (1 user stray + 1 closing our block), got %d:\n%s", want, got, s)
	}
	if strings.Contains(s, "FIRST BRIEF") {
		t.Errorf("previous brief body must be replaced, got:\n%s", s)
	}
	if !strings.Contains(s, newBrief) {
		t.Errorf("new brief body missing from output:\n%s", s)
	}

	// Idempotency under the stray-end pattern: a second write must not
	// stack another block.
	if err := writeRuntimeConfigFile(path, newBrief); err != nil {
		t.Fatalf("second writeRuntimeConfigFile: %v", err)
	}
	got2, _ := os.ReadFile(path)
	s2 := string(got2)
	if got, want := strings.Count(s2, runtimeMarkerBegin), 1; got != want {
		t.Errorf("repeat write must not grow begin markers, got %d, want %d:\n%s", got, want, s2)
	}
}

// Parser hardening: a file containing only a begin marker (e.g. a previous
// run that crashed mid-write) must not cause every subsequent run to stack
// another block beneath the half-block.
func TestWriteRuntimeConfigFileReplacesMalformedHalfBlock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")

	const userTop = "# Repo AGENTS.md\n\nrules above\n"
	const halfBlock = "leftover from crashed write\nsecond line\n"
	original := userTop + runtimeMarkerBegin + "\n" + halfBlock
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const newBrief = "recovered brief"
	if err := writeRuntimeConfigFile(path, newBrief); err != nil {
		t.Fatalf("writeRuntimeConfigFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	s := string(got)
	if !strings.HasPrefix(s, userTop) {
		t.Errorf("user content above the half-block must be preserved, got:\n%s", s)
	}
	if strings.Contains(s, "leftover from crashed write") {
		t.Errorf("half-block contents must be replaced, got:\n%s", s)
	}
	if got, want := strings.Count(s, runtimeMarkerBegin), 1; got != want {
		t.Errorf("expected exactly %d begin marker, got %d:\n%s", want, got, s)
	}
	if got, want := strings.Count(s, runtimeMarkerEnd), 1; got != want {
		t.Errorf("expected exactly %d end marker after recovery, got %d:\n%s", want, got, s)
	}
	if !strings.Contains(s, newBrief) {
		t.Errorf("new brief body missing from output:\n%s", s)
	}
}

// Cleanup excises the marker block, preserving every byte of surrounding
// user content. This is the local_directory invariant: a `claude` /
// `codex` run started by the user after a Multica task must see the same
// file the user wrote.
func TestCleanupRuntimeConfigPreservesUserContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")

	const userBefore = "# Repo CLAUDE.md\n\nuser line above\n"
	const userAfter = "\nuser line below the block\n"
	const userExpected = "# Repo CLAUDE.md\n\nuser line above\n\nuser line below the block\n"
	// Inject via the production write path so we exercise the actual
	// marker block format, not a hand-rolled approximation.
	if err := os.WriteFile(path, []byte(userBefore+userAfter), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := writeRuntimeConfigFile(path, "brief body"); err != nil {
		t.Fatalf("seed brief: %v", err)
	}

	if err := CleanupRuntimeConfig(dir, "claude"); err != nil {
		t.Fatalf("CleanupRuntimeConfig: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	s := string(got)
	if strings.Contains(s, runtimeMarkerBegin) || strings.Contains(s, runtimeMarkerEnd) {
		t.Errorf("marker block must be removed, got:\n%s", s)
	}
	if strings.Contains(s, "brief body") {
		t.Errorf("brief body must be removed, got:\n%s", s)
	}
	if s != userExpected {
		t.Errorf("user content must be preserved byte-for-byte\n got:\n%q\nwant:\n%q", s, userExpected)
	}
}

// Cleanup removes the file entirely when the marker block was the only
// content — i.e. we created the file from scratch in a directory that had
// no pre-existing CLAUDE.md / AGENTS.md / GEMINI.md.
func TestCleanupRuntimeConfigRemovesFileWhenOnlyBlockRemained(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")

	// No seed — writeRuntimeConfigFile creates the file with only the
	// marker block inside.
	if err := writeRuntimeConfigFile(path, "brief body"); err != nil {
		t.Fatalf("seed brief: %v", err)
	}

	if err := CleanupRuntimeConfig(dir, "claude"); err != nil {
		t.Fatalf("CleanupRuntimeConfig: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected file to be removed, stat err=%v", err)
	}
}

// Cleanup is a no-op when no marker block exists or when the file is
// missing — Cleanup is safe to call defensively from the daemon's defer.
func TestCleanupRuntimeConfigNoOpCases(t *testing.T) {
	t.Parallel()

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := CleanupRuntimeConfig(dir, "claude"); err != nil {
			t.Errorf("missing file must be no-op, got: %v", err)
		}
		// And the directory must remain untouched.
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("readdir: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("expected dir to remain empty, got: %v", entries)
		}
	})

	t.Run("file without marker block", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "CLAUDE.md")
		const userContent = "# Repo CLAUDE.md\n\nrules\n"
		if err := os.WriteFile(path, []byte(userContent), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if err := CleanupRuntimeConfig(dir, "claude"); err != nil {
			t.Errorf("no-marker-block file must be no-op, got: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if string(got) != userContent {
			t.Errorf("file must be untouched\n got:\n%q\nwant:\n%q", string(got), userContent)
		}
	})

	t.Run("unknown provider", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// Seed every candidate filename to verify none of them get touched.
		for _, name := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("untouched\n"), 0o644); err != nil {
				t.Fatalf("seed %s: %v", name, err)
			}
		}
		if err := CleanupRuntimeConfig(dir, "totally-unknown-provider"); err != nil {
			t.Errorf("unknown provider must be no-op, got: %v", err)
		}
		for _, name := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md"} {
			got, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			if string(got) != "untouched\n" {
				t.Errorf("unknown provider must not touch %s; got:\n%s", name, string(got))
			}
		}
	})
}

// Cleanup must handle a half-block left by a previous crashed run: begin
// marker present but no end. Otherwise the half-block would survive
// cleanup and pollute the next manual CLI invocation in the same dir.
func TestCleanupRuntimeConfigRemovesMalformedHalfBlock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")

	const userTop = "# Repo AGENTS.md\n\nrules\n"
	original := userTop + runtimeMarkerBegin + "\nhalf-written brief no end\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := CleanupRuntimeConfig(dir, "codex"); err != nil {
		t.Fatalf("CleanupRuntimeConfig: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	s := string(got)
	if strings.Contains(s, runtimeMarkerBegin) {
		t.Errorf("half-block begin marker must be excised, got:\n%s", s)
	}
	if strings.Contains(s, "half-written brief no end") {
		t.Errorf("half-block body must be excised, got:\n%s", s)
	}
	if !strings.HasPrefix(s, userTop) {
		t.Errorf("user content above the half-block must remain, got:\n%s", s)
	}
}

// Cleanup must remove the marker block for every provider's target file,
// using the same provider→filename mapping as InjectRuntimeConfig — so a
// new provider added to one side cannot drift past the other.
func TestCleanupRuntimeConfigByProvider(t *testing.T) {
	t.Parallel()
	cases := []struct {
		provider string
		filename string
	}{
		{"claude", "CLAUDE.md"},
		{"codex", "AGENTS.md"},
		{"copilot", "AGENTS.md"},
		{"opencode", "AGENTS.md"},
		{"openclaw", "AGENTS.md"},
		{"hermes", "AGENTS.md"},
		{"pi", "AGENTS.md"},
		{"cursor", "AGENTS.md"},
		{"kimi", "AGENTS.md"},
		{"kiro", "AGENTS.md"},
		{"antigravity", "AGENTS.md"},
		{"grok", "AGENTS.md"},
		{"gemini", "GEMINI.md"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.provider, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, tc.filename)
			const userContent = "# User file\n\ndon't touch this\n"
			if err := os.WriteFile(path, []byte(userContent), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}

			// Inject through the production path so cleanup runs against
			// the same wire format the agent saw.
			if _, err := InjectRuntimeConfig(dir, tc.provider, TaskContextForEnv{
				IssueID: "11111111-2222-3333-4444-555555555555",
			}); err != nil {
				t.Fatalf("InjectRuntimeConfig: %v", err)
			}
			if err := CleanupRuntimeConfig(dir, tc.provider); err != nil {
				t.Fatalf("CleanupRuntimeConfig: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			s := string(got)
			if strings.Contains(s, runtimeMarkerBegin) || strings.Contains(s, runtimeMarkerEnd) {
				t.Errorf("[%s] marker block must be removed from %s, got:\n%s", tc.provider, tc.filename, s)
			}
			if s != userContent {
				t.Errorf("[%s] user content in %s must be preserved byte-for-byte\n got:\n%q\nwant:\n%q", tc.provider, tc.filename, s, userContent)
			}
		})
	}
}

// Inject → Cleanup → manual edit → Inject must converge back to the
// pre-injection state on the next Cleanup. This is the end-to-end
// regression that locks in: the user's repo is byte-identical to what
// they had before the task, every task cycle.
func TestInjectThenCleanupRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	const userContent = "# User-authored CLAUDE.md\n\n- rule A\n- rule B\n"
	if err := os.WriteFile(path, []byte(userContent), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Two full inject→cleanup cycles — covers both the "first task on a
	// fresh user file" path and the "subsequent task hits a clean file
	// again" path.
	for i := 0; i < 2; i++ {
		if _, err := InjectRuntimeConfig(dir, "claude", TaskContextForEnv{
			IssueID: "11111111-2222-3333-4444-555555555555",
		}); err != nil {
			t.Fatalf("iter %d inject: %v", i, err)
		}
		if err := CleanupRuntimeConfig(dir, "claude"); err != nil {
			t.Fatalf("iter %d cleanup: %v", i, err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("iter %d read back: %v", i, err)
		}
		if string(got) != userContent {
			t.Errorf("iter %d: user file must be byte-identical to pre-injection state\n got:\n%q\nwant:\n%q", i, string(got), userContent)
		}
	}
}

// Byte-exact boundary coverage flagged in PR #3438 review (Elon): the
// previous cleanup used TrimRight + "\n" and TrimSpace-based file removal,
// which created a real diff in three boundary cases. The table walks each
// one through a full inject→cleanup cycle and asserts the file ends up
// byte-identical (or, for missing-file, that it stays missing).
func TestInjectThenCleanupRoundTripByteExactBoundaries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// seed describes the pre-inject filesystem state. When seedExists
		// is false the file is absent; when true the file is created with
		// seedContent (which may be empty / whitespace-only / arbitrary
		// bytes).
		seedExists  bool
		seedContent string
	}{
		{
			name:        "file missing — Inject creates, Cleanup removes",
			seedExists:  false,
			seedContent: "",
		},
		{
			name:        "pre-existing empty file (zero bytes)",
			seedExists:  true,
			seedContent: "",
		},
		{
			name:        "pre-existing whitespace-only file",
			seedExists:  true,
			seedContent: "   \n",
		},
		{
			name:        "no trailing newline",
			seedExists:  true,
			seedContent: "rules",
		},
		{
			name:        "one trailing newline (the common markdown shape)",
			seedExists:  true,
			seedContent: "# Rules\n\nbody\n",
		},
		{
			name:        "two trailing newlines",
			seedExists:  true,
			seedContent: "rules\n\n",
		},
		{
			name:        "many trailing newlines",
			seedExists:  true,
			seedContent: "rules\n\n\n\n",
		},
		{
			name:        "CRLF line endings",
			seedExists:  true,
			seedContent: "rule A\r\nrule B\r\n",
		},
		{
			name:        "no final newline AND embedded blank lines",
			seedExists:  true,
			seedContent: "para 1\n\npara 2\n\npara 3",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "CLAUDE.md")

			if tc.seedExists {
				if err := os.WriteFile(path, []byte(tc.seedContent), 0o644); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}

			// Two cycles to cover both "first inject hits user file" and
			// "subsequent inject hits a cleaned file" paths.
			for i := 0; i < 2; i++ {
				if _, err := InjectRuntimeConfig(dir, "claude", TaskContextForEnv{
					IssueID: "11111111-2222-3333-4444-555555555555",
				}); err != nil {
					t.Fatalf("iter %d inject: %v", i, err)
				}
				if err := CleanupRuntimeConfig(dir, "claude"); err != nil {
					t.Fatalf("iter %d cleanup: %v", i, err)
				}

				if !tc.seedExists {
					// Missing file must remain missing after the cycle so
					// the user's directory listing is also byte-identical
					// (no zero-byte stub left behind).
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Errorf("iter %d: file must remain missing, stat err=%v", i, err)
					}
					continue
				}
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("iter %d read back: %v", i, err)
				}
				if string(got) != tc.seedContent {
					t.Errorf("iter %d: file must be byte-identical to seed\n got:  %q\n want: %q", i, string(got), tc.seedContent)
				}
			}
		})
	}
}

// Idempotency across the byte-exact boundaries: when a second Inject runs
// against a file that already carries a marker block (the "replace in
// place" branch), the surrounding bytes must stay untouched and the
// subsequent Cleanup must still restore the user's original file
// byte-exactly. This guards against a regression where the replace path
// would re-normalise pre/post bytes the way the old cleanup did.
func TestInjectReplaceThenCleanupRestoresByteExact(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		seedContent string
	}{
		{name: "no trailing newline", seedContent: "rules"},
		{name: "two trailing newlines", seedContent: "rules\n\n"},
		{name: "empty file", seedContent: ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "CLAUDE.md")
			if err := os.WriteFile(path, []byte(tc.seedContent), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}

			// First inject — append path.
			if _, err := InjectRuntimeConfig(dir, "claude", TaskContextForEnv{
				IssueID: "11111111-2222-3333-4444-555555555555",
			}); err != nil {
				t.Fatalf("first inject: %v", err)
			}
			// Second inject — replace-in-place path.
			if _, err := InjectRuntimeConfig(dir, "claude", TaskContextForEnv{
				IssueID: "11111111-2222-3333-4444-555555555555",
			}); err != nil {
				t.Fatalf("second inject: %v", err)
			}
			if err := CleanupRuntimeConfig(dir, "claude"); err != nil {
				t.Fatalf("cleanup: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			if string(got) != tc.seedContent {
				t.Errorf("file must be byte-identical to seed after replace+cleanup\n got:  %q\n want: %q", string(got), tc.seedContent)
			}
		})
	}
}

// The fixed managed separator is the invariant that makes byte-exact
// cleanup possible. This test pins it: writeRuntimeConfigFile must
// produce exactly `<user-bytes><\n\n><marker-block>` for ANY non-empty
// or empty pre-existing file, with no trailing-newline normalisation.
func TestWriteRuntimeConfigFileAlwaysInsertsFixedManagedSeparator(t *testing.T) {
	t.Parallel()
	for _, seed := range []string{"", "rules", "rules\n", "rules\n\n", "rules\n\n\n\n"} {
		seed := seed
		t.Run(fmt.Sprintf("seed=%q", seed), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "CLAUDE.md")
			if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			if err := writeRuntimeConfigFile(path, "brief body"); err != nil {
				t.Fatalf("write: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			s := string(got)
			// The seed must appear verbatim at the start of the file —
			// no extra newline appended, no trailing newline trimmed.
			if !strings.HasPrefix(s, seed) {
				t.Errorf("seed bytes must survive verbatim at the start of the file\n got: %q\n seed: %q", s, seed)
			}
			// Immediately after the seed we must see the fixed managed
			// separator, then the begin marker.
			markerStart := len(seed) + len(runtimeManagedSeparator)
			if len(s) < markerStart+len(runtimeMarkerBegin) {
				t.Fatalf("file shorter than expected layout\n got: %q", s)
			}
			if got, want := s[len(seed):markerStart], runtimeManagedSeparator; got != want {
				t.Errorf("expected managed separator %q immediately after seed, got %q", want, got)
			}
			if got, want := s[markerStart:markerStart+len(runtimeMarkerBegin)], runtimeMarkerBegin; got != want {
				t.Errorf("expected begin marker after managed separator, got %q", got)
			}
		})
	}
}

func TestBuildMetaSkillContentDoesNotUseLegacyManagedRole(t *testing.T) {
	content := buildMetaSkillContent("codex", TaskContextForEnv{
		ChatSessionID: "chat-1",
		AgentName:     "ordinary-looking-name",
		ManagedRole:   "group_manager",
	})
	if strings.Contains(content, "**Group manager:") ||
		strings.Contains(content, "### Managed Group Manager Role") {
		t.Fatalf("legacy managed_role must not grant group manager duties: %q", content)
	}
}

func TestExecutionDisciplineBriefAppearsOnceOnEveryRunKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ctx  TaskContextForEnv
	}{
		{name: "assignment", ctx: TaskContextForEnv{IssueID: "issue-1"}},
		{name: "comment", ctx: TaskContextForEnv{IssueID: "issue-1", TriggerCommentID: "comment-1"}},
		{name: "chat", ctx: TaskContextForEnv{ChatSessionID: "chat-1"}},
		{name: "quick create", ctx: TaskContextForEnv{QuickCreatePrompt: "create an issue"}},
		{name: "autopilot", ctx: TaskContextForEnv{AutopilotRunID: "run-1"}},
	}
	rules := []string{
		`1. **传达就是传达。** 收到"转告/同步/提醒"类指令时直接执行，不做顺手调查、验证或扩展；只有对方明确要求核实才去查。`,
		`2. **多催合并一次回。** 同一事项的多条催促/追问，合并成一条回复；不逐条应答。`,
		`3. **讨论从哪来回哪去。** thread 里的讨论只回 thread，不在主频道复读同一内容。`,
		`4. **对 agent 只说增量。** 与其他 agent 往来只传新信息，不复述双方已知上下文；无新信息不回复。`,
		`5. **对人不贴原始输出。** 面向人的消息说结论和必要事实，不贴命令行、JSON 或工具原始输出。`,
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := buildMetaSkillContent("codex", tc.ctx)
			if got := strings.Count(out, executionDisciplineBrief); got != 1 {
				t.Fatalf("execution discipline block count = %d, want 1", got)
			}
			for _, rule := range rules {
				if got := strings.Count(out, rule); got != 1 {
					t.Errorf("rule count = %d, want 1 for %q", got, rule)
				}
			}
		})
	}
}

// Task #51 / engineering-principles.md #1178: a standalone chat bubble
// (ChatSessionID set, no ChannelID) and a real channel/DM transport
// (ChannelID set) must render through two independent, non-branching
// functions with no fallback between them. These two tests pin each path's
// own delivery marker and assert the other path's marker is absent — a red
// reverse assertion is exactly the failure mode Frank flagged (a fallback or
// re-merged branch would leak the wrong contract into the wrong path).
func TestStandaloneChatRuntimeBriefNeverLeaksChannelCLITransportContract(t *testing.T) {
	t.Parallel()
	out := buildMetaSkillContent("codex", TaskContextForEnv{
		ChatSessionID: "standalone-chat-1",
		Directed:      true,
	})

	for _, want := range []string{
		"No `ChannelID` target: final assistant output is delivered to the current session.",
		"This run has no `ChannelID`. A visible final response is required through final assistant output",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("standalone brief missing its own delivery marker %q\n---\n%s", want, out)
		}
	}

	for _, banned := range []string{
		"`ChannelID` target present: visible output is delivered only by the task-scoped Multica CLI transport",
		"Visible reply required for human DM/@mention/direct question/task/continuation.",
		"For visible chat replies, run `multica message send` or `multica message react`. After the command succeeds",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("standalone brief leaked channel-transport contract %q\n---\n%s", banned, out)
		}
	}
}

func TestChannelChatRuntimeBriefNeverLeaksStandaloneFinalOutputContract(t *testing.T) {
	t.Parallel()
	out := buildMetaSkillContent("codex", TaskContextForEnv{
		ChatSessionID: "chat-1",
		ChannelID:     "channel-1",
		Directed:      true,
	})

	for _, want := range []string{
		"`ChannelID` target present: visible output is delivered only by the task-scoped Multica CLI transport",
		"Visible reply required for human DM/@mention/direct question/task/continuation.",
		"For visible chat replies, run `multica message send` or `multica message react`. After the command succeeds",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("channel brief missing its own delivery marker %q\n---\n%s", want, out)
		}
	}

	for _, banned := range []string{
		"No `ChannelID` target: final assistant output is delivered to the current session.",
		"This run has no `ChannelID`. A visible final response is required through final assistant output",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("channel brief leaked standalone final-output contract %q\n---\n%s", banned, out)
		}
	}
}
