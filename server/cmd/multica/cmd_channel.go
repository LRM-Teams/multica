package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// channelCmd exposes agent-side channel discovery and mute/unfollow controls.
var channelCmd = &cobra.Command{
	Use:   "channel",
	Short: "List and manage channels",
	Long: "List the channels the running agent is a member of, inspect " +
		"members, mute/unsubscribe from ambient delivery, and manage thread " +
		"attention.\n\n" +
		"These commands let an agent consciously reduce its ambient wake " +
		"surface by muting channels or unfollowing threads it no longer " +
		"needs to observe, while still receiving personal @mentions.",
}

var channelListCmd = &cobra.Command{
	Use:   "list",
	Short: "List channels the agent is a member of",
	RunE:  runChannelList,
}

var channelMembersCmd = &cobra.Command{
	Use:   "members",
	Short: "List members of a channel",
	Long: "List members (users and agents) of the specified channel. " +
		"Use --target to specify the channel by name (e.g. #my-channel) " +
		"or by URL (#workspace-id:channel-id). Only channels the " +
		"agent is a member of are visible.",
	RunE: runChannelMembers,
}

var channelMuteCmd = &cobra.Command{
	Use:   "mute",
	Short: "Mute ambient channel delivery",
	Long: "Mute the channel so the agent no longer receives ambient " +
		"(unaddressed) observation tasks from it. Personal @mentions and " +
		"DMs still arrive. Use --target to specify the channel.",
	RunE: runChannelMute,
}

var channelUnmuteCmd = &cobra.Command{
	Use:   "unmute",
	Short: "Unmute a previously muted channel",
	Long: "Restore ambient delivery from a muted channel. After unmuting, " +
		"the agent will receive ambient observation tasks again. " +
		"Use --target to specify the channel.",
	RunE: runChannelUnmute,
}

func init() {
	channelCmd.AddCommand(channelListCmd)
	channelCmd.AddCommand(channelMembersCmd)
	channelCmd.AddCommand(channelMuteCmd)
	channelCmd.AddCommand(channelUnmuteCmd)

	channelListCmd.Flags().String("output", "table", "Output format: table or json")
	channelMembersCmd.Flags().String("target", "", "Channel to inspect (#channel-name)")
	channelMembersCmd.Flags().String("output", "table", "Output format: table or json")
	_ = channelMembersCmd.MarkFlagRequired("target")
	channelMuteCmd.Flags().String("target", "", "Channel to mute (#channel-name)")
	_ = channelMuteCmd.MarkFlagRequired("target")
	channelUnmuteCmd.Flags().String("target", "", "Channel to unmute (#channel-name)")
	_ = channelUnmuteCmd.MarkFlagRequired("target")
}

func runChannelList(cmd *cobra.Command, _ []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	var channels []map[string]any
	if err := client.GetJSON(ctx, "/api/channels", &channels); err != nil {
		return fmt.Errorf("list channels: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		b, _ := json.MarshalIndent(channels, "", "  ")
		fmt.Println(string(b))
		return nil
	}

	fmt.Printf("%-40s %-20s %s\n", "ID", "Name", "Members")
	for _, ch := range channels {
		id, _ := ch["id"].(string)
		name, _ := ch["name"].(string)
		members := 0
		if m, ok := ch["member_count"].(float64); ok {
			members = int(m)
		}
		fmt.Printf("%-40s %-20s %d\n", id, name, members)
	}
	return nil
}

func runChannelMembers(cmd *cobra.Command, _ []string) error {
	target, _ := cmd.Flags().GetString("target")
	target = strings.TrimPrefix(target, "#")

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	path := fmt.Sprintf("/api/channels/%s/members", url.PathEscape(target))
	var members []map[string]any
	if err := client.GetJSON(ctx, path, &members); err != nil {
		return fmt.Errorf("list channel members: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		b, _ := json.MarshalIndent(members, "", "  ")
		fmt.Println(string(b))
		return nil
	}

	fmt.Printf("%-40s %-10s %-20s %s\n", "ID", "Type", "Name", "Role")
	for _, m := range members {
		id, _ := m["id"].(string)
		mtype, _ := m["member_type"].(string)
		name, _ := m["name"].(string)
		role, _ := m["role"].(string)
		fmt.Printf("%-40s %-10s %-20s %s\n", id, mtype, name, role)
	}
	return nil
}

func runChannelMute(cmd *cobra.Command, _ []string) error {
	return setChannelMute(cmd, true)
}

func runChannelUnmute(cmd *cobra.Command, _ []string) error {
	return setChannelMute(cmd, false)
}

func setChannelMute(cmd *cobra.Command, mute bool) error {
	target, _ := cmd.Flags().GetString("target")
	target = strings.TrimPrefix(target, "#")

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	action := "mute"
	path := fmt.Sprintf("/api/channels/%s/mute", url.PathEscape(target))
	if !mute {
		path = fmt.Sprintf("/api/channels/%s/unmute", url.PathEscape(target))
		action = "unmute"
	}

	var resp map[string]any
	if err := client.PostJSON(ctx, path, map[string]string{}, &resp); err != nil {
		return fmt.Errorf("%s channel: %w", action, err)
	}

	fmt.Printf("Channel %sd successfully\n", action)
	return nil
}
