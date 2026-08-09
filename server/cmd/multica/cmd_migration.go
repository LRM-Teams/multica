package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var migrationCmd = &cobra.Command{
	Use:   "migration",
	Short: "Reserve and manage database migration numbers",
	Long: "Reserve a migration number before adding server/migrations/<N>_*.sql. " +
		"Never guess the next number from a local listing — reserve first so open PRs cannot collide.",
}

var migrationReserveCmd = &cobra.Command{
	Use:   "reserve",
	Short: "Reserve a migration number (MigrationLease)",
	RunE:  runMigrationReserve,
}

var migrationReleaseCmd = &cobra.Command{
	Use:   "release",
	Short: "Release a reserved migration number you own",
	RunE:  runMigrationRelease,
}

var migrationListCmd = &cobra.Command{
	Use:   "list",
	Short: "List active reserved migration leases in the workspace",
	RunE:  runMigrationList,
}

func init() {
	migrationCmd.AddCommand(migrationReserveCmd, migrationReleaseCmd, migrationListCmd)

	migrationReserveCmd.Flags().Int("number", 0, "Migration number to reserve")
	migrationReserveCmd.Flags().String("filename", "", "Target filename (e.g. 310_migration_lease.up.sql)")
	migrationReserveCmd.Flags().String("issue-id", "", "Related issue UUID")
	migrationReserveCmd.Flags().Int("pr-number", 0, "Related pull request number")
	migrationReserveCmd.Flags().Int("ttl-hours", 72, "Lease TTL in hours (max 336)")
	migrationReserveCmd.Flags().String("output", "json", "Output format: json")

	migrationReleaseCmd.Flags().Int("number", 0, "Migration number to release")
	migrationReleaseCmd.Flags().String("lease-id", "", "Lease UUID to release")
	migrationReleaseCmd.Flags().String("output", "json", "Output format: json")

	migrationListCmd.Flags().String("output", "json", "Output format: json")
}

func runMigrationReserve(cmd *cobra.Command, _ []string) error {
	number, _ := cmd.Flags().GetInt("number")
	filename, _ := cmd.Flags().GetString("filename")
	if number <= 0 && strings.TrimSpace(filename) == "" {
		return fmt.Errorf("provide --number and/or --filename")
	}
	body := map[string]any{}
	if number > 0 {
		body["migration_number"] = number
	}
	if strings.TrimSpace(filename) != "" {
		body["filename"] = strings.TrimSpace(filename)
	}
	if issueID := strings.TrimSpace(flagString(cmd, "issue-id")); issueID != "" {
		body["issue_id"] = issueID
	}
	if pr, _ := cmd.Flags().GetInt("pr-number"); pr > 0 {
		body["pr_number"] = pr
	}
	if ttl, _ := cmd.Flags().GetInt("ttl-hours"); ttl > 0 {
		body["ttl_hours"] = ttl
	}
	return postMigration(cmd, "/api/agent/migrations/reserve", body)
}

func runMigrationRelease(cmd *cobra.Command, _ []string) error {
	number, _ := cmd.Flags().GetInt("number")
	leaseID := strings.TrimSpace(flagString(cmd, "lease-id"))
	if number <= 0 && leaseID == "" {
		return fmt.Errorf("provide --number or --lease-id")
	}
	body := map[string]any{}
	if number > 0 {
		body["migration_number"] = number
	}
	if leaseID != "" {
		body["lease_id"] = leaseID
	}
	return postMigration(cmd, "/api/agent/migrations/release", body)
}

func runMigrationList(cmd *cobra.Command, _ []string) error {
	return postMigration(cmd, "/api/agent/migrations/list", map[string]any{})
}

func postMigration(cmd *cobra.Command, path string, body map[string]any) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var response any
	if err := client.PostJSON(ctx, path, body, &response); err != nil {
		return fmt.Errorf("migration: %w", err)
	}
	encoded, _ := json.MarshalIndent(response, "", "  ")
	fmt.Println(string(encoded))
	return nil
}
