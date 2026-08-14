package main

import (
	"context"
	"encoding/json"
	"fmt"

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
		"Omit `--note-page-id` to create a note after human confirm.",
}

var notesGetCmd = &cobra.Command{
	Use:   "get <page-id>",
	Short: "Get one product note page the current Worker task is allowed to read",
	Args:  cobra.ExactArgs(1),
	RunE:  runNotesGet,
}

func init() {
	notesCmd.AddCommand(notesGetCmd)
	notesGetCmd.Flags().String("output", "json", "Output format: json (default) or table")
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
