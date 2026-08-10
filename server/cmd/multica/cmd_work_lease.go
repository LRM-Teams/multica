package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var workLeaseCmd = &cobra.Command{
	Use:   "work-lease",
	Short: "Acquire and manage WorkOwnerLease for an issue",
	Long: "Acquire an executor/reviewer/coordinator lease before pushing a branch or opening a PR. " +
		"Issue task enqueue auto-acquires an executor lease for the assignee; conflicting agents must wait or take over explicitly.",
}

var workLeaseAcquireCmd = &cobra.Command{Use: "acquire", Short: "Acquire a work owner lease", RunE: runWorkLeaseAcquire}
var workLeaseReleaseCmd = &cobra.Command{Use: "release", Short: "Release a work owner lease you own", RunE: runWorkLeaseRelease}
var workLeaseListCmd = &cobra.Command{Use: "list", Short: "List active work owner leases", RunE: runWorkLeaseList}

func init() {
	workLeaseCmd.AddCommand(workLeaseAcquireCmd, workLeaseReleaseCmd, workLeaseListCmd)

	workLeaseAcquireCmd.Flags().String("issue-id", "", "Issue UUID")
	workLeaseAcquireCmd.Flags().String("role", "executor", "executor|reviewer|coordinator")
	workLeaseAcquireCmd.Flags().String("canonical-branch", "", "Canonical git branch for this lease")
	workLeaseAcquireCmd.Flags().String("conversation-id", "", "Conversation / lane id")
	workLeaseAcquireCmd.Flags().String("runtime-lane", "", "Runtime lane id")
	workLeaseAcquireCmd.Flags().StringSlice("allowed-path", nil, "Allowed path glob (repeatable)")
	workLeaseAcquireCmd.Flags().IntSlice("migration-number", nil, "Reserved migration numbers (repeatable)")
	workLeaseAcquireCmd.Flags().Int("ttl-hours", 72, "Lease TTL in hours")
	_ = workLeaseAcquireCmd.MarkFlagRequired("issue-id")

	workLeaseReleaseCmd.Flags().String("lease-id", "", "Lease UUID")
	workLeaseReleaseCmd.Flags().String("issue-id", "", "Issue UUID")
	workLeaseReleaseCmd.Flags().String("role", "executor", "Role to release when using --issue-id")

	workLeaseListCmd.Flags().String("issue-id", "", "Optional issue UUID filter")
}

func runWorkLeaseAcquire(cmd *cobra.Command, _ []string) error {
	body := map[string]any{
		"issue_id": strings.TrimSpace(flagString(cmd, "issue-id")),
		"role":     strings.TrimSpace(flagString(cmd, "role")),
	}
	if v := strings.TrimSpace(flagString(cmd, "canonical-branch")); v != "" {
		body["canonical_branch"] = v
	}
	if v := strings.TrimSpace(flagString(cmd, "conversation-id")); v != "" {
		body["conversation_id"] = v
	}
	if v := strings.TrimSpace(flagString(cmd, "runtime-lane")); v != "" {
		body["runtime_lane"] = v
	}
	if paths, _ := cmd.Flags().GetStringSlice("allowed-path"); len(paths) > 0 {
		body["allowed_paths"] = paths
	}
	if nums, _ := cmd.Flags().GetIntSlice("migration-number"); len(nums) > 0 {
		body["migration_numbers"] = nums
	}
	if ttl, _ := cmd.Flags().GetInt("ttl-hours"); ttl > 0 {
		body["ttl_hours"] = ttl
	}
	return postWorkLease(cmd, "/api/agent/work-leases/acquire", body)
}

func runWorkLeaseRelease(cmd *cobra.Command, _ []string) error {
	leaseID := strings.TrimSpace(flagString(cmd, "lease-id"))
	issueID := strings.TrimSpace(flagString(cmd, "issue-id"))
	if leaseID == "" && issueID == "" {
		return fmt.Errorf("provide --lease-id or --issue-id")
	}
	body := map[string]any{"role": strings.TrimSpace(flagString(cmd, "role"))}
	if leaseID != "" {
		body["lease_id"] = leaseID
	}
	if issueID != "" {
		body["issue_id"] = issueID
	}
	return postWorkLease(cmd, "/api/agent/work-leases/release", body)
}

func runWorkLeaseList(cmd *cobra.Command, _ []string) error {
	body := map[string]any{}
	if issueID := strings.TrimSpace(flagString(cmd, "issue-id")); issueID != "" {
		body["issue_id"] = issueID
	}
	return postWorkLease(cmd, "/api/agent/work-leases/list", body)
}

func postWorkLease(cmd *cobra.Command, path string, body map[string]any) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var response any
	if err := client.PostJSON(ctx, path, body, &response); err != nil {
		return fmt.Errorf("work-lease: %w", err)
	}
	encoded, _ := json.MarshalIndent(response, "", "  ")
	fmt.Println(string(encoded))
	return nil
}
