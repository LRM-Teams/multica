package daemon

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/promptcontext"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// BuildPrompt constructs the task prompt for an agent CLI.
// Keep this minimal — detailed instructions live in CLAUDE.md / AGENTS.md
// injected by execenv.InjectRuntimeConfig. The provider string is threaded
// through to comment-triggered tasks' per-turn reply template; that template
// is provider-agnostic now (Linux/macOS → quoted-HEREDOC stdin, Windows →
// file) because the shell-layer corruption it guards against is not specific
// to any one provider (MUL-2904).
func BuildPrompt(task Task, provider string, agentRoot string) string {
	if profile, err := taskExecutionProfile(task); err == nil {
		switch profile {
		case executionProfileProtocolTurn:
			return buildProtocolTurnPrompt(task)
		}
	}
	withCurrentStateOverlay := func(prompt string) string {
		return currentStateOverlay(task) + prompt
	}
	if task.InboxEvent != nil && task.InboxEvent.Reason == protocol.ChannelRoleChangedReason {
		return withCurrentStateOverlay(fmt.Sprintf(
			"Your channel manager role changed for channel %s. The state above (server-claimed, this wake) is authoritative: if it lists this channel, follow those duties and manage only this channel; if it does not, you are no longer that channel's manager.",
			task.ChannelID,
		))
	}
	// Transport/session identity does not select the work semantics. A
	// chat-backed task that carries an issue must receive the issue prompt while
	// retaining ChatSessionID for the resident backend and delivery transport.
	if task.IssueID != "" {
		if task.TriggerCommentID != "" {
			return withCurrentStateOverlay(buildCommentPrompt(task, provider))
		}
		if task.AssignmentSnapshot != nil {
			return withCurrentStateOverlay(buildAssignmentPrompt(task))
		}
	} else if isChatLikeTask(task) {
		if provider == agent.ProviderPi {
			if command, ok := piNativeSlashChatCommand(task.ChatMessage); ok {
				// Native slash commands are handled by Pi's command router rather
				// than a provider conversation turn. Prefixing them would make the
				// command unparseable, and they do not consume a resumed session.
				return command
			}
		}
		return withCurrentStateOverlay(buildChatPrompt(task, agentRoot))
	}
	if task.TriggerCommentID != "" {
		return withCurrentStateOverlay(buildCommentPrompt(task, provider))
	}
	if task.AutopilotRunID != "" {
		return withCurrentStateOverlay(buildAutopilotPrompt(task))
	}
	if task.QuickCreatePrompt != "" {
		return withCurrentStateOverlay(buildQuickCreatePrompt(task))
	}
	if task.AssignmentSnapshot != nil {
		return withCurrentStateOverlay(buildAssignmentPrompt(task))
	}
	var b strings.Builder
	b.WriteString("You are running as a local coding agent for a Multica workspace.\n\n")
	fmt.Fprintf(&b, "Your assigned issue ID is: %s\n\n", task.IssueID)
	fmt.Fprintf(&b, "Start by running `multica issue get %s --output json` to understand your task, then complete it.\n", task.IssueID)
	fmt.Fprintf(&b, "For comment history, follow the rule in your runtime workflow file (assignment-triggered tasks treat the read as mandatory). `multica issue comment list %s --output json` returns all comments for the issue (server caps at 2000). On long-running issues use `--recent 20 --output json` to read the 20 most recently active threads, then page older threads via the stderr `Next thread cursor: ...` line and the matching `--before` / `--before-id` until you have enough history. `--since <RFC3339>` is still available for incremental polling and may combine with `--recent`.\n", task.IssueID)
	return withCurrentStateOverlay(b.String())
}

// currentStateSlots are the server-claimed per-wake facts injected ahead of
// every real provider turn. They exist because resident/resumed sessions
// don't naturally pick up changes after session start: the create-time AGENTS
// brief is startup-only (see execenv.buildMetaSkillContent), so a promotion,
// demotion, or config change would otherwise go unseen until the session
// restarts. Slots are additive — a future permission/config slot attaches the
// same way — but only the manager-role slot is implemented today.
var currentStateSlots = []func(Task) string{
	managerRoleStateSlot,
	channelGoalStateSlot,
}

func currentStateOverlay(task Task) string {
	var b strings.Builder
	for _, slot := range currentStateSlots {
		b.WriteString(slot(task))
	}
	return b.String()
}

