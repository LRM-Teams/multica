package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var threadCmd = &cobra.Command{
	Use:   "thread",
	Short: "Manage thread subscriptions",
	Long: "Control which threads the agent actively follows. Unfollow a " +
		"thread to stop receiving ambient delivery from it (personal " +
		"@mentions still arrive). Posting in an unfollowed thread " +
		"automatically re-follows it.",
}

var threadUnfollowCmd = &cobra.Command{
	Use:   "unfollow",
	Short: "Unsubscribe from a thread",
	Long: "Unfollow the specified thread so the agent stops receiving " +
		"ambient delivery from it. Personal @mentions in the thread still " +
		"arrive. If the agent posts in this thread again, it will " +
		"automatically re-follow.\n\n" +
		"Use --target to specify the thread as #channel-name:message-id " +
		"or #workspace-id:channel-id:message-id.",
	RunE: runThreadUnfollow,
}

func init() {
	threadCmd.AddCommand(threadUnfollowCmd)
	threadUnfollowCmd.Flags().String("target", "", "Thread to unfollow (#channel-name:message-id)")
	_ = threadUnfollowCmd.MarkFlagRequired("target")
}

func runThreadUnfollow(cmd *cobra.Command, _ []string) error {
	target, _ := cmd.Flags().GetString("target")
	// Parse target: #channel:message-id or #workspace:channel:message-id
	target = strings.TrimPrefix(target, "#")
	parts := strings.SplitN(target, ":", 3)
	if len(parts) < 2 {
		return fmt.Errorf("invalid target format: use #channel-name:message-id or #workspace-id:channel-id:message-id")
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	channelID := parts[0]
	if len(parts) == 3 {
		channelID = parts[1]
	}

	messageID := parts[len(parts)-1]
	path := fmt.Sprintf("/api/channels/%s/messages/%s/unfollow", channelID, messageID)

	var resp map[string]any
	if err := client.PostJSON(ctx, path, map[string]string{}, &resp); err != nil {
		return fmt.Errorf("unfollow thread: %w", err)
	}

	fmt.Println("Thread unfollowed successfully")
	return nil
}
