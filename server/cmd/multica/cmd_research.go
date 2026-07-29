package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var researchCmd = &cobra.Command{
	Use:   "research",
	Short: "Research Fleet tools for 罗纳尔多 and fleet members",
}

var researchSessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Research session operations",
}

var researchSessionGetCmd = &cobra.Command{
	Use:   "get <session-id>",
	Short: "Get research session snapshot",
	Args:  exactArgs(1),
	RunE:  runResearchSessionGet,
}

var researchGraphAppendCmd = &cobra.Command{
	Use:   "graph-append <session-id>",
	Short: "Append an exploration graph node",
	Args:  exactArgs(1),
	RunE:  runResearchGraphAppend,
}

var researchSourceUpsertCmd = &cobra.Command{
	Use:   "source-upsert <session-id>",
	Short: "Upsert a weighted research source",
	Args:  exactArgs(1),
	RunE:  runResearchSourceUpsert,
}

var researchReportPatchCmd = &cobra.Command{
	Use:   "report-patch <session-id>",
	Short: "Patch / revise the research report",
	Args:  exactArgs(1),
	RunE:  runResearchReportPatch,
}

var researchStageEvalCmd = &cobra.Command{
	Use:   "stage-eval <session-id>",
	Short: "Request stage evaluation (lead only)",
	Args:  exactArgs(1),
	RunE:  runResearchStageEval,
}

var researchPresenceCmd = &cobra.Command{
	Use:   "presence <session-id>",
	Short: "Publish presence activity",
	Args:  exactArgs(1),
	RunE:  runResearchPresence,
}

var researchMessageCmd = &cobra.Command{
	Use:   "message <session-id>",
	Short: "Post a research session message (optionally target an agent)",
	Args:  exactArgs(1),
	RunE:  runResearchMessage,
}

var researchReportToLeadCmd = &cobra.Command{
	Use:   "report-to-lead <session-id>",
	Short: "Post a note targeting the research lead (罗纳尔多)",
	Args:  exactArgs(1),
	RunE:  runResearchReportToLead,
}

var researchHireCmd = &cobra.Command{
	Use:   "hire",
	Short: "Hire a fleet member (pending prompt review)",
	RunE:  runResearchHire,
}

var researchOptimizeCmd = &cobra.Command{
	Use:   "optimize <member-id>",
	Short: "Rewrite member instructions and optionally activate",
	Args:  exactArgs(1),
	RunE:  runResearchOptimize,
}

func init() {
	researchGraphAppendCmd.Flags().String("type", "probe", "node type")
	researchGraphAppendCmd.Flags().String("title", "", "node title")
	researchGraphAppendCmd.Flags().String("summary", "", "node summary")
	researchGraphAppendCmd.Flags().String("from", "", "from node id")
	researchGraphAppendCmd.Flags().String("edge", "leads_to", "edge type")

	researchSourceUpsertCmd.Flags().String("url", "", "source url")
	researchSourceUpsertCmd.Flags().String("title", "", "source title")
	researchSourceUpsertCmd.Flags().String("class", "other", "source class")
	researchSourceUpsertCmd.Flags().Float64("weight", 0.5, "credibility weight 0-1")
	researchSourceUpsertCmd.Flags().String("summary", "", "summary")

	researchReportPatchCmd.Flags().String("content", "", "markdown content")
	researchPresenceCmd.Flags().String("activity", "", "activity text")
	researchMessageCmd.Flags().String("body", "", "message body")
	researchMessageCmd.Flags().String("target", "", "optional target agent id")
	_ = researchMessageCmd.MarkFlagRequired("body")
	researchReportToLeadCmd.Flags().String("body", "", "message body for 罗纳尔多")
	_ = researchReportToLeadCmd.MarkFlagRequired("body")
	researchHireCmd.Flags().String("name", "", "agent name")
	researchHireCmd.Flags().String("role", "", "fleet role")
	researchHireCmd.Flags().String("description", "", "description")
	_ = researchHireCmd.MarkFlagRequired("name")
	_ = researchHireCmd.MarkFlagRequired("role")
	researchOptimizeCmd.Flags().String("instructions", "", "new instructions")
	researchOptimizeCmd.Flags().Bool("activate", true, "activate after optimize")
	_ = researchOptimizeCmd.MarkFlagRequired("instructions")

	researchSessionCmd.AddCommand(researchSessionGetCmd)
	researchCmd.AddCommand(researchSessionCmd)
	researchCmd.AddCommand(researchGraphAppendCmd)
	researchCmd.AddCommand(researchSourceUpsertCmd)
	researchCmd.AddCommand(researchReportPatchCmd)
	researchCmd.AddCommand(researchStageEvalCmd)
	researchCmd.AddCommand(researchPresenceCmd)
	researchCmd.AddCommand(researchMessageCmd)
	researchCmd.AddCommand(researchReportToLeadCmd)
	researchCmd.AddCommand(researchHireCmd)
	researchCmd.AddCommand(researchOptimizeCmd)
}