// managerRoleStateSlot is the sole source of manager-channel truth: the
// startup brief no longer renders it. A missing Agent means an old server
// sent no authority snapshot, so existing (no-overlay) behavior is preserved
// rather than inventing a negative role.
func managerRoleStateSlot(task Task) string {
	if task.Agent == nil {
		return ""
	}
	channels := task.Agent.ManagerChannels
	if len(channels) == 0 {
		return "Group manager this wake (server-claimed): none. Do no stale channel-management work from an older session or brief. Existing self-owned Reminders remain ordinary Agent Reminders; role removal does not cancel or mutate them. If one wakes, this current role list is authoritative.\n\n"
	}

	refs := make([]string, 0, len(channels))
	for _, channel := range channels {
		// %q keeps a user-controlled channel name as quoted data, not instructions.
		refs = append(refs, fmt.Sprintf("id=%q name=%q", channel.ID, channel.Name))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Group manager this wake (server-claimed): %s. Ignore any other session/brief for roles not listed.\n", strings.Join(refs, ", "))
	b.WriteString("Per channel: close open loops — unanswered questions · unclaimed `todo` · stale `in_progress`/`in_review` · someone blocked on one owner. Nudge in-channel, not DM.\n")
	b.WriteString("Adaptive Goal Mode: do not create a Goal for greetings, questions, or one-step work. When a human clearly asks you to lead sustained multi-step or multi-agent delivery, use `multica goal get --channel <id>` and create a durable Goal only if none exists; when authorization or completion standards are ambiguous, propose the Goal in-channel instead of silently activating it. You own decomposition, coordination, revision, and evidence-based completion; executors only checkpoint progress.\n")
	b.WriteString("You may use your ordinary self-owned Reminder capability only when you judge a later follow-up is useful; the role does not own, require, or auto-schedule a Reminder. Before acting on any Reminder, re-check this current role list.\n\n")
	return b.String()
}

func channelGoalStateSlot(task Task) string {
	goal := task.ChannelGoal
	if goal == nil || strings.TrimSpace(task.ChannelID) == "" {
		return ""
	}
	isManager := agentManagesChannel(task.Agent, task.ChannelID)
	completed := make(map[string]struct{}, len(goal.CompletedCriteria))
	for _, criterion := range goal.CompletedCriteria {
		completed[criterion] = struct{}{}
	}
	var b strings.Builder
	b.WriteString("Current channel goal this wake (server-claimed, authoritative):\n")
	fmt.Fprintf(&b, "- Goal ID: %s\n", goal.ID)
	fmt.Fprintf(&b, "- Title: %s\n", goal.Title)
	fmt.Fprintf(&b, "- Objective: %s\n", goal.Objective)
	fmt.Fprintf(&b, "- Goal version: %d\n", goal.Version)
	b.WriteString("- Success criteria:\n")
	for _, criterion := range goal.SuccessCriteria {
		mark := " "
		if _, ok := completed[criterion]; ok {
			mark = "x"
		}
		fmt.Fprintf(&b, "  - [%s] %s\n", mark, criterion)
	}
	if strings.TrimSpace(goal.ProgressSummary) != "" {
		fmt.Fprintf(&b, "- Progress: %s\n", goal.ProgressSummary)
	}
	if strings.TrimSpace(goal.CurrentStep) != "" {
		fmt.Fprintf(&b, "- Current step: %s\n", goal.CurrentStep)
	}
	if strings.TrimSpace(goal.Blocker) != "" {
		fmt.Fprintf(&b, "- Blocker: %s\n", goal.Blocker)
	}
	if goal.WorkGraph != nil {
		fmt.Fprintf(&b, "- Work Graph: %s version=%d status=%s completed=%d running=%d waiting=%d stale=%d\n", goal.WorkGraph.ID, goal.WorkGraph.Version, goal.WorkGraph.Status, goal.WorkGraph.Completed, goal.WorkGraph.Running, goal.WorkGraph.Waiting, goal.WorkGraph.Stale)
		b.WriteString("Treat this graph delta as current server state. Do not redo completed child work; act only on newly ready, failed, stale, blocked, or gated work assigned to you.\n")
	}
	if c := goal.Coordination; c != nil {
		fmt.Fprintf(&b, "- Delivery control plane: admission=%s agents=%d project=%q git=%t channel_issues=%d linked_project_issues=%d project_issues=%d open=%d in_review=%d subgoals=%d open_subgoals=%d\n",
			c.ExecutionAdmission, c.AgentMemberCount, c.ProjectID, c.GitRepositoryBound,
			c.ChannelIssueTotal, c.ChannelProjectIssueTotal, c.ProjectIssueTotal, c.OpenProjectIssueTotal,
			c.InReviewProjectIssueTotal, c.SubgoalTotal, c.OpenSubgoalTotal)
		if c.AgentMemberCount > 1 || c.ExecutionAdmission == "unavailable" {
			if strings.TrimSpace(task.IssueID) == "" {
				b.WriteString("EXECUTION GATE: this multi-agent Goal is not a code assignment. Do not edit shared project files, create a code branch or commit, push, open/merge a PR, or deploy from this chat task. Only durable control-plane setup and status/review coordination are admitted until this agent is claimed on a channel-linked Issue in the bound Project.\n")
				if isManager {
					b.WriteString("As group manager, establish the delivery chain in order: run `multica goal bootstrap --channel <id> --project-title <title> --repository-url <url>` to create/bind one Project and its canonical github_repo; create a channel-linked parent Issue in that Project; decompose non-overlapping child Issues for parallel agents; create one manager-owned integration/release Issue and set metadata `delivery_role=integration`; require implementers to submit in_review and an independent reviewer or human to approve before done. Never assign the same deliverable to two agents.\n")
				} else {
					b.WriteString("You have no server-owned code deliverable this wake. You may analyze and propose a bounded Issue to the group manager, then wait for assignment; do not start an independent implementation.\n")
				}
			} else if c.ProjectID == "" || strings.TrimSpace(task.ProjectID) == "" || task.ProjectID != c.ProjectID {
				b.WriteString("EXECUTION GATE: this Issue claim is not aligned with the Goal's bound Project. Do not mutate the repository; ask the group manager to correct the Project/Issue binding.\n")
			} else if isManager && goalIssueDeliveryRole(task) == "integration" {
				b.WriteString("Execution admitted on the canonical integration/release Issue. Do not author a competing feature implementation. Integrate only independently reviewed Issue branches, require green CI, deploy the resulting canonical commit, and verify the deployed artifact against every Goal criterion before recording evidence.\n")
			} else {
				b.WriteString("Execution admitted only for this claimed implementation Issue. Acquire the Issue's executor work lease, use its canonical non-main branch and bounded paths, and stop at in_review; never merge, deploy, or mark your own implementation done. A separate manager-owned Issue with metadata `delivery_role=integration` owns canonical merge and release.\n")
			}
			switch c.ExecutionAdmission {
			case "project_required":
				b.WriteString("Control-plane blocker: the channel has no bound Project.\n")
			case "git_required":
				b.WriteString("Control-plane blocker: the bound Project has no github_repo resource.\n")
			case "issues_required":
				b.WriteString("Control-plane blocker: no channel-linked Issue belongs to the bound Project.\n")
			case "acceptance_required":
				b.WriteString("All Project Issues are terminal. Reconcile review/CI/deployment evidence against every Goal criterion; complete the Goal only if the shipped version satisfies it, otherwise open corrective Issues.\n")
			case "unavailable":
				b.WriteString("Control-plane blocker: the server could not verify Project/Git/Issue ownership. Fail closed and retry after the control plane is readable.\n")
			}
			if isManager && c.OpenProjectIssueTotal > 0 {
				b.WriteString("Long-running manager duty: ensure exactly one self-owned recurring Goal follow-up Reminder exists (list active reminders first; schedule with `multica reminder schedule --title \"Goal follow-up: <goal-id>\" --repeat every:15m --msg-id <current-source-message-id>` only when absent). On each wake, inspect channel-linked Project Issues, unblock or reassign stale work, dispatch newly-ready parallel Issues, drive in_review to independent review, and checkpoint the Goal. Cancel the Reminder when the Goal becomes terminal.\n")
			}
			b.WriteString("Long-running delivery is durable across turns: use parallel Issue runs, Issue comments/status, Goal checkpoints, and Reminder wakes. Do not keep one chat turn alive as the scheduler and do not redo terminal Issue work after resume.\n")
		}
	}
	if isManager {
		fmt.Fprintf(&b, "Manager process document: before changing the long-form plan, inspect your current document with `multica goal process list --channel %s --output json`. After meaningful planning, delegation, review, milestone, scope, or blocker changes, preserve useful prior context and create or update your own document with `multica goal process put --channel %s --expected-version <current-process-version-or-0> --content-file <path> --output json` (use 0 only when no document exists). Do not write a no-change placeholder. The process document and the authoritative short Goal checkpoint are separate; when both changed, update both.\n", task.ChannelID, task.ChannelID)
	}
	b.WriteString("Advance only the work requested in this turn toward the goal. Preserve the objective and success standard; do not revise or lower the parent goal.\n")
	fmt.Fprintf(&b, "After concrete progress, checkpoint it with `multica goal checkpoint --channel %s --expected-version %d --progress \"...\" --current-step \"...\"` plus repeatable `--evidence`, `--completed-criterion`, or `--blocker` flags as needed. If the command reports a stale version, run `multica goal get --channel %s` and reconcile before retrying.\n", task.ChannelID, goal.Version, task.ChannelID)
	if len(goal.Subgoals) > 0 {
		b.WriteString("Your bounded sub-goals this wake (server-claimed; not other agents' full threads):\n")
		for _, sg := range goal.Subgoals {
			fmt.Fprintf(&b, "- [%s] %s (id=%s role=%s version=%d)\n", sg.Status, sg.Title, sg.ID, sg.OwnRole, sg.Version)
			fmt.Fprintf(&b, "  purpose: %s\n", sg.Purpose)
			if strings.TrimSpace(sg.CompletionBoundary) != "" {
				fmt.Fprintf(&b, "  completion boundary: %s\n", sg.CompletionBoundary)
			}
			if strings.TrimSpace(sg.WaitingOnKind) != "" {
				fmt.Fprintf(&b, "  waiting_on: %s %s\n", sg.WaitingOnKind, sg.WaitingOnNote)
			}
			for _, line := range sg.ActivityDelta {
				fmt.Fprintf(&b, "  activity: %s\n", line)
			}
		}
		b.WriteString("Do not inject or invent other agents' private dialogue. Resolve/complete a sub-goal without cascading unrelated Issues.\n")
	}
	b.WriteString("\n")
	return b.String()
}

func agentManagesChannel(agent *AgentData, channelID string) bool {
	if agent == nil {
		return false
	}
	for _, channel := range agent.ManagerChannels {
		if channel.ID == channelID {
			return true
		}
	}
	return false
}

func goalIssueDeliveryRole(task Task) string {
	if task.AssignmentSnapshot == nil || task.AssignmentSnapshot.Metadata == nil {
		return ""
	}
	role, _ := task.AssignmentSnapshot.Metadata["delivery_role"].(string)
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "release" {
		return "integration"
	}
	return role
}

func buildAssignmentPrompt(task Task) string {
	snapshot := task.AssignmentSnapshot
	if snapshot == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("You are running as a local coding agent for a Multica workspace.\n\n")
	fmt.Fprintf(&b, "Your assigned issue ID is: %s\n\n", task.IssueID)
	b.WriteString("Current issue state at claim:\n")
	fmt.Fprintf(&b, "- Status: %s\n\n", snapshot.Status)
	b.WriteString("Assignment-time issue snapshot:\n")
	fmt.Fprintf(&b, "- Title: %s\n", snapshot.Title)
	fmt.Fprintf(&b, "- Comment count: %d\n", snapshot.CommentCount)
	b.WriteString("- Description:\n")
	if snapshot.Description == nil || strings.TrimSpace(*snapshot.Description) == "" {
		b.WriteString("  (none)\n")
	} else {
		b.WriteString(strings.TrimSpace(*snapshot.Description))
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "- Acceptance criteria (%d):\n", len(snapshot.AcceptanceCriteria))
	if len(snapshot.AcceptanceCriteria) == 0 {
		b.WriteString("  (none)\n")
	} else {
		for _, criterion := range snapshot.AcceptanceCriteria {
			fmt.Fprintf(&b, "  - %s\n", criterion)
		}
	}
	metadata, err := json.Marshal(snapshot.Metadata)
	if err != nil {
		metadata = []byte("{}")
	}
	fmt.Fprintf(&b, "- Metadata: %s\n\n", metadata)
	b.WriteString("The title, description, acceptance criteria, metadata, and comment count were captured when the assignment wake was enqueued. The status above is current at claim time.\n")
	if snapshot.IsTerminal() {
		fmt.Fprintf(&b, "The issue is already %s. Treat this as a stale assignment wake: do not reopen it, do not start issue work, and stop after reporting the terminal state.\n", snapshot.Status)
		return b.String()
	}
	b.WriteString("Use this as the starting issue context; do not run `multica issue get` or `multica issue metadata list` merely to rediscover these fields.\n")
	if snapshot.CommentCount > 0 {
		fmt.Fprintf(&b, "Comment bodies are intentionally not copied into the snapshot. Read them through the existing cursor flow: `multica issue comment list %s --output json`; for a long issue use `--recent 20 --output json` and follow the `Next thread cursor:` with `--before` / `--before-id` until you have enough history.\n", task.IssueID)
	} else {
		b.WriteString("No comments existed when this assignment wake was enqueued. Do not run `multica issue comment list` merely to confirm the zero count.\n")
	}
	b.WriteString("\nCurrent-turn execution contract:\n")
	fmt.Fprintf(&b, "- Unless your Agent Identity forbids status changes, set `%s` to `in_progress` before substantive work.\n", task.IssueID)
	b.WriteString("- Default to direct execution. If the work has independently acceptable units or needs isolated workers, first open the `multica-working-on-issues` skill and follow its current DIRECT / Issue DAG / Goal Graph boundary; do not reconstruct graph rules from an old session.\n")
	b.WriteString("- Complete the acceptance criteria and verify proportionately to the change. Run the relevant build, tests, or behavior check; visual comparison is required only for UI or visual acceptance criteria.\n")
	fmt.Fprintf(&b, "- Deliver the outcome with `multica issue comment add %s` using `--content-stdin` with a quoted heredoc or a UTF-8 `--content-file`; never inline generated comment prose in the shell. Final assistant output is not the Issue reply.\n", task.IssueID)
	fmt.Fprintf(&b, "- When complete, set `%s` to `in_review` unless status changes are forbidden. If genuinely blocked, set it to `blocked` and comment with the concrete blocker and required next action.\n", task.IssueID)
	return b.String()
}

// isChatLikeTask reports conversational wakes that must not fall through to the
// empty-issue assignment prompt. After LRM-1079/1081, ordinary channel/DM wakes
// carry ChatMessage (and usually ChannelID). Single-track: ChatMessage/attachments
// only — never ChatSessionID (retired; ignore on wire).
func isChatLikeTask(task Task) bool {
	if strings.TrimSpace(task.IssueID) != "" {
		return false
	}
	if strings.TrimSpace(task.ChatMessage) != "" {
		return true
	}
	return len(task.ChatMessageAttachments) > 0
}

func buildProtocolTurnPrompt(task Task) string {
	var b strings.Builder
	b.WriteString("Execute one bounded collaboration protocol turn from the supplied state. Do not call tools and do not perform unrelated work.\n\n")
	if strings.TrimSpace(task.ChatContextSummary) != "" {
		b.WriteString("Protocol state:\n")
		b.WriteString(strings.TrimSpace(task.ChatContextSummary))
		b.WriteString("\n\n")
	}
	b.WriteString("Turn instruction:\n")
	b.WriteString(strings.TrimSpace(task.ChatMessage))
	b.WriteString("\n\nReturn exactly one non-empty JSON object matching the schema requested by the turn instruction, with no commentary or trailing text.")
	return b.String()
}

// piNativeSlashChatCommand returns the raw Pi command when a direct chat
// message is intended for Pi's native slash-command router. Other runtimes
// keep the normal Multica chat wrapper so their command semantics are not
// affected.
func piNativeSlashChatCommand(message string) (string, bool) {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/") {
		return "", false
	}
	nameEnd := len(trimmed)
	for i, r := range trimmed[1:] {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			nameEnd = i + 1
			break
		}
	}
	name := strings.TrimPrefix(trimmed[:nameEnd], "/")
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[:i]
	}
	switch name {
	case "goal", "autogoal", "pet", "memory-review", "memory-skill", "memory-sync-upload", "memory-sync-pull", "memory-curator-enable", "memory-curator-disable", "memory-curator-status", "memory-curator-manager-scan", "memory-curator-manager-enable", "memory-curator-manager-disable", "memory-curator-manager-status", "memory-version-status", "memory-version-snapshot", "memory-version-list", "memory-version-restore", "memory-version-push":
		return trimmed, true
	default:
		return "", false
	}
}

