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

var researchTaskResultCmd = &cobra.Command{
	Use:   "task-result <session-id> <task-id> <attempt-id>",
	Short: "Submit the structured result for an assigned Research Run task",
	Args:  exactArgs(3),
	RunE:  runResearchTaskResult,
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
	Short: "Hire a fleet member (pending prompt review; lead only)",
	RunE:  runResearchHire,
}

var researchOptimizeCmd = &cobra.Command{
	Use:   "optimize <member-id>",
	Short: "Rewrite member instructions/model and optionally activate (lead only)",
	Args:  exactArgs(1),
	RunE:  runResearchOptimize,
}

var researchArchiveCmd = &cobra.Command{
	Use:   "archive <member-id>",
	Short: "Archive (减员) a fleet member; cancels wakes (lead only)",
	Args:  exactArgs(1),
	RunE:  runResearchArchive,
}

func init() {
	researchSessionGetCmd.Flags().String("attempt-id", "", "Assigned Research Run attempt ID for frozen task context")
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
	researchSourceUpsertCmd.Flags().String("why", "", "why this source (routing rationale / dimension)")
	researchSourceUpsertCmd.Flags().String("dimension", "", "dimension_family this source serves")

	researchReportPatchCmd.Flags().String("content", "", "markdown content")
	researchPresenceCmd.Flags().String("activity", "", "activity text")
	researchMessageCmd.Flags().String("body", "", "message body")
	researchMessageCmd.Flags().String("target", "", "optional target agent id")
	_ = researchMessageCmd.MarkFlagRequired("body")
	researchReportToLeadCmd.Flags().String("body", "", "message body for 罗纳尔多")
	_ = researchReportToLeadCmd.MarkFlagRequired("body")
	researchHireCmd.Flags().String("name", "", "agent name")
	researchHireCmd.Flags().String("role", "", "fleet role (unique among non-archived)")
	researchHireCmd.Flags().String("description", "", "description")
	researchHireCmd.Flags().String("instructions", "", "initial instructions (rewritten on optimize)")
	researchHireCmd.Flags().String("model", "", "specialty model (defaults to runtime explicit model)")
	researchHireCmd.Flags().String("reason", "", "specialty gap / why hire (required unless --fixture)")
	researchHireCmd.Flags().Bool("fixture", false, "capacity/409 fixture hire (skips canvas projection; set X-Research-Roster-Fixture)")
	_ = researchHireCmd.MarkFlagRequired("name")
	_ = researchHireCmd.MarkFlagRequired("role")
	researchOptimizeCmd.Flags().String("instructions", "", "new instructions")
	researchOptimizeCmd.Flags().String("model", "", "optional model override")
	researchOptimizeCmd.Flags().String("reason", "", "why optimize (audit + canvas)")
	researchOptimizeCmd.Flags().Bool("activate", true, "activate after optimize")
	_ = researchOptimizeCmd.MarkFlagRequired("instructions")
	researchArchiveCmd.Flags().String("reason", "", "why archive / 减员 (audit + canvas)")
	researchArchiveCmd.Flags().Bool("fixture", false, "capacity fixture cleanup (bypasses shell anti-churn)")
	researchTaskResultCmd.Flags().String("file", "", "JSON result file path, or - for stdin")
	_ = researchTaskResultCmd.MarkFlagRequired("file")

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
	researchCmd.AddCommand(researchArchiveCmd)
	researchCmd.AddCommand(researchTaskResultCmd)
}

// researchAPIPath rewrites /api/research/... → /api/agent/research/... under mat_*.
func researchAPIPath(cmd *cobra.Command, path string) string {
	if !isAgentAPIToken(cmd) {
		return path
	}
	const prefix = "/api/research"
	if strings.HasPrefix(path, prefix) {
		return "/api/agent/research" + path[len(prefix):]
	}
	return path
}

func runResearchSessionGet(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var out map[string]any
	path := "/api/research/sessions/" + args[0]
	attemptID, _ := cmd.Flags().GetString("attempt-id")
	if attemptID = strings.TrimSpace(attemptID); attemptID != "" {
		path += "?attempt_id=" + url.QueryEscape(attemptID)
	}
	if err := client.GetJSON(ctx, researchAPIPath(cmd, path), &out); err != nil {
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
	why, _ := cmd.Flags().GetString("why")
	dimension, _ := cmd.Flags().GetString("dimension")
	body := map[string]any{
		"url":                url,
		"title":              title,
		"source_class":       class,
		"credibility_weight": weight,
		"summary":            summary,
	}
	if why != "" {
		body["why"] = why
	}
	if dimension != "" {
		body["dimension_family"] = dimension
	}
	return researchPostJSON(cmd, "/api/research/sessions/"+args[0]+"/sources", body)
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

func runResearchTaskResult(cmd *cobra.Command, args []string) error {
	path, _ := cmd.Flags().GetString("file")
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), (2<<20)+1))
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return fmt.Errorf("read research result: %w", err)
	}
	if len(raw) > 2<<20 {
		return fmt.Errorf("research result exceeds 2 MiB")
	}
	if !json.Valid(raw) {
		return fmt.Errorf("research result is not valid JSON")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var out map[string]any
	path = fmt.Sprintf("/api/agent/research/sessions/%s/tasks/%s/attempts/%s/result", args[0], args[1], args[2])
	if err = client.PostJSON(ctx, path, json.RawMessage(raw), &out); err != nil {
		return fmt.Errorf("submit research result: %w", err)
	}
	return cli.PrintJSON(os.Stdout, out)
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
	if err := client.GetJSON(ctx, researchAPIPath(cmd, "/api/research/sessions/"+args[0]), &snap); err != nil {
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
	instructions, _ := cmd.Flags().GetString("instructions")
	model, _ := cmd.Flags().GetString("model")
	reason, _ := cmd.Flags().GetString("reason")
	fixture, _ := cmd.Flags().GetBool("fixture")
	body := map[string]any{
		"name": name, "role": role, "description": desc,
	}
	if instructions != "" {
		body["instructions"] = instructions
	}
	if model != "" {
		body["model"] = model
	}
	if reason != "" {
		body["reason"] = reason
	}
	if fixture {
		body["fixture"] = true
	}
	return researchPostJSON(cmd, "/api/research/fleet/members", body)
}

func runResearchOptimize(cmd *cobra.Command, args []string) error {
	instructions, _ := cmd.Flags().GetString("instructions")
	activate, _ := cmd.Flags().GetBool("activate")
	model, _ := cmd.Flags().GetString("model")
	reason, _ := cmd.Flags().GetString("reason")
	body := map[string]any{
		"instructions": instructions,
		"activate":     activate,
	}
	if model != "" {
		body["model"] = model
	}
	if reason != "" {
		body["reason"] = reason
	}
	return researchPostJSON(cmd, "/api/research/fleet/members/"+args[0]+"/optimize", body)
}

func runResearchArchive(cmd *cobra.Command, args []string) error {
	reason, _ := cmd.Flags().GetString("reason")
	fixture, _ := cmd.Flags().GetBool("fixture")
	body := map[string]any{}
	if reason != "" {
		body["reason"] = reason
	}
	if fixture {
		body["fixture"] = true
	}
	return researchPostJSON(cmd, "/api/research/fleet/members/"+args[0]+"/archive", body)
}

func researchPostJSON(cmd *cobra.Command, path string, body map[string]any) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var out map[string]any
	if err := client.PostJSON(ctx, researchAPIPath(cmd, path), body, &out); err != nil {
		return err
	}
	return cli.PrintJSON(os.Stdout, out)
}
