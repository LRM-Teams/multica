package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// notesCmd is the agent-facing product-note surface (S2-C2). Local agent
// memory files under ~/.multica/.../notes are unrelated — see
// docs/notes-editor-worker-contract.md.
var notesCmd = &cobra.Command{
	Use:   "notes",
	Short: "Read product notes (note_page) authorized for the current agent",
	Long: "Read Worker- or Notes-bubble-authorized product note pages. There is no `notes write` command; " +
		"`get` / `tree` being the read surface does not mean product notes cannot be proposed. " +
		"From a DM or channel, pipe cleaned markdown to `multica message send --target <target> --note-write`. " +
		"Omit `--note-page-id` to create a note after human confirm. " +
		"Period Work collectors deliver packs with `notes period-brief submit-pack` (not --note-write). " +
		"The Notes Assistant collect-plan wake delivers `notes period-brief submit-collect-plan`. " +
		"Period Brief synthesizers may call `notes period-brief retry-collectors` to re-dispatch retryable collectors.",
}

var notesGetCmd = &cobra.Command{
	Use:   "get <page-id>",
	Short: "Get one product note page the current agent task is allowed to read",
	Args:  cobra.ExactArgs(1),
	RunE:  runNotesGet,
}

var notesTreeCmd = &cobra.Command{
	Use:   "tree <page-id>",
	Short: "List a product note page and its descendants (ids + titles)",
	Args:  cobra.ExactArgs(1),
	RunE:  runNotesTree,
}

var notesPeriodBriefCmd = &cobra.Command{
	Use:   "period-brief",
	Short: "Period Work Brief synthesizer helpers",
}

var notesPeriodBriefRetryCollectorsCmd = &cobra.Command{
	Use:   "retry-collectors",
	Short: "Re-dispatch retryable Period Work collectors for a draft (one retry each)",
	Long: "Narrow tool for the Period Brief synthesizer. Call once after a transient failure. " +
		"Platform skips permanent failures (missing API key / model config / auth / quota) and " +
		"collectors that already used their one retry. Inbox will not auto-retry. " +
		"After success, stop and wait — the platform re-wakes you when that attempt settles.",
	Args: cobra.NoArgs,
	RunE: runNotesPeriodBriefRetryCollectors,
}

var notesPeriodBriefSubmitCollectPlanCmd = &cobra.Command{
	Use:   "submit-collect-plan",
	Short: "Store a Notes Assistant collect plan on the Period Brief run",
	Long: "Planner-only tool. Reads JSON from stdin (or --json) and stores the collect plan " +
		"on note_period_brief_run.collect_plan. Do not --note-write. Do not submit-pack from this wake.",
	Args: cobra.NoArgs,
	RunE: runNotesPeriodBriefSubmitCollectPlan,
}

var notesPeriodBriefSubmitPackCmd = &cobra.Command{
	Use:   "submit-pack",
	Short: "Store a Period Work collector pack on the Brief run (not a Notes page)",
	Long: "Collector-only tool. Reads pack markdown from stdin (or --markdown) and stores it on " +
		"note_period_brief_run.collectors[].pack_markdown for the given draft. Do not --note-write packs into Notes.",
	Args: cobra.NoArgs,
	RunE: runNotesPeriodBriefSubmitPack,
}

func init() {
	notesCmd.AddCommand(notesGetCmd)
	notesCmd.AddCommand(notesTreeCmd)
	notesCmd.AddCommand(notesPeriodBriefCmd)
	notesPeriodBriefCmd.AddCommand(notesPeriodBriefRetryCollectorsCmd)
	notesPeriodBriefCmd.AddCommand(notesPeriodBriefSubmitPackCmd)
	notesPeriodBriefCmd.AddCommand(notesPeriodBriefSubmitCollectPlanCmd)
	notesGetCmd.Flags().String("output", "json", "Output format: json (default) or table")
	notesTreeCmd.Flags().String("output", "json", "Output format: json (default) or table")
	notesPeriodBriefRetryCollectorsCmd.Flags().String("draft-page-id", "", "Period Brief draft page id (required)")
	notesPeriodBriefRetryCollectorsCmd.Flags().StringSlice("collector-agent-id", nil, "Optional collector agent ids to retry (default: all retryable)")
	_ = notesPeriodBriefRetryCollectorsCmd.MarkFlagRequired("draft-page-id")
	notesPeriodBriefSubmitPackCmd.Flags().String("draft-page-id", "", "Period Brief draft page id (required)")
	notesPeriodBriefSubmitPackCmd.Flags().String("markdown", "", "Pack markdown (default: read stdin)")
	_ = notesPeriodBriefSubmitPackCmd.MarkFlagRequired("draft-page-id")
	notesPeriodBriefSubmitCollectPlanCmd.Flags().String("draft-page-id", "", "Period Brief draft page id (required)")
	notesPeriodBriefSubmitCollectPlanCmd.Flags().String("json", "", "Collect plan JSON (default: read stdin)")
	_ = notesPeriodBriefSubmitCollectPlanCmd.MarkFlagRequired("draft-page-id")
}