// buildQuickCreatePrompt constructs a prompt for quick-create tasks. The
// user typed a single natural-language sentence in the create-issue modal;
// the agent's job is to translate it into one `multica issue create` CLI
// invocation, using its judgment to decide whether fetching referenced URLs
// would produce a better issue. No issue exists yet, so the agent must NOT
// call `multica issue get` or attempt to comment — there's nothing to read
// or reply to.
func buildQuickCreatePrompt(task Task) string {
	var b strings.Builder
	b.WriteString("You are running as a quick-create assistant for a Multica workspace.\n\n")
	b.WriteString("A user captured the following input via the quick-create modal. There is NO existing issue. Your job is to create a well-formed issue from this input with a single `multica issue create` command.\n\n")
	fmt.Fprintf(&b, "User input:\n> %s\n\n", task.QuickCreatePrompt)
	if task.QuickCreateSource != nil {
		b.WriteString("Source chat context:\n")
		b.WriteString(formatQuickCreateSourceContext(task.QuickCreateSource))
		b.WriteString("\n\n")
	}

	b.WriteString("Field rules:\n\n")

	// title
	b.WriteString("- **title**: required. A concise but semantically rich summary. If the input references external resources (PRs, issues, URLs), use your judgment on whether fetching the resource would produce a meaningfully better title — e.g. \"review PR #123\" → \"Review PR #123: Refactor auth module to OAuth2\". Strip filler words but preserve key semantic information.\n\n")

	// description — the core optimization
	b.WriteString("- **description**: The description is the executing agent's primary context. Aim for high fidelity — they should grasp the user's intent as if they had read the raw input themselves. Use this section structure:\n\n")
	b.WriteString("  1. **User request** — Faithfully restate what the user wants in their own words. Preserve specific names, identifiers, file paths, code snippets, and technical terms verbatim. Strip non-spec material before writing it (this is removal, not paraphrasing): verbal routing wrappers about creating the issue or routing it (e.g. \"create an issue\", \"assign it to X\", \"have @X handle it\") and pure conversational fillers (e.g. \"right?\"). When in doubt, keep it.\n\n")
	b.WriteString("     CC exception: `multica issue create` has no `--subscriber` flag, and the platform auto-subscribes members whose `[@Name](mention://member/<uuid>)` link appears in the description. When the user wrote \"cc @Y\", strip the verbal \"cc\" wrapper from the User request body and append a final `CC: <mention link(s)>` line to the description so the cc routing still fires.\n\n")
	b.WriteString("  2. **Context** — include ONLY when the input cited external resources AND you successfully fetched them AND they produced verifiable facts worth recording. Summarize facts only (e.g. \"PR #45 changes auth to JWT\"), not interpretation or unsolicited reference implementations. If you have nothing factual to add, omit the section entirely — never use it as an apology log for resources you could not fetch.\n\n")
	b.WriteString("  3. **Source chat context** — include ONLY when a `Source chat context` block is present in this prompt. Copy the source surface, thread root message ID, source message ID, source quote/excerpt, attachment IDs, and bounded visible summary into this section so the created issue can be audited back to the chat/DM/thread that spawned it. Do not add internal run IDs, queue IDs, event payloads, or hidden conversation data.\n\n")
	b.WriteString("  Hard rules: never invent requirements, implementation details, or acceptance criteria the user did not express; never reduce multi-sentence input to a single vague sentence; never echo the title.\n\n")

	// priority
	b.WriteString("- **priority**: one of `urgent`, `high`, `medium`, `low`, or omit. Map P0/P1 → urgent/high; \"asap\" → urgent. If unspecified, omit.\n\n")

	// assignee
	b.WriteString("- **assignee**:\n")
	b.WriteString("    - When the user names someone (\"assign to X\" / \"@X\"), call `multica workspace member list --output json` and `multica workspace info --agents --output json` and find the matching entity by display name. Assignees are members or agents only (squads are retired). On a clean unambiguous match, prefer `--assignee-id <uuid>` using the `user_id` (member) or `id` (agent) from that JSON — UUID matching is exact and robust to name collisions in workspaces with overlapping names. `--assignee <name>` (fuzzy) is acceptable as a fallback when names are unambiguous. On no match or ambiguous match, do NOT pass either flag — instead append a final line to the description: `Unrecognized assignee: X`.\n")
	b.WriteString("    - Treat bare @-routing as an assignee directive even when the user did not write the English word \"assign\". This includes imperative forms such as `have @X review this PR`, `@X handle it`, or `give it to @X`; strip the leading `@`/`＠` before matching display names. Do not keep that routing wrapper or `@Name` in the description unless it is a true CC-style notification rather than ownership.\n")
	agentID := ""
	agentName := ""
	if task.Agent != nil {
		agentID = task.Agent.ID
		agentName = task.Agent.Name
	}
	switch {
	case agentID != "":
		fmt.Fprintf(&b, "    - When the user did NOT name an assignee, default to YOURSELF: pass `--assignee-id %q` (your agent UUID). The picker agent is the expected owner because the user opened quick-create with you selected — never leave the issue unassigned. Use the UUID flag, not `--assignee <name>`, so the assignment is unambiguous even when other agents share part of your name.\n\n", agentID)
	case agentName != "":
		fmt.Fprintf(&b, "    - When the user did NOT name an assignee, default to YOURSELF: pass `--assignee %q`. The picker agent is the expected owner because the user opened quick-create with you selected — never leave the issue unassigned.\n\n", agentName)
	default:
		b.WriteString("    - When the user did NOT name an assignee, default to YOURSELF (the picker agent): pass `--assignee-id <your agent UUID>` (preferred) or `--assignee <your agent name>`. Never leave the issue unassigned.\n\n")
	}

	// project — pinned by the modal when the user picked one, otherwise
	// omitted so the platform routes to the workspace default. Always pass
	// the UUID (never a name) so the issue lands in the right project even
	// when several share a title.
	if task.ProjectID != "" {
		if task.ProjectTitle != "" {
			fmt.Fprintf(&b, "- **project**: required for this run. Pass `--project %q` so the new issue lands in project %q (the user picked it in the quick-create modal). Do not infer a different project from the prompt text — the modal selection is authoritative.\n", task.ProjectID, task.ProjectTitle)
		} else {
			fmt.Fprintf(&b, "- **project**: required for this run. Pass `--project %q` so the new issue lands in the project the user picked in the quick-create modal. Do not infer a different project from the prompt text — the modal selection is authoritative.\n", task.ProjectID)
		}
	} else {
		b.WriteString("- **project**: omit. The platform will route the issue to the workspace default.\n")
	}
	// parent — pinned by the modal when the user opened it from "Add sub
	// issue" on an existing issue. Pass the UUID (never the identifier) so
	// the create lands the sub-issue under the right parent even when the
	// workspace prefix changes; the identifier is included in the prose
	// purely as human-readable context for the agent.
	if task.ParentIssueID != "" {
		if task.ParentIssueIdentifier != "" {
			fmt.Fprintf(&b, "- **parent**: required for this run. Pass `--parent %q` so the new issue is filed as a sub-issue of %s (the user opened quick-create from that issue's \"Add sub issue\" entry). Do not infer a different parent from the prompt text — the modal entry point is authoritative.\n", task.ParentIssueID, task.ParentIssueIdentifier)
		} else {
			fmt.Fprintf(&b, "- **parent**: required for this run. Pass `--parent %q` so the new issue is filed as a sub-issue of the parent the user picked in the quick-create modal. Do not infer a different parent from the prompt text — the modal entry point is authoritative.\n", task.ParentIssueID)
		}
	}
	b.WriteString("- **status**: omit (defaults to `todo`).\n")
	b.WriteString("- **attachments**: when Source attachment IDs are listed (or `MULTICA_QUICK_CREATE_ATTACHMENT_IDS` is set), pass EVERY id with `--attachment-id` on this MAIN `issue create`. Also keep markdown image embeds inline in `--description`. Do NOT create a separate attachment-carrier sub-issue as the only place for screenshots — sub-issues may back up designs, but the parent/main issue must show the reference images (attachments non-empty or description embeds). Do NOT try to re-upload image URLs.\n\n")

	// output format
	b.WriteString("Output format:\n")
	b.WriteString("- Run exactly one `multica issue create --output json` invocation. Do not retry for any reason — even on non-zero exit. The issue may already exist; another attempt would create a duplicate.\n")
	b.WriteString("- Parse the JSON response to read the created issue's `identifier` (preferred) or `id` (fallback). Do not scrape human output and do not assume any workspace issue prefix such as `MUL-`; workspaces can use custom prefixes.\n")
	b.WriteString("- After success, print exactly one line: `Created <identifier-or-id>: <title>` and exit. No commentary, no follow-up tool calls.\n")
	b.WriteString("- Do NOT call `multica issue get` or `multica issue comment add` — there is no issue to query or comment on.\n")
	b.WriteString("- On CLI error or JSON parse error, exit with the error as the only output. The platform writes a failure notification automatically.\n")
	return b.String()
}

