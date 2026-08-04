package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/turntransport"
	"github.com/spf13/cobra"
)

// multica action prepare — Raft-aligned hire card prepare (agent:create).
// Posts the card as a structured message part when --target is set.

var actionCmd = &cobra.Command{
	Use:   "action",
	Short: "Prepare human-confirmable action cards",
}

var actionPrepareCmd = &cobra.Command{
	Use:   "prepare",
	Short: "Prepare an agent:create hire card (and optionally post it)",
	RunE:  runActionPrepare,
}

func init() {
	actionCmd.AddCommand(actionPrepareCmd)
	actionPrepareCmd.Flags().String("type", "agent:create", "Action type (only agent:create)")
	actionPrepareCmd.Flags().String("name", "", "Agent display name seed (required)")
	actionPrepareCmd.Flags().String("description", "", "Optional short catalog description")
	actionPrepareCmd.Flags().String("target", "", "Channel/DM/thread to post the card (same as message send)")
	actionPrepareCmd.Flags().String("output", "json", "Output format: json or text")
	actionPrepareCmd.Flags().String("channel-id", "", "Optional channel_id for the card row")
}

func runActionPrepare(cmd *cobra.Command, _ []string) error {
	actionType := strings.TrimSpace(flagString(cmd, "type"))
	if actionType == "" {
		actionType = "agent:create"
	}
	if actionType != "agent:create" {
		return fmt.Errorf("unsupported --type %q (only agent:create)", actionType)
	}
	name := strings.TrimSpace(flagString(cmd, "name"))
	if name == "" {
		return fmt.Errorf("--name is required")
	}
	description := strings.TrimSpace(flagString(cmd, "description"))
	target := strings.TrimSpace(flagString(cmd, "target"))
	channelID := strings.TrimSpace(flagString(cmd, "channel-id"))

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cli.APITimeout())
	defer cancel()

	body := map[string]any{
		"action_type": actionType,
		"name":        name,
	}
	if description != "" {
		body["description"] = description
	}
	if channelID != "" {
		body["channel_id"] = channelID
	}

	var prepared map[string]any
	if err := client.PostJSON(ctx, "/api/agent/actions/prepare", body, &prepared); err != nil {
		return fmt.Errorf("action prepare: %w", err)
	}

	// When --target is set, post structured part like raft action prepare.
	if target != "" {
		part, _ := prepared["part"].(map[string]any)
		if part == nil {
			return fmt.Errorf("prepare response missing part template")
		}
		sendBody := map[string]any{
			"target": target,
			"parts":  []any{part},
		}
		// Optional short content for clients that only show text.
		if label, ok := part["label"].(string); ok && strings.TrimSpace(label) != "" {
			sendBody["content"] = strings.TrimSpace(label)
		} else {
			sendBody["content"] = name
		}
		if err := turntransport.RecordAttemptFromEnvironment(); err != nil {
			return err
		}
		var sent map[string]any
		if err := client.PostJSON(ctx, "/api/agent/messages/send", sendBody, &sent); err != nil {
			return fmt.Errorf("post action card message: %w", err)
		}
		prepared["message"] = sent
	}

	output := strings.ToLower(strings.TrimSpace(flagString(cmd, "output")))
	switch {
	case output == "" || output == "json":
		return cli.PrintJSON(os.Stdout, prepared)
	case output == "text":
		id, _ := prepared["id"].(string)
		fmt.Fprintf(os.Stdout, "Prepared agent:create card %s\n", id)
		if target != "" {
			fmt.Fprintf(os.Stdout, "Posted to %s\n", target)
		}
		return nil
	default:
		return fmt.Errorf("unsupported output format %q; use json or text", output)
	}
}
