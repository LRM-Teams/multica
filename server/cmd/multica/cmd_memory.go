package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon"
	"github.com/multica-ai/multica/server/internal/memorycuration"
	"github.com/multica-ai/multica/server/internal/memoryrecall"
)

var memoryCmd = &cobra.Command{
	Use:   "memory",
	Short: "Manage local agent memory files",
}

var memoryCurateCmd = &cobra.Command{
	Use:   "curate",
	Short: "Run the agent memory curation pipeline for local agent roots",
	RunE:  runMemoryCurate,
}

var memorySearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search scoped agent memory files for a query",
	Args:  exactArgs(1),
	RunE:  runMemorySearch,
}

var memoryGetCmd = &cobra.Command{
	Use:   "get <path>",
	Short: "Read a scoped agent memory file or line range",
	Args:  exactArgs(1),
	RunE:  runMemoryGet,
}

func init() {
	memoryCmd.AddCommand(memorySearchCmd, memoryGetCmd, memoryCurateCmd)
	memorySearchCmd.Flags().Int("limit", memoryrecall.DefaultLimit, "Maximum hits (capped at 20)")
	memorySearchCmd.Flags().String("agent-root", "", "Override MULTICA_AGENT_ROOT")
	memorySearchCmd.Flags().String("member-id", "", "Override MULTICA_MEMBER_ID")
	memorySearchCmd.Flags().String("project-id", "", "Override MULTICA_PROJECT_ID")
	memorySearchCmd.Flags().String("channel-id", "", "Override MULTICA_CHANNEL_ID")
	memorySearchCmd.Flags().String("output", "json", "Output format: json or table")
	memoryGetCmd.Flags().Int("from-line", 1, "1-based start line")
	memoryGetCmd.Flags().Int("lines", memoryrecall.DefaultGetLines, "Number of lines to return (capped at 200)")
	memoryGetCmd.Flags().String("agent-root", "", "Override MULTICA_AGENT_ROOT")
	memoryGetCmd.Flags().String("member-id", "", "Override MULTICA_MEMBER_ID")
	memoryGetCmd.Flags().String("project-id", "", "Override MULTICA_PROJECT_ID")
	memoryGetCmd.Flags().String("channel-id", "", "Override MULTICA_CHANNEL_ID")
	memoryGetCmd.Flags().String("output", "json", "Output format: json or table")
	memoryCurateCmd.Flags().String("workspace", "", "Workspace ID to curate (defaults to current workspace when available)")
	memoryCurateCmd.Flags().StringArray("agent", nil, "Agent ID to curate; repeatable")
	memoryCurateCmd.Flags().Bool("all-agents", false, "Curate every local agent in scope")
	memoryCurateCmd.Flags().String("since", "", "Start date, YYYY-MM-DD (defaults to --until)")
	memoryCurateCmd.Flags().String("until", "", "End date, YYYY-MM-DD (defaults to yesterday UTC)")
	memoryCurateCmd.Flags().String("stage", "all", "Stage to run: l1, l2, l3, l4, or all")
	memoryCurateCmd.Flags().Bool("include-history", false, "Reserved for future DB/history evidence scans; local file evidence is always used")
	memoryCurateCmd.Flags().Bool("dry-run", false, "Report planned changes without writing files")
	memoryCurateCmd.Flags().Bool("force", false, "Reprocess already processed inputs")
	memoryCurateCmd.Flags().String("workspaces-root", "", "Override local workspaces root (env: MULTICA_WORKSPACES_ROOT)")
	memoryCurateCmd.Flags().String("output", "table", "Output format: table or json")
}

