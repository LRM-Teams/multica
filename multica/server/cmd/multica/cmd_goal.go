package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var goalCmd = &cobra.Command{
	Use:   "goal",
	Short: "Read and maintain a channel's sustained goal",
}

var goalGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get the current channel goal",
	RunE:  runGoalGet,
}

var goalCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a sustained goal (channel managers only for agent tokens)",
	RunE:  runGoalCreate,
}

var goalCheckpointCmd = &cobra.Command{
	Use:   "checkpoint",
	Short: "Save progress against the active channel goal",
	RunE:  runGoalCheckpoint,
}

var goalUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Revise or change the lifecycle of a goal (channel managers only)",
	RunE:  runGoalUpdate,
}

var goalProcessCmd = &cobra.Command{
	Use:   "process",
	Short: "Read and write per-manager process Markdown under the current goal",
}

var goalProcessListCmd = &cobra.Command{
	Use:   "list",
	Short: "List process Markdown documents for the current channel goal",
	RunE:  runGoalProcessList,
}

var goalProcessGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get one manager's process Markdown",
	RunE:  runGoalProcessGet,
}

var goalProcessPutCmd = &cobra.Command{
	Use:   "put",
	Short: "Create or update process Markdown (expected_version=0 creates)",
	RunE:  runGoalProcessPut,
}

func init() {
	goalCmd.AddCommand(goalGetCmd, goalCreateCmd, goalCheckpointCmd, goalUpdateCmd, goalProcessCmd)
	goalProcessCmd.AddCommand(goalProcessListCmd, goalProcessGetCmd, goalProcessPutCmd)
	for _, cmd := range []*cobra.Command{goalGetCmd, goalCreateCmd, goalCheckpointCmd, goalUpdateCmd, goalProcessListCmd, goalProcessGetCmd, goalProcessPutCmd} {
		cmd.Flags().String("channel", "", "Channel id or #name")
		cmd.Flags().String("output", "json", "Output format (json)")
		_ = cmd.MarkFlagRequired("channel")
	}
	goalCreateCmd.Flags().String("title", "", "Short goal title")
	goalCreateCmd.Flags().String("objective", "", "Outcome the team must achieve")
	goalCreateCmd.Flags().StringSlice("criterion", nil, "Success criterion (repeatable)")
	_ = goalCreateCmd.MarkFlagRequired("title")
	_ = goalCreateCmd.MarkFlagRequired("objective")
	goalCheckpointCmd.Flags().Int64("expected-version", 0, "Current goal version")
	goalCheckpointCmd.Flags().String("progress", "", "Concrete progress made")
	goalCheckpointCmd.Flags().String("current-step", "", "Next or current step")
	goalCheckpointCmd.Flags().String("blocker", "", "Current blocker, empty when unblocked")
	goalCheckpointCmd.Flags().StringSlice("evidence", nil, "Evidence reference (repeatable)")
	goalCheckpointCmd.Flags().StringSlice("completed-criterion", nil, "Completed criterion text (repeatable)")
	_ = goalCheckpointCmd.MarkFlagRequired("expected-version")
	goalUpdateCmd.Flags().Int64("expected-version", 0, "Current goal version")
	goalUpdateCmd.Flags().String("title", "", "Replacement title")
	goalUpdateCmd.Flags().String("objective", "", "Replacement objective")
	goalUpdateCmd.Flags().StringSlice("criterion", nil, "Replacement success criteria (repeatable)")
	goalUpdateCmd.Flags().String("status", "", "Lifecycle status: active, paused, completed, or cancelled")
	_ = goalUpdateCmd.MarkFlagRequired("expected-version")
	goalProcessGetCmd.Flags().String("agent", "", "Manager agent id")
	_ = goalProcessGetCmd.MarkFlagRequired("agent")
	goalProcessPutCmd.Flags().String("agent", "", "Manager agent id (required for human tokens; agents default to self)")
	goalProcessPutCmd.Flags().Int64("expected-version", 0, "0 creates; otherwise current process version")
	goalProcessPutCmd.Flags().String("content", "", "Markdown body")
	goalProcessPutCmd.Flags().String("content-file", "", "Read Markdown body from file (- for stdin)")
}

