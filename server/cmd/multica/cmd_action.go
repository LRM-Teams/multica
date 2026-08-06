package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/cli"
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
	Short: "Prepare an agent:create hire card (canonical Message when --target is set)",
	RunE:  runActionPrepare,
}

func init() {
	actionCmd.AddCommand(actionPrepareCmd)
	actionPrepareCmd.Flags().String("type", "agent:create", "Action type (only agent:create)")
	actionPrepareCmd.Flags().String("name", "", "Agent display name seed (required)")
	actionPrepareCmd.Flags().String("description", "", "Optional short catalog description")
	actionPrepareCmd.Flags().String("preferred-computer", "", "Optional preferred Computer suggestion (human may change)")
	actionPrepareCmd.Flags().String("target", "", "Channel/DM/thread to post the card (same as message send)")
	actionPrepareCmd.Flags().String("client-request-id", "", "Stable idempotency key; reused on retry to return the same message_id")
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
	preferredComputer := strings.TrimSpace(flagString(cmd, "preferred-computer"))
	target := strings.TrimSpace(flagString(cmd, "target"))
	channelID := strings.TrimSpace(flagString(cmd, "channel-id"))
	clientRequestID := strings.TrimSpace(flagString(cmd, "client-request-id"))
	if clientRequestID == "" {
		clientRequestID = uuid.NewString()
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cli.APITimeout())
	defer cancel()

	body := map[string]any{
		"action_type":       actionType,
		"name":              name,
		"client_request_id": clientRequestID,
	}
	if description != "" {
		body["description"] = description
	}
	if preferredComputer != "" {
		body["preferred_computer"] = preferredComputer
	}
	if channelID != "" {
		body["channel_id"] = channelID
	}
	if target != "" {
		body["target"] = target
	}

	var prepared map[string]any
	if err := client.PostJSON(ctx, "/api/agent/actions/prepare", body, &prepared); err != nil {
		return fmt.Errorf("action prepare: %w", err)
	}

	// LRM-2343: prepare returns message_id and already created the canonical
	// Message (story 2/3). The CLI no longer issues a second message-send.
	output := strings.ToLower(strings.TrimSpace(flagString(cmd, "output")))
	switch {
	case output == "" || output == "json":
		return cli.PrintJSON(os.Stdout, prepared)
	case output == "text":
		id, _ := prepared["id"].(string)
		messageID, _ := prepared["message_id"].(string)
		fmt.Fprintf(os.Stdout, "Prepared agent:create card %s\n", id)
		if messageID != "" {
			fmt.Fprintf(os.Stdout, "Message %s (target %s)\n", messageID, target)
		}
		return nil
	default:
		return fmt.Errorf("unsupported output format %q; use json or text", output)
	}
}
