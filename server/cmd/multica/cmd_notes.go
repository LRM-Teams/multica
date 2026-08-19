package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// notesCmd is the agent-facing product-note surface (S2-C2). Local agent
// memory files under ~/.multica/.../notes are unrelated — see
// docs/notes-editor-worker-contract.md.
var notesCmd = &cobra.Command{
	Use:   "notes",
	Short: "Read product notes (note_page) for the current Worker task",
	Long: "Read one Worker-authorized product note page. There is no `notes write` command; " +
		"`get` being the only subcommand does not mean product notes cannot be proposed. " +
		"From a DM or channel, pipe cleaned markdown to `multica message send --target <target> --note-write`. " +
		"Omit `--note-page-id` to create a note after human confirm. " +
		"Period Brief synthesizers may call `notes period-brief retry-collectors` to re-dispatch retryable collectors.",
}

var notesGetCmd = &cobra.Command{
	Use:   "get <page-id>",
	Short: "Get one product note page the current Worker task is allowed to read",
	Args:  cobra.ExactArgs(1),
	RunE:  runNotesGet,
}

var notesPeriodBriefCmd = &cobra.Command{
	Use:   "period-brief",
	Short: "Period Work Brief synthesizer helpers",
}

var notesPeriodBriefRetryCollectorsCmd = &cobra.Command{
	Use:   "retry-collectors",
	Short: "Re-dispatch retryable Period Work collectors for a draft (max 3 retries each)",
	Long: "Narrow tool for the Period Brief synthesizer. Platform skips permanent failures " +
		"(missing API key / model config / auth / quota) and collectors already at the retry cap. " +
		"After success, stop and wait — the platform re-wakes you when packs settle.",
	Args: cobra.NoArgs,
	RunE: runNotesPeriodBriefRetryCollectors,
}

func init() {
	notesCmd.AddCommand(notesGetCmd)
	notesCmd.AddCommand(notesPeriodBriefCmd)
	notesPeriodBriefCmd.AddCommand(notesPeriodBriefRetryCollectorsCmd)
	notesGetCmd.Flags().String("output", "json", "Output format: json (default) or table")
	notesPeriodBriefRetryCollectorsCmd.Flags().String("draft-page-id", "", "Period Brief draft page id (required)")
	notesPeriodBriefRetryCollectorsCmd.Flags().StringSlice("collector-agent-id", nil, "Optional collector agent ids to retry (default: all retryable)")
	_ = notesPeriodBriefRetryCollectorsCmd.MarkFlagRequired("draft-page-id")
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
