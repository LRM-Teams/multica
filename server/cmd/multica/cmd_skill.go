package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var skillCmd = &cobra.Command{Use: "skill", Short: "Inspect skills"}

var skillListCmd = &cobra.Command{Use: "list", Short: "List skills in the workspace", RunE: runSkillList}

var skillGetCmd = &cobra.Command{Use: "get <id>", Short: "Get skill details (includes files)", Args: exactArgs(1), RunE: runSkillGet}

func init() {
	skillCmd.AddCommand(skillListCmd, skillGetCmd)
	skillListCmd.Flags().String("output", "table", "Output format: table or json")
	skillGetCmd.Flags().String("output", "json", "Output format: table or json")
}

func runSkillList(cmd *cobra.Command, _ []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var skills []map[string]any
	if err := client.GetJSON(ctx, "/api/skills", &skills); err != nil {
		return fmt.Errorf("list skills: %w", err)
	}
	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, skills)
	}
	rows := make([][]string, 0, len(skills))
	for _, s := range skills {
		rows = append(rows, []string{strVal(s, "id"), strVal(s, "name"), strVal(s, "description"), strVal(s, "created_at")})
	}
	cli.PrintTable(os.Stdout, []string{"ID", "NAME", "DESCRIPTION", "CREATED_AT"}, rows)
	return nil
}

func runSkillGet(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var skill map[string]any
	if err := client.GetJSON(ctx, "/api/skills/"+args[0], &skill); err != nil {
		return fmt.Errorf("get skill: %w", err)
	}
	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, skill)
	}
	rows := [][]string{{strVal(skill, "id"), strVal(skill, "name"), strVal(skill, "description"), strVal(skill, "created_at")}}
	cli.PrintTable(os.Stdout, []string{"ID", "NAME", "DESCRIPTION", "CREATED_AT"}, rows)
	return nil
}
