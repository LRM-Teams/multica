package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var issueGraphCmd = &cobra.Command{Use: "graph", Short: "Create and inspect executable issue work graphs"}
var issueGraphCreateCmd = &cobra.Command{Use: "create", Short: "Atomically create an executable graph and its declared issues", Args: cobra.NoArgs, RunE: runIssueGraphCreate}
var issueGraphGetCmd = &cobra.Command{Use: "get <graph-id>", Short: "Get a work graph", Args: exactArgs(1), RunE: runIssueGraphGet}
var issueGraphReconcileCmd = &cobra.Command{Use: "reconcile <graph-id>", Short: "Recompute ready nodes", Args: exactArgs(1), RunE: runIssueGraphReconcile}
var issueGraphInvalidateCmd = &cobra.Command{Use: "invalidate <graph-id> <node-id>", Short: "Invalidate a node and mark affected downstream nodes stale", Args: exactArgs(2), RunE: runIssueGraphInvalidate}
var issueGraphReviseCmd = &cobra.Command{Use: "revise <graph-id>", Short: "Create a new graph revision from a JSON plan", Args: exactArgs(1), RunE: runIssueGraphRevise}
var issueGraphArtifactCmd = &cobra.Command{Use: "artifact <graph-id>", Short: "Register an immutable artifact revision from JSON", Args: exactArgs(1), RunE: runIssueGraphArtifact}
var issueGraphVerificationCmd = &cobra.Command{Use: "verification <graph-id>", Short: "Submit PASS, FAIL, or BLOCKED verification JSON", Args: exactArgs(1), RunE: runIssueGraphVerification}

func init() {
	issueGraphCmd.AddCommand(issueGraphCreateCmd, issueGraphGetCmd, issueGraphReconcileCmd, issueGraphInvalidateCmd, issueGraphReviseCmd, issueGraphArtifactCmd, issueGraphVerificationCmd)
	issueCmd.AddCommand(issueGraphCmd)
	issueGraphCreateCmd.Flags().String("plan-file", "", "JSON graph plan file")
	issueGraphCreateCmd.Flags().String("idempotency-key", "", "Stable UUID for safe retry")
	_ = issueGraphCreateCmd.MarkFlagRequired("plan-file")
	_ = issueGraphCreateCmd.MarkFlagRequired("idempotency-key")
	issueGraphInvalidateCmd.Flags().String("reason", "", "Reason for invalidation")
	_ = issueGraphInvalidateCmd.MarkFlagRequired("reason")
	for _, command := range []*cobra.Command{issueGraphReviseCmd, issueGraphArtifactCmd, issueGraphVerificationCmd} {
		command.Flags().String("input-file", "", "JSON request file")
		_ = command.MarkFlagRequired("input-file")
	}
}

func readGraphInput(cmd *cobra.Command) (map[string]any, error) {
	path, _ := cmd.Flags().GetString("input-file")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var body map[string]any
	if err = json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("decode input: %w", err)
	}
	return body, nil
}
func postGraphInput(cmd *cobra.Command, path string) error {
	body, err := readGraphInput(cmd)
	if err != nil {
		return err
	}
	c, ctx, cancel, err := graphClient(cmd)
	if err != nil {
		return err
	}
	defer cancel()
	var out any
	if err = c.PostJSON(ctx, path, body, &out); err != nil {
		return err
	}
	return printGoalResponse(out)
}
func runIssueGraphRevise(cmd *cobra.Command, args []string) error {
	return postGraphInput(cmd, "/api/agent/work-graphs/"+url.PathEscape(args[0])+"/revisions")
}
func runIssueGraphArtifact(cmd *cobra.Command, args []string) error {
	return postGraphInput(cmd, "/api/agent/work-graphs/"+url.PathEscape(args[0])+"/artifacts")
}
func runIssueGraphVerification(cmd *cobra.Command, args []string) error {
	return postGraphInput(cmd, "/api/agent/work-graphs/"+url.PathEscape(args[0])+"/verifications")
}

func graphClient(cmd *cobra.Command) (*cli.APIClient, context.Context, context.CancelFunc, error) {
	c, err := newAPIClient(cmd)
	if err != nil {
		return nil, nil, nil, err
	}
	ctx, cancel := cli.APIContext(context.Background())
	return c, ctx, cancel, nil
}

func runIssueGraphCreate(cmd *cobra.Command, _ []string) error {
	path, _ := cmd.Flags().GetString("plan-file")
	key, _ := cmd.Flags().GetString("idempotency-key")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var body map[string]any
	if err = json.Unmarshal(data, &body); err != nil {
		return fmt.Errorf("decode plan: %w", err)
	}
	body["idempotency_key"] = key
	c, ctx, cancel, err := graphClient(cmd)
	if err != nil {
		return err
	}
	defer cancel()
	var out any
	if err = c.PostJSON(ctx, "/api/agent/work-graphs", body, &out); err != nil {
		return err
	}
	return printGoalResponse(out)
}
func runIssueGraphGet(cmd *cobra.Command, args []string) error {
	c, ctx, cancel, err := graphClient(cmd)
	if err != nil {
		return err
	}
	defer cancel()
	var out any
	if err = c.GetJSON(ctx, "/api/agent/work-graphs/"+url.PathEscape(args[0]), &out); err != nil {
		return err
	}
	return printGoalResponse(out)
}
func runIssueGraphReconcile(cmd *cobra.Command, args []string) error {
	c, ctx, cancel, err := graphClient(cmd)
	if err != nil {
		return err
	}
	defer cancel()
	var out any
	if err = c.PostJSON(ctx, "/api/agent/work-graphs/"+url.PathEscape(args[0])+"/reconcile", map[string]any{}, &out); err != nil {
		return err
	}
	return printGoalResponse(out)
}
func runIssueGraphInvalidate(cmd *cobra.Command, args []string) error {
	reason, _ := cmd.Flags().GetString("reason")
	c, ctx, cancel, err := graphClient(cmd)
	if err != nil {
		return err
	}
	defer cancel()
	var out any
	if err = c.PostJSON(ctx, "/api/agent/work-graphs/"+url.PathEscape(args[0])+"/nodes/"+url.PathEscape(args[1])+"/invalidate", map[string]any{"reason": reason}, &out); err != nil {
		return err
	}
	return printGoalResponse(out)
}
