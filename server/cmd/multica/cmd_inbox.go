package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

type inboxSourceRef struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Revision string `json:"revision,omitempty"`
}

type inboxMessageRow struct {
	Target            string   `json:"target"`
	PendingCount      int      `json:"pendingCount"`
	FirstPendingMsgID string   `json:"firstPendingMsgId,omitempty"`
	LatestMsgID       string   `json:"latestMsgId,omitempty"`
	LatestSenderName  string   `json:"latestSenderName,omitempty"`
	Flags             []string `json:"flags"`
}

type inboxTypedItem struct {
	Source            string           `json:"source"`
	Row               *inboxMessageRow `json:"row,omitempty"`
	ItemID            string           `json:"itemId,omitempty"`
	AppID             string           `json:"appId,omitempty"`
	NotificationClass string           `json:"notificationClass,omitempty"`
	SourceRef         inboxSourceRef   `json:"sourceRef,omitempty"`
	ActionCLI         string           `json:"actionCli,omitempty"`
	Retention         string           `json:"retention,omitempty"`
	Title             string           `json:"title,omitempty"`
	Summary           string           `json:"summary,omitempty"`
}

type inboxAcknowledgedSource struct {
	AppID             string         `json:"appId"`
	NotificationClass string         `json:"notificationClass"`
	SourceRef         inboxSourceRef `json:"sourceRef"`
	ItemID            string         `json:"itemId"`
}

type inboxCheckResponse struct {
	Rows                   []inboxMessageRow         `json:"rows"`
	Items                  []inboxTypedItem          `json:"items"`
	AcknowledgedAppSources []inboxAcknowledgedSource `json:"acknowledged_app_sources"`
}

type inboxAckResponse struct {
	OK                bool   `json:"ok"`
	ItemID            string `json:"itemId"`
	RemainingAppItems int    `json:"remaining_app_items"`
}

var inboxCmd = &cobra.Command{Use: "inbox", Short: "Inspect and acknowledge the local aggregate Inbox"}
var inboxCheckCmd = &cobra.Command{Use: "check", Short: "Show pending Inbox targets and app items without consuming them", Args: cobra.NoArgs, RunE: runInboxCheck}
var inboxAckCmd = &cobra.Command{Use: "ack", Short: "Acknowledge one exact app Inbox item", Args: cobra.NoArgs, RunE: runInboxAck}

const (
	localAgentInboxPath    = "/inbox"
	localAgentInboxAckPath = "/inbox/ack"
)

func init() {
	inboxCmd.AddCommand(inboxCheckCmd, inboxAckCmd)
	inboxAckCmd.Flags().String("item-id", "", "Exact app Inbox item id")
}

func runInboxCheck(cmd *cobra.Command, _ []string) error {
	return runInboxCheckWithWriter(cmd, cmd.OutOrStdout())
}

func runInboxCheckWithWriter(cmd *cobra.Command, output io.Writer) error {
	if !inAgentExecutionContext() {
		return errors.New("`multica inbox check` is only available inside managed daemon runners")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(cmd.Context())
	defer cancel()
	var response inboxCheckResponse
	if err := client.GetJSON(ctx, localAgentInboxPath, &response); err != nil {
		return fmt.Errorf("check local Inbox: %w", err)
	}
	_, err = fmt.Fprintln(output, formatInboxSnapshot(response))
	return err
}

func runInboxAck(cmd *cobra.Command, _ []string) error {
	if !inAgentExecutionContext() {
		return errors.New("`multica inbox ack` is only available inside managed daemon runners")
	}
	itemID := strings.TrimSpace(flagString(cmd, "item-id"))
	if itemID == "" {
		return errors.New("--item-id is required")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(cmd.Context())
	defer cancel()
	var response inboxAckResponse
	if err := client.PostJSON(ctx, localAgentInboxAckPath, map[string]string{"itemId": itemID}, &response); err != nil {
		return fmt.Errorf("acknowledge local Inbox item: %w", err)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Inbox item %s acknowledged; %d app item(s) remain.\n", response.ItemID, response.RemainingAppItems)
	return err
}

func formatInboxSnapshot(snapshot inboxCheckResponse) string {
	parts := make([]string, 0, 2)
	if len(snapshot.Rows) == 0 {
		parts = append(parts, "Inbox: empty")
	} else {
		lines := []string{fmt.Sprintf("Inbox: %d pending target(s)", len(snapshot.Rows))}
		for _, row := range snapshot.Rows {
			details := fmt.Sprintf("pending: %d message(s)", row.PendingCount)
			if row.FirstPendingMsgID != "" {
				details += " · first msg=" + shortInboxID(row.FirstPendingMsgID)
			}
			if row.LatestSenderName != "" {
				details += " · latest sender @" + row.LatestSenderName
			}
			if row.LatestMsgID != "" {
				details += " · latest msg=" + shortInboxID(row.LatestMsgID)
			}
			lines = append(lines, "", row.Target, details)
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	appItems := make([]inboxTypedItem, 0)
	for _, item := range snapshot.Items {
		if item.Source == "app" {
			appItems = append(appItems, item)
		}
	}
	if len(appItems) > 0 {
		lines := []string{fmt.Sprintf("App items: %d", len(appItems))}
		for _, item := range appItems {
			line := fmt.Sprintf("app=%s · class=%s · item=%s · retention=%s · sourceRef=%s:%s:%s · action=%s", item.AppID, item.NotificationClass, shortInboxID(item.ItemID), item.Retention, item.SourceRef.Kind, item.SourceRef.ID, item.SourceRef.Revision, item.ActionCLI)
			if item.Title != "" {
				line += " · title=" + item.Title
			}
			if item.Summary != "" {
				line += " · summary=" + item.Summary
			}
			lines = append(lines, "", line)
		}
		if len(snapshot.Rows) == 0 {
			parts = parts[:0]
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

func shortInboxID(value string) string {
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}