func goalRequestContext(cmd *cobra.Command) (*cli.APIClient, context.Context, context.CancelFunc, string, error) {
	client, err := newAPIClient(cmd)
	if err != nil {
		return nil, nil, nil, "", err
	}
	ctx, cancel := cli.APIContext(context.Background())
	target, _ := cmd.Flags().GetString("channel")
	channelID, err := resolveChannelRef(ctx, client, strings.TrimPrefix(target, "#"))
	if err != nil {
		cancel()
		return nil, nil, nil, "", err
	}
	return client, ctx, cancel, channelID, nil
}

func printGoalResponse(out any) error {
	payload, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(payload))
	return nil
}

func runGoalGet(cmd *cobra.Command, _ []string) error {
	client, ctx, cancel, channelID, err := goalRequestContext(cmd)
	if err != nil {
		return err
	}
	defer cancel()
	path := "/api/channels/" + url.PathEscape(channelID) + "/goal"
	if isAgentAPIToken(cmd) {
		path = "/api/agent/channels/" + url.PathEscape(channelID) + "/goal"
	}
	var out map[string]any
	if err := client.GetJSON(ctx, path, &out); err != nil {
		return fmt.Errorf("get channel goal: %w", err)
	}
	return printGoalResponse(out)
}

func runGoalCreate(cmd *cobra.Command, _ []string) error {
	client, ctx, cancel, channelID, err := goalRequestContext(cmd)
	if err != nil {
		return err
	}
	defer cancel()
	title, _ := cmd.Flags().GetString("title")
	objective, _ := cmd.Flags().GetString("objective")
	criteria, _ := cmd.Flags().GetStringSlice("criterion")
	if len(criteria) == 0 {
		return fmt.Errorf("at least one --criterion is required")
	}
	path := "/api/channels/" + url.PathEscape(channelID) + "/goal"
	if isAgentAPIToken(cmd) {
		path = "/api/agent/channels/" + url.PathEscape(channelID) + "/goal"
	}
	var out map[string]any
	if err := client.PostJSON(ctx, path, map[string]any{
		"title": title, "objective": objective, "success_criteria": criteria,
	}, &out); err != nil {
		return fmt.Errorf("create channel goal: %w", err)
	}
	return printGoalResponse(out)
}

func runGoalCheckpoint(cmd *cobra.Command, _ []string) error {
	if !isAgentAPIToken(cmd) {
		return fmt.Errorf("goal checkpoint requires an active agent task token")
	}
	client, ctx, cancel, channelID, err := goalRequestContext(cmd)
	if err != nil {
		return err
	}
	defer cancel()
	version, _ := cmd.Flags().GetInt64("expected-version")
	progress, _ := cmd.Flags().GetString("progress")
	currentStep, _ := cmd.Flags().GetString("current-step")
	blocker, _ := cmd.Flags().GetString("blocker")
	evidence, _ := cmd.Flags().GetStringSlice("evidence")
	completed, _ := cmd.Flags().GetStringSlice("completed-criterion")
	var out map[string]any
	if err := client.PostJSON(ctx,
		"/api/agent/channels/"+url.PathEscape(channelID)+"/goal/checkpoint",
		map[string]any{
			"expected_version": version, "progress_summary": progress,
			"current_step": currentStep, "blocker": blocker,
			"evidence_refs": evidence, "completed_criteria": completed,
		}, &out); err != nil {
		return fmt.Errorf("checkpoint channel goal: %w", err)
	}
	return printGoalResponse(out)
}

func runGoalUpdate(cmd *cobra.Command, _ []string) error {
	if !isAgentAPIToken(cmd) {
		return fmt.Errorf("agent goal update requires an active agent task token")
	}
	client, ctx, cancel, channelID, err := goalRequestContext(cmd)
	if err != nil {
		return err
	}
	defer cancel()
	body, err := goalUpdateBody(cmd)
	if err != nil {
		return err
	}
	var out map[string]any
	if err := client.PatchJSON(ctx, "/api/agent/channels/"+url.PathEscape(channelID)+"/goal", body, &out); err != nil {
		return fmt.Errorf("update channel goal: %w", err)
	}
	return printGoalResponse(out)
}