func runNotesGet(cmd *cobra.Command, args []string) error {
	if !isAgentAPIToken(cmd) {
		return fmt.Errorf("multica notes get requires an agent task token; human note access uses the product UI /api/notes")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	pageID := args[0]
	var page map[string]any
	path := "/api/agent/notes/pages/" + pageID
	if err := client.GetJSON(ctx, path, &page); err != nil {
		return fmt.Errorf("get note page: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		fmt.Fprintf(cmd.OutOrStdout(), "ID\t%s\nTITLE\t%s\nWORKSPACE\t%s\nUPDATED\t%s\n",
			strVal(page, "id"),
			strVal(page, "title"),
			strVal(page, "workspace_id"),
			strVal(page, "updated_at"),
		)
		if content := strVal(page, "content"); content != "" {
			fmt.Fprintln(cmd.OutOrStdout())
			fmt.Fprintln(cmd.OutOrStdout(), content)
		}
		return nil
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(page)
}

func runNotesTree(cmd *cobra.Command, args []string) error {
	if !isAgentAPIToken(cmd) {
		return fmt.Errorf("multica notes tree requires an agent task token; human note access uses the product UI /api/notes")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	pageID := args[0]
	var out map[string]any
	path := "/api/agent/notes/pages/" + pageID + "/tree"
	if err := client.GetJSON(ctx, path, &out); err != nil {
		return fmt.Errorf("list note tree: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		pages, _ := out["pages"].([]any)
		for _, raw := range pages {
			page, _ := raw.(map[string]any)
			depth := 0
			switch d := page["depth"].(type) {
			case float64:
				depth = int(d)
			case int:
				depth = d
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s%s\t%s\n",
				strings.Repeat("  ", depth),
				strVal(page, "title"),
				strVal(page, "id"),
			)
		}
		return nil
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func runNotesPeriodBriefRetryCollectors(cmd *cobra.Command, _ []string) error {
	if !isAgentAPIToken(cmd) {
		return fmt.Errorf("multica notes period-brief retry-collectors requires an agent task token")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	draftPageID, _ := cmd.Flags().GetString("draft-page-id")
	draftPageID = strings.TrimSpace(draftPageID)
	if draftPageID == "" {
		return fmt.Errorf("--draft-page-id is required")
	}
	collectorIDs, _ := cmd.Flags().GetStringSlice("collector-agent-id")
	body := map[string]any{}
	if len(collectorIDs) > 0 {
		cleaned := make([]string, 0, len(collectorIDs))
		for _, id := range collectorIDs {
			if id = strings.TrimSpace(id); id != "" {
				cleaned = append(cleaned, id)
			}
		}
		if len(cleaned) > 0 {
			body["collector_agent_ids"] = cleaned
		}
	}

	var out map[string]any
	path := "/api/agent/notes/period-briefs/" + draftPageID + "/retry-collectors"
	if err := client.PostJSON(ctx, path, body, &out); err != nil {
		return fmt.Errorf("retry collectors: %w", err)
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func runNotesPeriodBriefSubmitPack(cmd *cobra.Command, _ []string) error {
	if !isAgentAPIToken(cmd) {
		return fmt.Errorf("multica notes period-brief submit-pack requires an agent task token")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	draftPageID, _ := cmd.Flags().GetString("draft-page-id")
	draftPageID = strings.TrimSpace(draftPageID)
	if draftPageID == "" {
		return fmt.Errorf("--draft-page-id is required")
	}
	markdown, _ := cmd.Flags().GetString("markdown")
	markdown = strings.TrimSpace(markdown)
	if markdown == "" {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		markdown = strings.TrimSpace(string(raw))
	}
	if markdown == "" {
		return fmt.Errorf("pack markdown is required (stdin or --markdown)")
	}

	var out map[string]any
	path := "/api/agent/notes/period-briefs/" + draftPageID + "/submit-pack"
	if err := client.PostJSON(ctx, path, map[string]any{"markdown": markdown}, &out); err != nil {
		return fmt.Errorf("submit pack: %w", err)
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func runNotesPeriodBriefSubmitCollectPlan(cmd *cobra.Command, _ []string) error {
	if !isAgentAPIToken(cmd) {
		return fmt.Errorf("multica notes period-brief submit-collect-plan requires an agent task token")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	draftPageID, _ := cmd.Flags().GetString("draft-page-id")
	draftPageID = strings.TrimSpace(draftPageID)
	if draftPageID == "" {
		return fmt.Errorf("--draft-page-id is required")
	}
	rawJSON, _ := cmd.Flags().GetString("json")
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		rawJSON = strings.TrimSpace(string(raw))
	}
	if rawJSON == "" {
		return fmt.Errorf("collect plan JSON is required (stdin or --json)")
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &body); err != nil {
		return fmt.Errorf("collect plan must be JSON: %w", err)
	}

	var out map[string]any
	path := "/api/agent/notes/period-briefs/" + draftPageID + "/submit-collect-plan"
	if err := client.PostJSON(ctx, path, body, &out); err != nil {
		return fmt.Errorf("submit collect plan: %w", err)
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
