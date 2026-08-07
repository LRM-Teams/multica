package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/spf13/cobra"
)

var issueDecomposeCmd = &cobra.Command{
	Use:   "decompose <issue-id>",
	Short: "Atomically create independent child Issues and their dependencies",
	Args:  exactArgs(1),
	RunE:  runIssueDecompose,
}

func init() {
	issueCmd.AddCommand(issueDecomposeCmd)
	issueDecomposeCmd.Flags().String("plan-file", "", "JSON Issue decomposition plan")
	issueDecomposeCmd.Flags().String("idempotency-key", "", "Stable UUID for safe retry")
	_ = issueDecomposeCmd.MarkFlagRequired("plan-file")
	_ = issueDecomposeCmd.MarkFlagRequired("idempotency-key")
}

func runIssueDecompose(cmd *cobra.Command, args []string) error {
	planPath, _ := cmd.Flags().GetString("plan-file")
	key, _ := cmd.Flags().GetString("idempotency-key")
	data, err := os.ReadFile(planPath)
	if err != nil {
		return err
	}
	var body map[string]any
	if err = json.Unmarshal(data, &body); err != nil {
		return fmt.Errorf("decode plan: %w", err)
	}
	body["idempotency_key"] = key
	c, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var out any
	if err = c.PostJSON(ctx, "/api/agent/issues/"+url.PathEscape(args[0])+"/decompose", body, &out); err != nil {
		return err
	}
	return printGoalResponse(out)
}