func runMemoryCurate(cmd *cobra.Command, _ []string) error {
	stageRaw, _ := cmd.Flags().GetString("stage")
	stage, err := memorycuration.NormalizeStage(stageRaw)
	if err != nil {
		return err
	}
	workspaceID, _ := cmd.Flags().GetString("workspace")
	if workspaceID == "" {
		workspaceID, _ = requireWorkspaceID(cmd)
	}
	agents, _ := cmd.Flags().GetStringArray("agent")
	allAgents, _ := cmd.Flags().GetBool("all-agents")
	if len(agents) == 0 && !allAgents {
		return fmt.Errorf("pass at least one --agent or --all-agents")
	}
	rootOverride, _ := cmd.Flags().GetString("workspaces-root")
	workspacesRoot, err := daemon.ResolveWorkspacesRoot(rootOverride)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	until := now.AddDate(0, 0, -1)
	if raw, _ := cmd.Flags().GetString("until"); raw != "" {
		until, err = parseMemoryDate(raw, "until")
		if err != nil {
			return err
		}
	}
	since := until
	if raw, _ := cmd.Flags().GetString("since"); raw != "" {
		since, err = parseMemoryDate(raw, "since")
		if err != nil {
			return err
		}
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	force, _ := cmd.Flags().GetBool("force")
	includeHistory, _ := cmd.Flags().GetBool("include-history")
	res, err := memorycuration.NewEngine(memorycuration.NewL3ReviewerFromEnv()).Run(memorycuration.Options{
		WorkspacesRoot: workspacesRoot,
		WorkspaceID:    workspaceID,
		AgentIDs:       agents,
		AllAgents:      allAgents,
		Stage:          stage,
		Since:          since,
		Until:          until,
		IncludeHistory: includeHistory,
		DryRun:         dryRun,
		Force:          force,
		Now:            now,
		Timezone:       memorycuration.DefaultTimezone,
	})
	if err != nil {
		return err
	}
	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, res)
	}
	fmt.Fprintf(os.Stdout, "Memory curation %s completed for %d agent(s) under %s\n", stage, res.AgentsScanned, res.WorkspacesRoot)
	rows := [][]string{{
		strconv.Itoa(res.AgentsChanged),
		strconv.Itoa(res.DailyFilesWritten),
		strconv.Itoa(res.ReviewCandidatesAdded),
		strconv.Itoa(res.EntriesReviewed),
		strconv.Itoa(res.SkillCandidatesAdded),
		strconv.Itoa(res.EntriesPromoted),
		strconv.Itoa(res.EntriesArchived),
		strconv.Itoa(res.DuplicatesMerged),
		strconv.Itoa(len(res.Errors)),
	}}
	cli.PrintTable(os.Stdout, []string{"CHANGED_AGENTS", "DAILY", "REVIEW", "REVIEWED", "SKILL_DRAFTS", "PROMOTED", "ARCHIVED", "DEDUPED", "ERRORS"}, rows)
	return nil
}

func runMemorySearch(cmd *cobra.Command, args []string) error {
	scope, err := memoryRecallScope(cmd)
	if err != nil {
		return err
	}
	limit, _ := cmd.Flags().GetInt("limit")
	res, err := memoryrecall.Search(scope, args[0], limit)
	if err != nil {
		return err
	}
	output, _ := cmd.Flags().GetString("output")
	if output != "table" {
		return cli.PrintJSON(os.Stdout, res)
	}
	rows := make([][]string, 0, len(res.Hits))
	for _, hit := range res.Hits {
		rows = append(rows, []string{
			hit.Path,
			hit.Scope,
			strconv.FormatFloat(hit.Score, 'f', 3, 64),
			fmt.Sprintf("%d-%d", hit.LineStart, hit.LineEnd),
			hit.Snippet,
		})
	}
	cli.PrintTable(os.Stdout, []string{"PATH", "SCOPE", "SCORE", "LINES", "SNIPPET"}, rows)
	return nil
}

func runMemoryGet(cmd *cobra.Command, args []string) error {
	scope, err := memoryRecallScope(cmd)
	if err != nil {
		return err
	}
	fromLine, _ := cmd.Flags().GetInt("from-line")
	lines, _ := cmd.Flags().GetInt("lines")
	res, err := memoryrecall.Get(scope, args[0], fromLine, lines)
	if err != nil {
		return err
	}
	output, _ := cmd.Flags().GetString("output")
	if output != "table" {
		return cli.PrintJSON(os.Stdout, res)
	}
	fmt.Fprintf(os.Stdout, "%s:%d-%d\n%s\n", res.Path, res.LineStart, res.LineEnd, res.Content)
	return nil
}

func memoryRecallScope(cmd *cobra.Command) (memoryrecall.Scope, error) {
	scope := memoryrecall.ScopeFromEnv()
	if raw, _ := cmd.Flags().GetString("agent-root"); strings.TrimSpace(raw) != "" {
		scope.AgentRoot = strings.TrimSpace(raw)
	}
	if raw, _ := cmd.Flags().GetString("member-id"); strings.TrimSpace(raw) != "" {
		scope.MemberID = strings.TrimSpace(raw)
	}
	if raw, _ := cmd.Flags().GetString("project-id"); strings.TrimSpace(raw) != "" {
		scope.ProjectID = strings.TrimSpace(raw)
	}
	if raw, _ := cmd.Flags().GetString("channel-id"); strings.TrimSpace(raw) != "" {
		scope.ChannelID = strings.TrimSpace(raw)
	}
	if strings.TrimSpace(scope.AgentRoot) == "" {
		return scope, fmt.Errorf("MULTICA_AGENT_ROOT is required (or pass --agent-root)")
	}
	return scope, nil
}

func parseMemoryDate(raw, name string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid --%s date %q (expected YYYY-MM-DD)", name, raw)
	}
	return t, nil
}
