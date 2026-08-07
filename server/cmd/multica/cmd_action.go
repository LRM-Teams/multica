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

// multica action prepare — Raft-aligned agent:create Proposal prepare.
// Posts one canonical Message-backed Proposal at the required target.

var actionCmd = &cobra.Command{
	Use:   "action",
	Short: "Prepare human-confirmable agent creation proposals",
}

var actionPrepareCmd = &cobra.Command{
	Use:   "prepare",
	Short: "Prepare an agent:create proposal Message",
	RunE:  runActionPrepare,
}

func init() {
	actionCmd.AddCommand(actionPrepareCmd)
	actionPrepareCmd.Flags().String("type", "agent:create", "Action type (only agent:create)")
	actionPrepareCmd.Flags().String("name", "", "Permanent lowercase Agent name (required)")
	actionPrepareCmd.Flags().String("description", "", "Optional short catalog description")
	actionPrepareCmd.Flags().String("preferred-computer", "", "Optional preferred Computer suggestion (human may change)")
	actionPrepareCmd.Flags().String("target", "", "Required channel/DM/thread target (same as message send)")
	actionPrepareCmd.Flags().String("client-request-id", "", "Stable idempotency key; reused on retry to return the same message_id")
	actionPrepareCmd.Flags().String("output", "json", "Output format: json or text")
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
	if target == "" {
		return fmt.Errorf("--target is required")
	}
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
	body["target"] = target

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
		messageID, _ := prepared["message_id"].(string)
		fmt.Fprintf(os.Stdout, "Prepared agent:create proposal\n")
		if messageID != "" {
			fmt.Fprintf(os.Stdout, "Message %s (target %s)\n", messageID, target)
		}
		return nil
	default:
		return fmt.Errorf("unsupported output format %q; use json or text", output)
	}
}
