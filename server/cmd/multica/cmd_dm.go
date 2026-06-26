package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// dm is the agent-only surface for proactively messaging the human behind the
// current task. Sessions and recipient are resolved server-side from the task
// context, so the agent only supplies the message body.
var dmCmd = &cobra.Command{
	Use:   "dm",
	Short: "Send a 1:1 direct message to the human you're working for",
	Long: "Proactively send a private message to the human who triggered your " +
		"current task (falls back to your owner). Use it when you need their " +
		"input, have reached a conclusion, or are blocked — it lands in their " +
		"agent DM panel with an unread badge.\n\n" +
		"Direct messages are strictly human <-> agent. To reach ANOTHER agent, " +
		"post in a channel and @-mention it — never DM an agent.\n\n" +
		"Input modes:\n" +
		"  --message \"...\"        inline (decodes \\n, \\t escapes)\n" +
		"  --message-stdin         pipe a HEREDOC (preserves verbatim)\n" +
		"  --message-file <path>   read a UTF-8 file (Windows-safe)\n",
	RunE: runDM,
}

func init() {
	dmCmd.Flags().String("message", "", "Message to send (decodes \\n, \\r, \\t, \\\\; use --message-stdin to preserve literal backslashes)")
	dmCmd.Flags().Bool("message-stdin", false, "Read the message from stdin (preserves multi-line content verbatim)")
	dmCmd.Flags().String("message-file", "", "Read the message from a UTF-8 file")
	dmCmd.Flags().String("output", "table", "Output format: table or json")
}

func runDM(cmd *cobra.Command, _ []string) error {
	content, ok, err := resolveTextFlag(cmd, "message")
	if err != nil {
		return err
	}
	if !ok || strings.TrimSpace(content) == "" {
		return fmt.Errorf("message is required; pass --message, --message-stdin, or --message-file")
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	var out map[string]any
	if err := client.PostJSON(ctx, "/api/chat/agent-dm", map[string]any{"content": content}, &out); err != nil {
		return fmt.Errorf("send direct message: %w", err)
	}

	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(os.Stdout, out)
	}
	fmt.Fprintln(os.Stdout, "Direct message sent.")
	return nil
}