func runResearchSessionGet(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var out map[string]any
	if err := client.GetJSON(ctx, "/api/research/sessions/"+args[0], &out); err != nil {
		return fmt.Errorf("get research session: %w", err)
	}
	return cli.PrintJSON(os.Stdout, out)
}

func runResearchGraphAppend(cmd *cobra.Command, args []string) error {
	nodeType, _ := cmd.Flags().GetString("type")
	title, _ := cmd.Flags().GetString("title")
	summary, _ := cmd.Flags().GetString("summary")
	fromID, _ := cmd.Flags().GetString("from")
	edgeType, _ := cmd.Flags().GetString("edge")
	body := map[string]any{
		"node_type": nodeType,
		"title":     title,
		"summary":   summary,
	}
	if fromID != "" {
		body["from_node_id"] = fromID
		body["edge_type"] = edgeType
	}
	return researchPostJSON(cmd, "/api/research/sessions/"+args[0]+"/graph/nodes", body)
}

func runResearchSourceUpsert(cmd *cobra.Command, args []string) error {
	url, _ := cmd.Flags().GetString("url")
	title, _ := cmd.Flags().GetString("title")
	class, _ := cmd.Flags().GetString("class")
	weight, _ := cmd.Flags().GetFloat64("weight")
	summary, _ := cmd.Flags().GetString("summary")
	return researchPostJSON(cmd, "/api/research/sessions/"+args[0]+"/sources", map[string]any{
		"url":                url,
		"title":              title,
		"source_class":       class,
		"credibility_weight": weight,
		"summary":            summary,
	})
}

func runResearchReportPatch(cmd *cobra.Command, args []string) error {
	content, _ := cmd.Flags().GetString("content")
	return researchPostJSON(cmd, "/api/research/sessions/"+args[0]+"/report", map[string]any{
		"content_md":   content,
		"new_revision": true,
	})
}

func runResearchStageEval(cmd *cobra.Command, args []string) error {
	return researchPostJSON(cmd, "/api/research/sessions/"+args[0]+"/stage-eval", map[string]any{})
}

func runResearchPresence(cmd *cobra.Command, args []string) error {
	activity, _ := cmd.Flags().GetString("activity")
	return researchPostJSON(cmd, "/api/research/sessions/"+args[0]+"/presence", map[string]any{
		"activity": activity,
	})
}

func runResearchMessage(cmd *cobra.Command, args []string) error {
	body, _ := cmd.Flags().GetString("body")
	target, _ := cmd.Flags().GetString("target")
	payload := map[string]any{"body": body}
	if target != "" {
		payload["target_agent_id"] = target
	}
	return researchPostJSON(cmd, "/api/research/sessions/"+args[0]+"/messages", payload)
}

func runResearchReportToLead(cmd *cobra.Command, args []string) error {
	body, _ := cmd.Flags().GetString("body")
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var snap map[string]any
	if err := client.GetJSON(ctx, "/api/research/sessions/"+args[0], &snap); err != nil {
		return fmt.Errorf("get research session: %w", err)
	}
	fleet, _ := snap["fleet"].(map[string]any)
	leadID, _ := fleet["lead_agent_id"].(string)
	payload := map[string]any{"body": body}
	if leadID != "" {
		payload["target_agent_id"] = leadID
	}
	return researchPostJSON(cmd, "/api/research/sessions/"+args[0]+"/messages", payload)
}

func runResearchHire(cmd *cobra.Command, args []string) error {
	name, _ := cmd.Flags().GetString("name")
	role, _ := cmd.Flags().GetString("role")
	desc, _ := cmd.Flags().GetString("description")
	return researchPostJSON(cmd, "/api/research/fleet/members", map[string]any{
		"name": name, "role": role, "description": desc,
	})
}

func runResearchOptimize(cmd *cobra.Command, args []string) error {
	instructions, _ := cmd.Flags().GetString("instructions")
	activate, _ := cmd.Flags().GetBool("activate")
	return researchPostJSON(cmd, "/api/research/fleet/members/"+args[0]+"/optimize", map[string]any{
		"instructions": instructions,
		"activate":     activate,
	})
}

func researchPostJSON(cmd *cobra.Command, path string, body map[string]any) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var out map[string]any
	if err := client.PostJSON(ctx, path, body, &out); err != nil {
		return err
	}
	return cli.PrintJSON(os.Stdout, out)
}