func formatQuickCreateSourceContext(src *protocol.QuickCreateSourceContext) string {
	if src == nil {
		return ""
	}
	var b strings.Builder
	surface := "channel"
	if src.ChannelKind == "dm" {
		surface = "DM"
	} else if src.ChannelName != "" {
		surface = "channel #" + src.ChannelName
	}
	fmt.Fprintf(&b, "- Source surface: %s\n", surface)
	fmt.Fprintf(&b, "- Channel ID: %s\n", src.ChannelID)
	fmt.Fprintf(&b, "- Thread root message ID: %s\n", src.ThreadRootMessageID)
	fmt.Fprintf(&b, "- Source message ID: %s\n", src.SourceMessageID)
	if src.SourceAuthorName != "" || src.SourceAuthorType != "" {
		fmt.Fprintf(&b, "- Source author: %s", src.SourceAuthorName)
		if src.SourceAuthorType != "" {
			fmt.Fprintf(&b, " (%s)", src.SourceAuthorType)
		}
		if src.SourceAuthorID != "" {
			fmt.Fprintf(&b, " [%s]", src.SourceAuthorID)
		}
		b.WriteString("\n")
	}
	if src.SourceExcerpt != "" {
		fmt.Fprintf(&b, "- Source excerpt: %s\n", src.SourceExcerpt)
	}
	if len(src.AttachmentIDs) > 0 {
		fmt.Fprintf(&b, "- Source attachment IDs: %s\n", strings.Join(src.AttachmentIDs, ", "))
	}
	if strings.TrimSpace(src.Summary) != "" {
		b.WriteString("\nBounded visible summary:\n")
		b.WriteString(strings.TrimSpace(src.Summary))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// buildCommentPrompt constructs a prompt for comment-triggered tasks.
// The triggering comment content is embedded directly so the agent cannot
// miss it, even when stale output files exist in a reused workdir.
// The reply instructions (including the current TriggerCommentID as --parent)
// are re-emitted on every turn so resumed sessions cannot carry forward a
// previous turn's --parent UUID.
func buildCommentPrompt(task Task, provider string) string {
	var b strings.Builder
	b.WriteString("You are running as a local coding agent for a Multica workspace.\n\n")
	fmt.Fprintf(&b, "Your assigned issue ID is: %s\n\n", task.IssueID)
	if task.TriggerCommentContent != "" {
		authorLabel := "A user"
		if task.TriggerAuthorType == "agent" {
			name := task.TriggerAuthorName
			if name == "" {
				name = "another agent"
			}
			authorLabel = fmt.Sprintf("Another agent (%s)", name)
		}
		fmt.Fprintf(&b, "[NEW COMMENT] %s just left a new comment. Focus on THIS comment — do not confuse it with previous ones:\n\n", authorLabel)
		fmt.Fprintf(&b, "> %s\n\n", task.TriggerCommentContent)
		promptcontext.AppendReferencedEntitySnapshots(&b, task.ReferencedEntities, task.ReferencedEntityOmittedCount)
		if task.TriggerAuthorType == "agent" {
			b.WriteString("⚠️ The triggering comment was posted by another agent. Decide whether a reply is warranted. If you produced actual work this turn (investigated, fixed something, answered a real question), post the result as a normal reply — that is NOT a noise comment, and the standard rule that final results must be delivered via comment still applies. If the triggering comment was a pure acknowledgment, thanks, or sign-off AND you produced no work this turn, do NOT reply — and do NOT post a comment saying 'No reply needed' or similar. Simply exit with no output. Silence is the preferred way to end agent-to-agent threads. If you do reply, do not @mention the other agent as a sign-off (that re-triggers them and starts a loop).\n\n")
		}
	}
	fmt.Fprintf(&b, "Start by running `multica issue get %s --output json` to understand your task, then decide how to proceed.\n\n", task.IssueID)
	// Comment-reading pointer. Warm path with new comments: issue-wide
	// since-delta count, but steer the agent to read the triggering thread
	// first. Warm resumed path with no new comments: the trigger is already
	// injected, so don't force a duplicate thread read. Cold path: read the
	// triggering thread, not the flat timeline. Final fallback (no trigger id,
	// shouldn't happen here): plain read.
	if hint := execenv.BuildNewCommentsHint(task.IssueID, task.TriggerCommentID, task.TriggerThreadID, task.NewCommentsSince, task.NewCommentCount); hint != "" {
		b.WriteString(hint)
	} else if task.PriorSessionID != "" {
		b.WriteString(execenv.BuildResumedCommentsHint(task.IssueID, task.TriggerCommentID, task.TriggerThreadID))
	} else if cold := execenv.BuildColdCommentsHint(task.IssueID, task.TriggerCommentID, task.TriggerThreadID); cold != "" {
		b.WriteString(cold)
	} else {
		fmt.Fprintf(&b, "Read the discussion: `multica issue comment list %s --output json` (long issue? use `--recent 20`).\n\n", task.IssueID)
	}
	b.WriteString(execenv.BuildCommentReplyInstructions(provider, task.IssueID, task.TriggerCommentID))
	b.WriteString("Verify any requested change proportionately before replying. Do not change the issue status unless the triggering comment explicitly asks you to. Final assistant output is not delivered as the Issue reply.\n")
	return b.String()
}

// writeAgentRootSection exposes a short, explicit persistence contract. Paths
// not used by the current turn stay lazy and do not need prompt space.
func writeAgentRootSection(b *strings.Builder, agentRoot string) {
	if strings.TrimSpace(agentRoot) == "" {
		return
	}
	b.WriteString("Persistent memory (create files only when writing real content):\n")
	fmt.Fprintf(b, "- Durable cross-task knowledge: %s/memory/MEMORY.md\n", agentRoot)
	fmt.Fprintf(b, "- Dated temporary state: %s/memory/STATE.md\n", agentRoot)
	fmt.Fprintf(b, "- Today's concise work log: %s/memory/daily/YYYY-MM-DD.md\n", agentRoot)
	fmt.Fprintf(b, "- Current member preferences/relationship: %s/users/<member-id>/USER.md or RELATIONSHIP.md\n", agentRoot)
	fmt.Fprintf(b, "- Current project knowledge/state/decisions: %s/projects/<project-id>/MEMORY.md, STATE.md, or DECISIONS.md\n", agentRoot)
	fmt.Fprintf(b, "- Current channel collaboration context: %s/channels/<channel-id>/CONTEXT.md\n", agentRoot)
	b.WriteString("Keep agent, member, project, and channel scopes separate. Do not create empty files, placeholder templates, directories for unused scopes, or parallel memory files.\n\n")
}

// buildChatPrompt constructs a prompt for interactive chat tasks.
func buildChatPrompt(task Task, agentRoot string) string {
	var b strings.Builder
	b.WriteString("You are running as a chat assistant for a Multica workspace.\n")
	b.WriteString("A user is chatting with you directly. Respond to their message.\n\n")
	if task.ChannelID == "" {
		b.WriteString("Standalone chat delivery contract (READ FIRST — overrides agent habits and skills that mention channel/DM transport):\n")
		b.WriteString("- This task is a Multica `chat_session` (bubble / FAB chat), NOT a channel or outer-DM transport task.\n")
		b.WriteString("- Your final assistant output is delivered to this chat session automatically.\n")
		b.WriteString("- Do NOT run `multica message send`, `multica message react`, or search for a DM/channel `--target` to reply to THIS turn.\n")
		b.WriteString("- Pure greetings (hi / hello / hey): reply with a sticker JSON only. Zero tools. Zero troubleshooting. Example: `{\"action\":\"message_send\",\"parts\":[{\"type\":\"sticker\",\"sticker_id\":\"hi\"}]}`.\n")
		b.WriteString("- If you mistakenly call `multica message send` and get `agent task is not a channel task` (403): STOP. Do not run help/env/rg/daemon diagnostics. Immediately reply via final output instead.\n")
		b.WriteString("- For a short text reply without a sticker, return plain text (or `{\"action\":\"message_send\",\"parts\":[{\"type\":\"text\",\"text\":\"...\"}]}`) as the final output — never dump protocol JSON as user-visible prose after a tool spiral.\n\n")
	}
	writeAgentRootSection(&b, agentRoot)
	// Per-turn initiator (option A): who is speaking this turn. Not in startup
	// AGENTS digest — same chat can change speakers without process restart.
	if name := strings.TrimSpace(task.InitiatorName); name != "" {
		b.WriteString("Current message initiator:\n")
		fmt.Fprintf(&b, "- Name: %s\n", name)
		if t := strings.TrimSpace(task.InitiatorType); t != "" {
			fmt.Fprintf(&b, "- Type: %s\n", t)
		}
		if id := strings.TrimSpace(task.InitiatorID); id != "" {
			fmt.Fprintf(&b, "- ID: %s\n", id)
		}
		// Member email was previously only in AGENTS brief; option A static strip
		// removes it from startup — must live in the per-turn envelope with the
		// same sanitizer (Parker: migration keeps the guard).
		if task.InitiatorType == "member" {
			if email := execenv.SanitizeEmailForBrief(task.InitiatorEmail); email != "" {
				fmt.Fprintf(&b, "- Email: %s\n", email)
			}
		}
		b.WriteString("\n")
	}
	// Per-turn issue/trigger facts when present on a chat wake (not startup-static).
	if id := strings.TrimSpace(task.IssueID); id != "" {
		fmt.Fprintf(&b, "Related issue for this turn: %s\n", id)
		if cid := strings.TrimSpace(task.TriggerCommentID); cid != "" {
			fmt.Fprintf(&b, "Trigger comment: %s\n", cid)
		}
		b.WriteString("\n")
	}
	b.WriteString("Context assembly rules:\n")
	b.WriteString("- Treat the injected conversation context as scoped to the current DM, channel, or thread only. Do not assume visibility into other DMs, channels, issues, or threads unless the user explicitly references them and the Multica CLI allows access.\n")
	b.WriteString("- For thread-triggered runs, the thread root and recent replies are the relevant conversation boundary; do not infer the entire parent channel/DM history.\n")
	b.WriteString("- Channel/thread snippets are intentionally bounded. If the answer depends on omitted channel history, search or fetch more channel/thread messages via Multica before guessing.\n")
	b.WriteString("- Full histories, issue timelines, attachments, project metadata, and complete skill files are lazy context: load them only with the appropriate tool/CLI command when needed.\n\n")
	if task.Agent != nil && len(task.Agent.Skills) > 0 {
		refs := ExtractSlashSkills(task.ChatMessage)
		if len(refs) > 0 {
			agentSkills := make(map[string]string, len(task.Agent.Skills))
			for _, s := range task.Agent.Skills {
				agentSkills[s.ID] = s.Name
			}

			selected := make([]string, 0, len(refs))
			seen := make(map[string]struct{}, len(refs))
			for _, ref := range refs {
				name, ok := agentSkills[ref.ID]
				if !ok {
					continue
				}
				if _, ok := seen[ref.ID]; ok {
					continue
				}
				seen[ref.ID] = struct{}{}
				selected = append(selected, name)
			}

			if len(selected) > 0 {
				b.WriteString("Explicitly selected skills:\n")
				for _, name := range selected {
					fmt.Fprintf(&b, "- %s\n", name)
				}
				b.WriteString("\n")
			}
		}
	}
	if strings.TrimSpace(task.ChatContextSummary) != "" {
		b.WriteString("Conversation surface context:\n")
		b.WriteString(task.ChatContextSummary)
		if !strings.HasSuffix(task.ChatContextSummary, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "User message:\n%s\n", task.ChatMessage)
	promptcontext.AppendReferencedEntitySnapshots(&b, task.ReferencedEntities, task.ReferencedEntityOmittedCount)
	// List attachments by id + filename so the agent can fetch them via
	// the CLI. We deliberately do NOT inline the URL: chat attachments
	// live behind a signed CDN with a short TTL, so by the time the agent
	// has finished thinking the URL embedded in the markdown body may
	// have expired. `multica attachment view --id <id> --output <path>`
	// re-signs at download time and is the only reliable path.
	if len(task.ChatMessageAttachments) > 0 {
		b.WriteString("\nAttachments on this message:\n")
		for _, a := range task.ChatMessageAttachments {
			if a.ContentType != "" {
				fmt.Fprintf(&b, "- id=%s filename=%q content_type=%s\n", a.ID, a.Filename, a.ContentType)
			} else {
				fmt.Fprintf(&b, "- id=%s filename=%q\n", a.ID, a.Filename)
			}
		}
		b.WriteString("Use `multica attachment view --id <id> --output <path>` to fetch each file locally before referring to it.\n")
		b.WriteString("When creating an issue that should preserve these attachments, pass EVERY id with `--attachment-id <id>` on the MAIN `multica issue create` (plus markdown embeds in `--description`). Do NOT park screenshots only on an attachment-carrier sub-issue — the main issue must show the reference images.\n")
	}
	return b.String()
}

// buildAutopilotPrompt constructs a prompt for run_only autopilot tasks.
func buildAutopilotPrompt(task Task) string {
	var b strings.Builder
	b.WriteString("You are running as a local coding agent for a Multica workspace.\n\n")
	b.WriteString("This task was triggered by an Autopilot in run-only mode. There is no assigned Multica issue for this run.\n\n")
	fmt.Fprintf(&b, "Autopilot run ID: %s\n", task.AutopilotRunID)
	if task.AutopilotID != "" {
		fmt.Fprintf(&b, "Autopilot ID: %s\n", task.AutopilotID)
	}
	if task.AutopilotTitle != "" {
		fmt.Fprintf(&b, "Autopilot title: %s\n", task.AutopilotTitle)
	}
	if task.AutopilotSource != "" {
		fmt.Fprintf(&b, "Trigger source: %s\n", task.AutopilotSource)
	}
	if strings.TrimSpace(string(task.AutopilotTriggerPayload)) != "" {
		fmt.Fprintf(&b, "Trigger payload:\n%s\n", strings.TrimSpace(string(task.AutopilotTriggerPayload)))
	}
	b.WriteString("\nAutopilot instructions:\n")
	if strings.TrimSpace(task.AutopilotDescription) != "" {
		b.WriteString(task.AutopilotDescription)
		b.WriteString("\n\n")
	} else if task.AutopilotTitle != "" {
		fmt.Fprintf(&b, "%s\n\n", task.AutopilotTitle)
	} else {
		b.WriteString("No additional autopilot instructions were provided. Use only the task context above — the Autopilot product is retired (no `multica autopilot` CLI).\n\n")
	}
	// Autopilot CLI/API retired (task #40 / LRM-1049). Do not instruct agents
	// to run multica autopilot get — that command no longer exists.
	b.WriteString("Complete the instructions above.\n")
	b.WriteString("Do not run `multica issue get`; this run does not have an issue ID.\n")
	return b.String()
}