func goalUpdateBody(cmd *cobra.Command) (map[string]any, error) {
	version, _ := cmd.Flags().GetInt64("expected-version")
	title, _ := cmd.Flags().GetString("title")
	objective, _ := cmd.Flags().GetString("objective")
	criteria, _ := cmd.Flags().GetStringSlice("criterion")
	status, _ := cmd.Flags().GetString("status")
	body := map[string]any{"expected_version": version}
	if cmd.Flags().Changed("title") {
		body["title"] = title
	}
	if cmd.Flags().Changed("objective") {
		body["objective"] = objective
	}
	if cmd.Flags().Changed("criterion") {
		body["success_criteria"] = criteria
	}
	if cmd.Flags().Changed("status") {
		body["status"] = status
	}
	if len(body) == 1 {
		return nil, fmt.Errorf("provide at least one of --title, --objective, --criterion, or --status")
	}
	return body, nil
}

func goalProcessAPIPath(cmd *cobra.Command, channelID, agentID string) string {
	base := "/api/channels/" + url.PathEscape(channelID) + "/goal/process"
	if isAgentAPIToken(cmd) {
		base = "/api/agent/channels/" + url.PathEscape(channelID) + "/goal/process"
	}
	if agentID == "" {
		return base
	}
	return base + "/" + url.PathEscape(agentID)
}

func runGoalProcessList(cmd *cobra.Command, _ []string) error {
	client, ctx, cancel, channelID, err := goalRequestContext(cmd)
	if err != nil {
		return err
	}
	defer cancel()
	var out map[string]any
	if err := client.GetJSON(ctx, goalProcessAPIPath(cmd, channelID, ""), &out); err != nil {
		return fmt.Errorf("list goal process markdown: %w", err)
	}
	return printGoalResponse(out)
}

func runGoalProcessGet(cmd *cobra.Command, _ []string) error {
	client, ctx, cancel, channelID, err := goalRequestContext(cmd)
	if err != nil {
		return err
	}
	defer cancel()
	agentID, _ := cmd.Flags().GetString("agent")
	var out map[string]any
	if err := client.GetJSON(ctx, goalProcessAPIPath(cmd, channelID, agentID), &out); err != nil {
		return fmt.Errorf("get goal process markdown: %w", err)
	}
	return printGoalResponse(out)
}

func runGoalProcessPut(cmd *cobra.Command, _ []string) error {
	client, ctx, cancel, channelID, err := goalRequestContext(cmd)
	if err != nil {
		return err
	}
	defer cancel()
	agentID, _ := cmd.Flags().GetString("agent")
	if !isAgentAPIToken(cmd) && strings.TrimSpace(agentID) == "" {
		return fmt.Errorf("--agent is required for human tokens")
	}
	content, err := goalProcessContent(cmd)
	if err != nil {
		return err
	}
	version, _ := cmd.Flags().GetInt64("expected-version")
	path := goalProcessAPIPath(cmd, channelID, agentID)
	if isAgentAPIToken(cmd) && strings.TrimSpace(agentID) == "" {
		path = goalProcessAPIPath(cmd, channelID, "")
	}
	var out map[string]any
	if err := client.PutJSON(ctx, path, map[string]any{
		"content": content, "expected_version": version,
	}, &out); err != nil {
		return fmt.Errorf("put goal process markdown: %w", err)
	}
	return printGoalResponse(out)
}

func goalProcessContent(cmd *cobra.Command) (string, error) {
	contentFile, _ := cmd.Flags().GetString("content-file")
	content, _ := cmd.Flags().GetString("content")
	if contentFile != "" && cmd.Flags().Changed("content") {
		return "", fmt.Errorf("provide either --content or --content-file, not both")
	}
	if contentFile != "" {
		data, err := readGoalProcessFile(contentFile)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	if !cmd.Flags().Changed("content") {
		return "", fmt.Errorf("--content or --content-file is required")
	}
	return content, nil
}

func readGoalProcessFile(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}
