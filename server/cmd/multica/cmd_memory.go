package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon"
	"github.com/multica-ai/multica/server/internal/memorycuration"
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

func init() {
	memoryCmd.AddCommand(memoryCurateCmd)
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
	profile, _ := cmd.Flags().GetString("profile")
	workspacesRoot, err := daemon.ResolveWorkspacesRoot(profile, rootOverride)
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

func parseMemoryDate(raw, name string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid --%s date %q (expected YYYY-MM-DD)", name, raw)
	}
	return t, nil
}
