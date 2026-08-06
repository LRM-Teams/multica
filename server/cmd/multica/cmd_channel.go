package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
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

var channelCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a temporary multi-agent coordination group",
	Long: "Create an idempotent temporary coordination group from an active Agent run. " +
		"The initiating human becomes the group owner and observer. This command is " +
		"available only with an Agent task or inbox token.",
	RunE: runAgentChannelCreate,
}

var channelArchiveCmd = &cobra.Command{
	Use:   "archive",
	Short: "Archive a temporary coordination group created by this Agent",
	RunE:  runAgentChannelArchive,
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

var channelMemberCmd = &cobra.Command{
	Use:   "member",
	Short: "Manage channel members",
}

var channelMemberAddCmd = &cobra.Command{
	Use:   "add --target <channel> <agent> [<agent>...]",
	Short: "Add agent(s) to a group channel",
	Long: "Add one or more agents to a group channel in one call. Each <agent> " +
		"may be an agent UUID or display name (resolved against the workspace " +
		"workspace info --agents). Only agents can be added this way — to invite people, add " +
		"them from the channel UI. <channel> is the --target channel UUID or #name.", Args: cobra.MinimumNArgs(1),
	RunE: runChannelMemberAdd,
}

func init() {
	channelCmd.AddCommand(channelListCmd)
	channelCmd.AddCommand(channelCreateCmd)
	channelCmd.AddCommand(channelArchiveCmd)
	channelCmd.AddCommand(channelMembersCmd)
	channelCmd.AddCommand(channelMuteCmd)
	channelCmd.AddCommand(channelUnmuteCmd)
	channelCmd.AddCommand(channelMemberCmd)
	channelMemberCmd.AddCommand(channelMemberAddCmd)

	channelListCmd.Flags().String("output", "table", "Output format: table or json")
	channelCreateCmd.Flags().String("name", "", "Coordination group name")
	channelCreateCmd.Flags().String("description", "", "Optional group description")
	channelCreateCmd.Flags().StringSlice("member", nil, "Agent UUID or name to add (repeatable)")
	channelCreateCmd.Flags().String("parent", "", "Optional parent group (#name or UUID)")
	channelCreateCmd.Flags().String("purpose", "", "Optional general coordination purpose")
	channelCreateCmd.Flags().String("request-id", "", "Required idempotency key for this coordination group")
	channelCreateCmd.Flags().String("output", "table", "Output format: table or json")
	_ = channelCreateCmd.MarkFlagRequired("name")
	_ = channelCreateCmd.MarkFlagRequired("request-id")
	channelArchiveCmd.Flags().String("target", "", "Temporary coordination group (#name or UUID)")
	_ = channelArchiveCmd.MarkFlagRequired("target")

	channelMemberAddCmd.Flags().String("target", "", "Channel to add into (id or #channel-name)")
	channelMemberAddCmd.Flags().String("output", "table", "Output format: table or json")
	_ = channelMemberAddCmd.MarkFlagRequired("target")
	channelMembersCmd.Flags().String("target", "", "Channel to inspect (#channel-name)")
	channelMembersCmd.Flags().String("output", "table", "Output format: table or json")
	_ = channelMembersCmd.MarkFlagRequired("target")
	channelMuteCmd.Flags().String("target", "", "Channel to mute (#channel-name)")
	_ = channelMuteCmd.MarkFlagRequired("target")
	channelUnmuteCmd.Flags().String("target", "", "Channel to unmute (#channel-name)")
	_ = channelUnmuteCmd.MarkFlagRequired("target")
}

func runAgentChannelArchive(cmd *cobra.Command, _ []string) error {
	if !isAgentAPIToken(cmd) {
		return fmt.Errorf("channel archive requires an active Agent task or inbox token")
	}
	target := strings.TrimSpace(flagString(cmd, "target"))
	if target == "" {
		return fmt.Errorf("--target is required")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	channelID, err := resolveChannelRef(ctx, client, strings.TrimPrefix(target, "#"))
	if err != nil {
		return fmt.Errorf("resolve coordination channel %q: %w", target, err)
	}
	var resp map[string]any
	path := fmt.Sprintf("/api/agent/channels/%s/archive", url.PathEscape(channelID))
	if err := client.PostJSON(ctx, path, map[string]any{}, &resp); err != nil {
		return fmt.Errorf("archive coordination channel: %w", err)
	}
	fmt.Printf("Archived coordination channel %s\n", channelID)
	return nil
}

func runAgentChannelCreate(cmd *cobra.Command, _ []string) error {
	if !isAgentAPIToken(cmd) {
		return fmt.Errorf("channel create requires an active Agent task or inbox token")
	}
	name := strings.TrimSpace(flagString(cmd, "name"))
	requestID := strings.TrimSpace(flagString(cmd, "request-id"))
	if name == "" {
		return fmt.Errorf("--name is required")
	}
	if requestID == "" {
		return fmt.Errorf("--request-id is required")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	memberRefs, _ := cmd.Flags().GetStringSlice("member")
	memberIDs := make([]string, 0, len(memberRefs))
	seen := map[string]struct{}{}
	for _, ref := range memberRefs {
		ref = strings.TrimPrefix(strings.TrimSpace(ref), "@")
		if ref == "" {
			continue
		}
		agent, err := resolveAgentRef(ctx, client, ref)
		if err != nil {
			return fmt.Errorf("resolve agent %q: %w", ref, err)
		}
		if _, exists := seen[agent.ID]; exists {
			continue
		}
		seen[agent.ID] = struct{}{}
		memberIDs = append(memberIDs, agent.ID)
	}

	body := map[string]any{
		"name":              name,
		"member_agent_ids":  memberIDs,
		"client_request_id": requestID,
	}
	if description := strings.TrimSpace(flagString(cmd, "description")); description != "" {
		body["description"] = description
	}
	if purpose := strings.TrimSpace(flagString(cmd, "purpose")); purpose != "" {
		body["purpose"] = purpose
	}
	if parent := strings.TrimSpace(flagString(cmd, "parent")); parent != "" {
		parentID, err := resolveChannelRef(ctx, client, strings.TrimPrefix(parent, "#"))
		if err != nil {
			return fmt.Errorf("resolve parent channel %q: %w", parent, err)
		}
		body["parent_channel_id"] = parentID
	}

	var resp map[string]any
	if err := client.PostJSON(ctx, "/api/agent/channels", body, &resp); err != nil {
		return fmt.Errorf("create coordination channel: %w", err)
	}
	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		data, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	fmt.Printf("Created coordination channel %s (%s)\n", strVal(resp, "name"), strVal(resp, "channel_id"))
	return nil
}

func runChannelList(cmd *cobra.Command, _ []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	path := "/api/channels"
	if isAgentAPIToken(cmd) {
		path = "/api/agent/channels"
	}
	var channels []map[string]any
	if err := client.GetJSON(ctx, path, &channels); err != nil {
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
	if isAgentAPIToken(cmd) {
		path = fmt.Sprintf("/api/agent/channels/%s/members", url.PathEscape(target))
	}
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

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	channelID, err := resolveChannelIDFromUploadTarget(ctx, client, target)
	if err != nil {
		return fmt.Errorf("resolve channel target: %w", err)
	}
	if channelID == "" {
		return fmt.Errorf("--target is required")
	}

	action := "mute"
	path := fmt.Sprintf("/api/channels/%s/agent-mute", url.PathEscape(channelID))
	if isAgentAPIToken(cmd) {
		path = fmt.Sprintf("/api/agent/channels/%s/mute", url.PathEscape(channelID))
	}
	if !mute {
		action = "unmute"
	}

	var resp map[string]any
	if mute {
		err = client.PutJSON(ctx, path, map[string]string{}, &resp)
	} else {
		err = client.DeleteJSON(ctx, path)
	}
	if err != nil {
		return fmt.Errorf("%s channel: %w", action, err)
	}

	fmt.Printf("Channel %sd successfully\n", action)
	return nil
}

// runChannelMemberAdd adds one or more agents to a group channel. Agents are
// resolved by UUID or display name; only agents are accepted (no humans). The
// whole batch is sent to /channels/{id}/members/batch in one request, so the
// auth (the task initiator user) needs to be a channel member — the same gate
// as a manual add.
func runChannelMemberAdd(cmd *cobra.Command, args []string) error {
	target, _ := cmd.Flags().GetString("target")
	target = strings.TrimPrefix(strings.TrimSpace(target), "#")
	if target == "" {
		return fmt.Errorf("--target is required (channel id or #name)")
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	channelID, err := resolveChannelRef(ctx, client, target)
	if err != nil {
		return fmt.Errorf("resolve channel %q: %w", target, err)
	}

	members := make([]map[string]string, 0, len(args))
	labels := make([]string, 0, len(args))
	for _, ref := range args {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		agent, err := resolveAgentRef(ctx, client, ref)
		if err != nil {
			return fmt.Errorf("resolve agent %q: %w", ref, err)
		}
		members = append(members, map[string]string{"member_type": "agent", "member_id": agent.ID})
		labels = append(labels, agent.Display)
	}
	if len(members) == 0 {
		return fmt.Errorf("no agents to add")
	}

	path := fmt.Sprintf("/api/channels/%s/members/batch", url.PathEscape(channelID))
	var resp map[string]any
	if err := client.PostJSON(ctx, path, map[string]any{"members": members}, &resp); err != nil {
		return fmt.Errorf("add channel members: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		b, _ := json.MarshalIndent(map[string]any{"channel_id": channelID, "added": labels, "count": len(labels)}, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("Added %d agent(s) to channel %s: %s\n", len(labels), channelID, strings.Join(labels, ", "))
	return nil
}

// resolveChannelRef resolves a channel target (UUID or name) to a channel id
// via the workspace channel list. Ambiguous or missing names are errors.
// Under mat_* ambient token, list uses /api/agent/channels only (no human path).
func resolveChannelRef(ctx context.Context, client *cli.APIClient, target string) (string, error) {
	if _, err := uuid.Parse(target); err == nil {
		return target, nil
	}
	var channels []map[string]any
	path := agentChannelsListPathAmbient(client)
	if err := client.GetJSON(ctx, path, &channels); err != nil {
		return "", fmt.Errorf("list channels: %w", err)
	}
	var matches []string
	available := []string{}
	for _, ch := range channels {
		id, name := strVal(ch, "id"), strVal(ch, "name")
		if id == "" {
			continue
		}
		if name != "" {
			available = append(available, name)
		}
		if nameMatches(name, target) {
			matches = append(matches, id)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("no channel named %q (available: %s)", target, strings.Join(available, ", "))
	default:
		return "", fmt.Errorf("channel name %q is ambiguous (%d matches)", target, len(matches))
	}
}

// resolveAgentRef resolves an agent ref (UUID or display name) to an agent id
// via the workspace agent list. Ambiguous or missing names are errors.
func resolveAgentRef(ctx context.Context, client *cli.APIClient, ref string) (resolvedID, error) {
	if _, err := uuid.Parse(ref); err == nil {
		return resolvedID{ID: ref, Display: ref}, nil
	}
	var agents []map[string]any
	path := agentAgentsListPathAmbient(client)
	if err := client.GetJSON(ctx, path, &agents); err != nil {
		return resolvedID{}, fmt.Errorf("list agents: %w", err)
	}
	var matches []resolvedID
	available := []string{}
	for _, a := range agents {
		id := strVal(a, "id")
		if id == "" {
			continue
		}
		label := firstNonEmpty(strVal(a, "display_name"), strVal(a, "name"))
		if label != "" {
			available = append(available, label)
		}
		if nameMatches(strVal(a, "display_name"), ref) || nameMatches(strVal(a, "name"), ref) {
			matches = append(matches, resolvedID{ID: id, Display: firstNonEmpty(label, id)})
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return resolvedID{}, fmt.Errorf("no agent named %q (available: %s)", ref, strings.Join(available, ", "))
	default:
		return resolvedID{}, fmt.Errorf("agent name %q is ambiguous (%d matches)", ref, len(matches))
	}
}

// nameMatches is a trimmed, case-insensitive comparison for display-name lookup.
func nameMatches(name, target string) bool {
	return strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(target))
}

// isMatAgentToken is the sole pure mat_* classifier. All path selection must
// go through this predicate — never reimplement with Getenv("MULTICA_TOKEN").
func isMatAgentToken(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), "mat_")
}

// isAgentAPIToken reports whether command handlers must use the dedicated Agent
// API surface. Daemon-managed runs have no bearer in their environment: their
// local Credential Proxy supplies it, so execution context is authoritative.
// The mat_ check remains only for direct legacy machine-token callers.
func isAgentAPIToken(cmd *cobra.Command) bool {
	return inAgentExecutionContext() || isMatAgentToken(resolveToken(cmd))
}

// isAgentAPITokenAmbient is for id resolvers without *cobra.Command.
// Daemon execution is Agent principal even though its credential stays local.
func isAgentAPITokenAmbient() bool {
	return inAgentExecutionContext() || isMatAgentToken(ambientTokenFromEnvOrFile())
}

// agentIssueAPIPath returns /api/issues/{id}{suffix} or /api/agent/issues/{id}{suffix}.
// suffix should start with "/" (e.g. "/labels") or be empty.
func agentIssueAPIPath(cmd *cobra.Command, issueID, suffix string) string {
	base := "/api/issues/"
	if isAgentAPIToken(cmd) {
		base = "/api/agent/issues/"
	}
	return base + issueID + suffix
}

// agentIssuesListPath returns /api/issues or /api/agent/issues (optional query already encoded).
func agentIssuesListPath(cmd *cobra.Command, query string) string {
	base := "/api/issues"
	if isAgentAPIToken(cmd) {
		base = "/api/agent/issues"
	}
	if query == "" {
		return base
	}
	return base + "?" + query
}

// agentChannelsListPathAmbient is the principal-aware channel list URL for
// resolvers without *cobra.Command (ambient mat_* / human).
func agentChannelsListPathAmbient(client *cli.APIClient) string {
	if isAgentAPITokenAmbient() {
		return "/api/agent/channels"
	}
	path := "/api/channels"
	if client != nil && client.WorkspaceID != "" {
		path += "?" + url.Values{"workspace_id": {client.WorkspaceID}}.Encode()
	}
	return path
}

// agentAgentsListPathAmbient is the principal-aware agent list URL for resolvers.
func agentAgentsListPathAmbient(client *cli.APIClient) string {
	if isAgentAPITokenAmbient() {
		return "/api/agent/agents"
	}
	path := "/api/agents"
	if client != nil && client.WorkspaceID != "" {
		path += "?" + url.Values{"workspace_id": {client.WorkspaceID}}.Encode()
	}
	return path
}

// agentProjectResourcesPathAmbient lists project resources for resolvers.
func agentProjectResourcesPathAmbient(projectID string) string {
	escaped := url.PathEscape(projectID)
	if isAgentAPITokenAmbient() {
		return "/api/agent/projects/" + escaped + "/resources"
	}
	return "/api/projects/" + escaped + "/resources"
}
