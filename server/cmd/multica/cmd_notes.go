package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// notesCmd is the agent-facing product-note surface (S2-C2). Local agent
// memory files under ~/.multica/.../notes are unrelated — see
// docs/notes-editor-worker-contract.md.
var notesCmd = &cobra.Command{
	Use:   "notes",
	Short: "Read product notes (note_page) authorized for the current agent",
	Long: "Read Worker- or Notes-bubble-authorized product note pages. There is no `notes write` command; " +
		"`get` / `tree` being the read surface does not mean product notes cannot be proposed. " +
		"From a DM or channel, pipe cleaned markdown to `multica message send --target <target> --note-write`. " +
		"Omit `--note-page-id` to create a note after human confirm. " +
		"Period Work collectors deliver packs with `notes period-brief submit-pack` (not --note-write). " +
		"The Notes Assistant bubble opens the plan card with `notes period-brief plan` and inserts with `notes period-brief insert`. " +
		"The human starts collect from the plan card (开始采集). " +
		"The collect-plan wake delivers `notes period-brief submit-collect-plan`. " +
		"Period Brief synthesizers may call `notes period-brief retry-collectors` to re-dispatch retryable collectors.",
}

var notesGetCmd = &cobra.Command{
	Use:   "get <page-id>",
	Short: "Get one product note page the current agent task is allowed to read",
	Args:  cobra.ExactArgs(1),
	RunE:  runNotesGet,
}

var notesTreeCmd = &cobra.Command{
	Use:   "tree <page-id>",
	Short: "List a product note page and its descendants (ids + titles)",
	Args:  cobra.ExactArgs(1),
	RunE:  runNotesTree,
}

var notesPeriodBriefCmd = &cobra.Command{
	Use:   "period-brief",
	Short: "Period Work Brief tools (bubble plan/insert, collector pack, planner, retry)",
}

var notesPeriodBriefPlanCmd = &cobra.Command{
	Use:   "plan",
	Short: "Create or update this bubble session's current 写汇报 plan",
	Long: "Notes Assistant bubble tool. One current plan per chat session. " +
		"With only --chat-session-id, creates the visible plan card if none exists and prints it. " +
		"Pass window, collector, or focus flags to update it. " +
		"The plan card shows this plan. Do not emit compose fences.",
	Args: cobra.NoArgs,
	RunE: runNotesPeriodBriefPlan,
}

var notesPeriodBriefStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start 写汇报 collect from the Notes Assistant bubble",
	Long: "Notes Assistant bubble tool. Dispatches owned collectors for the confirmed plan. " +
		"Required: chat session and context note page. Omit window and collector ids to use " +
		"this session's current plan. Always walks every selected computer, then writes the brief. " +
		"A conflict means a run is already live — do not retry. Returns run_id after dispatch. " +
		"Do not emit chat XML to start collect. Prefer the human plan card (开始采集).",
	Args: cobra.NoArgs,
	RunE: runNotesPeriodBriefStart,
}

var notesPeriodBriefInsertCmd = &cobra.Command{
	Use:   "insert",
	Short: "Insert a finished Period Brief onto a writable note",
	Long: "Notes Assistant bubble tool. Inserts the finished brief (append or child). " +
		"When the target is not the issuing page the tool returns needs_confirm and the human confirms on the card.",
	Args: cobra.NoArgs,
	RunE: runNotesPeriodBriefInsert,
}

var notesPeriodBriefRetryCollectorsCmd = &cobra.Command{
	Use:   "retry-collectors",
	Short: "Re-dispatch retryable Period Work collectors for a draft (one retry each)",
	Long: "Narrow tool for the Period Brief synthesizer. Call once after a transient failure. " +
		"Platform skips permanent failures (missing API key / model config / auth / quota) and " +
		"collectors that already used their one retry. Inbox will not auto-retry. " +
		"After success, stop and wait — the platform re-wakes you when that attempt settles.",
	Args: cobra.NoArgs,
	RunE: runNotesPeriodBriefRetryCollectors,
}

var notesPeriodBriefSubmitCollectPlanCmd = &cobra.Command{
	Use:   "submit-collect-plan",
	Short: "Store a Notes Assistant collect plan on the Period Brief run",
	Long: "Planner-only tool. Reads JSON from stdin (or --json) and stores the collect plan " +
		"on note_period_brief_run.collect_plan. Do not --note-write. Do not submit-pack from this wake.",
	Args: cobra.NoArgs,
	RunE: runNotesPeriodBriefSubmitCollectPlan,
}

var notesPeriodBriefSubmitPackCmd = &cobra.Command{
	Use:   "submit-pack",
	Short: "Store a Period Work collector pack on the Brief run (not a Notes page)",
	Long: "Collector-only tool. Reads pack markdown from stdin (or --markdown) and stores it on " +
		"note_period_brief_run.collectors[].pack_markdown for the given draft. Do not --note-write packs into Notes.",
	Args: cobra.NoArgs,
	RunE: runNotesPeriodBriefSubmitPack,
}

func init() {
	notesCmd.AddCommand(notesGetCmd)
	notesCmd.AddCommand(notesTreeCmd)
	notesCmd.AddCommand(notesPeriodBriefCmd)
	notesPeriodBriefCmd.AddCommand(notesPeriodBriefPlanCmd)
	notesPeriodBriefCmd.AddCommand(notesPeriodBriefStartCmd)
	notesPeriodBriefCmd.AddCommand(notesPeriodBriefInsertCmd)
	notesPeriodBriefCmd.AddCommand(notesPeriodBriefRetryCollectorsCmd)
	notesPeriodBriefCmd.AddCommand(notesPeriodBriefSubmitPackCmd)
	notesPeriodBriefCmd.AddCommand(notesPeriodBriefSubmitCollectPlanCmd)
	notesPeriodBriefPlanCmd.Flags().String("chat-session-id", "", "Notes FAB chat_session id (required)")
	notesPeriodBriefPlanCmd.Flags().String("context-note-page-id", "", "Context note page id (optional)")
	notesPeriodBriefPlanCmd.Flags().String("window", "", "day | week | month | custom")
	notesPeriodBriefPlanCmd.Flags().String("date", "", "YYYY-MM-DD for day / week / month")
	notesPeriodBriefPlanCmd.Flags().String("start-date", "", "custom window start YYYY-MM-DD")
	notesPeriodBriefPlanCmd.Flags().String("end-date", "", "custom window end YYYY-MM-DD")
	notesPeriodBriefPlanCmd.Flags().StringSlice("collector-agent-id", nil, "Owned Period Work collector agent ids")
	notesPeriodBriefPlanCmd.Flags().String("focus", "", "Optional typed topic or path")
	_ = notesPeriodBriefPlanCmd.MarkFlagRequired("chat-session-id")
	notesPeriodBriefStartCmd.Flags().String("chat-session-id", "", "Notes FAB chat_session id (required)")
	notesPeriodBriefStartCmd.Flags().String("context-note-page-id", "", "Context note page id (required)")
	notesPeriodBriefStartCmd.Flags().String("window", "", "day | week | month | custom (omit to use the current plan)")
	notesPeriodBriefStartCmd.Flags().String("date", "", "YYYY-MM-DD for day / week / month")
	notesPeriodBriefStartCmd.Flags().String("start-date", "", "custom window start YYYY-MM-DD")
	notesPeriodBriefStartCmd.Flags().String("end-date", "", "custom window end YYYY-MM-DD")
	notesPeriodBriefStartCmd.Flags().String("timezone", "", "IANA timezone (optional)")
	notesPeriodBriefStartCmd.Flags().StringSlice("collector-agent-id", nil, "Owned Period Work collector agent ids (omit to use the current plan)")
	notesPeriodBriefStartCmd.Flags().String("focus", "", "Optional typed topic or path")
	_ = notesPeriodBriefStartCmd.MarkFlagRequired("chat-session-id")
	_ = notesPeriodBriefStartCmd.MarkFlagRequired("context-note-page-id")
	notesPeriodBriefInsertCmd.Flags().String("run-id", "", "Finished period brief run id (required)")
	notesPeriodBriefInsertCmd.Flags().String("target-page-id", "", "Writable note page id (required)")
	notesPeriodBriefInsertCmd.Flags().String("mode", "", "append or child (required)")
	_ = notesPeriodBriefInsertCmd.MarkFlagRequired("run-id")
	_ = notesPeriodBriefInsertCmd.MarkFlagRequired("target-page-id")
	_ = notesPeriodBriefInsertCmd.MarkFlagRequired("mode")
	notesGetCmd.Flags().String("output", "json", "Output format: json (default) or table")
	notesTreeCmd.Flags().String("output", "json", "Output format: json (default) or table")
	notesPeriodBriefRetryCollectorsCmd.Flags().String("draft-page-id", "", "Period Brief draft page id (required)")
	notesPeriodBriefRetryCollectorsCmd.Flags().StringSlice("collector-agent-id", nil, "Optional collector agent ids to retry (default: all retryable)")
	_ = notesPeriodBriefRetryCollectorsCmd.MarkFlagRequired("draft-page-id")
	notesPeriodBriefSubmitPackCmd.Flags().String("draft-page-id", "", "Period Brief draft page id (required)")
	notesPeriodBriefSubmitPackCmd.Flags().String("markdown", "", "Pack markdown (default: read stdin)")
	_ = notesPeriodBriefSubmitPackCmd.MarkFlagRequired("draft-page-id")
	notesPeriodBriefSubmitCollectPlanCmd.Flags().String("draft-page-id", "", "Period Brief draft page id (required)")
	notesPeriodBriefSubmitCollectPlanCmd.Flags().String("json", "", "Collect plan JSON (default: read stdin)")
	_ = notesPeriodBriefSubmitCollectPlanCmd.MarkFlagRequired("draft-page-id")
}

func runNotesGet(cmd *cobra.Command, args []string) error {
	if !isAgentAPIToken(cmd) {
		return fmt.Errorf("multica notes get requires an agent task token; human note access uses the product UI /api/notes")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	pageID := args[0]
	var page map[string]any
	path := "/api/agent/notes/pages/" + pageID
	if err := client.GetJSON(ctx, path, &page); err != nil {
		return fmt.Errorf("get note page: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		fmt.Fprintf(cmd.OutOrStdout(), "ID\t%s\nTITLE\t%s\nWORKSPACE\t%s\nUPDATED\t%s\n",
			strVal(page, "id"),
			strVal(page, "title"),
			strVal(page, "workspace_id"),
			strVal(page, "updated_at"),
		)
		if content := strVal(page, "content"); content != "" {
			fmt.Fprintln(cmd.OutOrStdout())
			fmt.Fprintln(cmd.OutOrStdout(), content)
		}
		return nil
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(page)
}

func runNotesTree(cmd *cobra.Command, args []string) error {
	if !isAgentAPIToken(cmd) {
		return fmt.Errorf("multica notes tree requires an agent task token; human note access uses the product UI /api/notes")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	pageID := args[0]
	var out map[string]any
	path := "/api/agent/notes/pages/" + pageID + "/tree"
	if err := client.GetJSON(ctx, path, &out); err != nil {
		return fmt.Errorf("list note tree: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		pages, _ := out["pages"].([]any)
		for _, raw := range pages {
			page, _ := raw.(map[string]any)
			depth := 0
			switch d := page["depth"].(type) {
			case float64:
				depth = int(d)
			case int:
				depth = d
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s%s\t%s\n",
				strings.Repeat("  ", depth),
				strVal(page, "title"),
				strVal(page, "id"),
			)
		}
		return nil
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func runNotesPeriodBriefPlan(cmd *cobra.Command, _ []string) error {
	if !isAgentAPIToken(cmd) {
		return fmt.Errorf("multica notes period-brief plan requires an agent task token")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	sessionID, _ := cmd.Flags().GetString("chat-session-id")
	pageID, _ := cmd.Flags().GetString("context-note-page-id")

	var out map[string]any
	body := map[string]any{
		"chat_session_id": strings.TrimSpace(sessionID),
	}
	if trimmed := strings.TrimSpace(pageID); trimmed != "" {
		body["context_note_page_id"] = trimmed
	}
	if cmd.Flags().Changed("window") {
		window, _ := cmd.Flags().GetString("window")
		body["window"] = strings.TrimSpace(window)
	}
	if cmd.Flags().Changed("date") {
		date, _ := cmd.Flags().GetString("date")
		body["date"] = strings.TrimSpace(date)
	}
	if cmd.Flags().Changed("start-date") {
		startDate, _ := cmd.Flags().GetString("start-date")
		body["start_date"] = strings.TrimSpace(startDate)
	}
	if cmd.Flags().Changed("end-date") {
		endDate, _ := cmd.Flags().GetString("end-date")
		body["end_date"] = strings.TrimSpace(endDate)
	}
	if cmd.Flags().Changed("focus") {
		focus, _ := cmd.Flags().GetString("focus")
		body["focus"] = strings.TrimSpace(focus)
	}
	if cmd.Flags().Changed("collector-agent-id") {
		collectorIDs, _ := cmd.Flags().GetStringSlice("collector-agent-id")
		cleaned := make([]string, 0, len(collectorIDs))
		for _, id := range collectorIDs {
			if id = strings.TrimSpace(id); id != "" {
				cleaned = append(cleaned, id)
			}
		}
		body["collector_agent_ids"] = cleaned
	}
	if err := client.PutJSON(ctx, "/api/agent/notes/period-briefs/plan", body, &out); err != nil {
		return fmt.Errorf("update period brief plan: %w", err)
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func runNotesPeriodBriefStart(cmd *cobra.Command, _ []string) error {
	if !isAgentAPIToken(cmd) {
		return fmt.Errorf("multica notes period-brief start requires an agent task token")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	sessionID, _ := cmd.Flags().GetString("chat-session-id")
	pageID, _ := cmd.Flags().GetString("context-note-page-id")
	window, _ := cmd.Flags().GetString("window")
	date, _ := cmd.Flags().GetString("date")
	startDate, _ := cmd.Flags().GetString("start-date")
	endDate, _ := cmd.Flags().GetString("end-date")
	timezone, _ := cmd.Flags().GetString("timezone")
	focus, _ := cmd.Flags().GetString("focus")
	collectorIDs, _ := cmd.Flags().GetStringSlice("collector-agent-id")
	cleaned := make([]string, 0, len(collectorIDs))
	for _, id := range collectorIDs {
		if id = strings.TrimSpace(id); id != "" {
			cleaned = append(cleaned, id)
		}
	}
	body := map[string]any{
		"chat_session_id":      strings.TrimSpace(sessionID),
		"context_note_page_id": strings.TrimSpace(pageID),
	}
	if trimmed := strings.TrimSpace(window); trimmed != "" {
		body["window"] = trimmed
	}
	if len(cleaned) > 0 {
		body["collector_agent_ids"] = cleaned
	}
	if trimmed := strings.TrimSpace(date); trimmed != "" {
		body["date"] = trimmed
	}
	if trimmed := strings.TrimSpace(startDate); trimmed != "" {
		body["start_date"] = trimmed
	}
	if trimmed := strings.TrimSpace(endDate); trimmed != "" {
		body["end_date"] = trimmed
	}
	if trimmed := strings.TrimSpace(timezone); trimmed != "" {
		body["timezone"] = trimmed
	}
	if trimmed := strings.TrimSpace(focus); trimmed != "" {
		body["focus"] = trimmed
	}

	var out map[string]any
	if err := client.PostJSON(ctx, "/api/agent/notes/period-briefs/start", body, &out); err != nil {
		return fmt.Errorf("start period brief: %w", err)
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func runNotesPeriodBriefInsert(cmd *cobra.Command, _ []string) error {
	if !isAgentAPIToken(cmd) {
		return fmt.Errorf("multica notes period-brief insert requires an agent task token")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	runID, _ := cmd.Flags().GetString("run-id")
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return fmt.Errorf("--run-id is required")
	}
	targetPageID, _ := cmd.Flags().GetString("target-page-id")
	mode, _ := cmd.Flags().GetString("mode")
	body := map[string]any{
		"target_page_id": strings.TrimSpace(targetPageID),
		"mode":           strings.TrimSpace(mode),
	}

	var out map[string]any
	path := "/api/agent/notes/period-briefs/" + runID + "/insert"
	if err := client.PostJSON(ctx, path, body, &out); err != nil {
		return fmt.Errorf("insert period brief: %w", err)
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func runNotesPeriodBriefRetryCollectors(cmd *cobra.Command, _ []string) error {
	if !isAgentAPIToken(cmd) {
		return fmt.Errorf("multica notes period-brief retry-collectors requires an agent task token")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	draftPageID, _ := cmd.Flags().GetString("draft-page-id")
	draftPageID = strings.TrimSpace(draftPageID)
	if draftPageID == "" {
		return fmt.Errorf("--draft-page-id is required")
	}
	collectorIDs, _ := cmd.Flags().GetStringSlice("collector-agent-id")
	body := map[string]any{}
	if len(collectorIDs) > 0 {
		cleaned := make([]string, 0, len(collectorIDs))
		for _, id := range collectorIDs {
			if id = strings.TrimSpace(id); id != "" {
				cleaned = append(cleaned, id)
			}
		}
		if len(cleaned) > 0 {
			body["collector_agent_ids"] = cleaned
		}
	}

	var out map[string]any
	path := "/api/agent/notes/period-briefs/" + draftPageID + "/retry-collectors"
	if err := client.PostJSON(ctx, path, body, &out); err != nil {
		return fmt.Errorf("retry collectors: %w", err)
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func runNotesPeriodBriefSubmitPack(cmd *cobra.Command, _ []string) error {
	if !isAgentAPIToken(cmd) {
		return fmt.Errorf("multica notes period-brief submit-pack requires an agent task token")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	draftPageID, _ := cmd.Flags().GetString("draft-page-id")
	draftPageID = strings.TrimSpace(draftPageID)
	if draftPageID == "" {
		return fmt.Errorf("--draft-page-id is required")
	}
	markdown, _ := cmd.Flags().GetString("markdown")
	markdown = strings.TrimSpace(markdown)
	if markdown == "" {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		markdown = strings.TrimSpace(string(raw))
	}
	if markdown == "" {
		return fmt.Errorf("pack markdown is required (stdin or --markdown)")
	}

	var out map[string]any
	path := "/api/agent/notes/period-briefs/" + draftPageID + "/submit-pack"
	if err := client.PostJSON(ctx, path, map[string]any{"markdown": markdown}, &out); err != nil {
		return fmt.Errorf("submit pack: %w", err)
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func runNotesPeriodBriefSubmitCollectPlan(cmd *cobra.Command, _ []string) error {
	if !isAgentAPIToken(cmd) {
		return fmt.Errorf("multica notes period-brief submit-collect-plan requires an agent task token")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	draftPageID, _ := cmd.Flags().GetString("draft-page-id")
	draftPageID = strings.TrimSpace(draftPageID)
	if draftPageID == "" {
		return fmt.Errorf("--draft-page-id is required")
	}
	rawJSON, _ := cmd.Flags().GetString("json")
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		rawJSON = strings.TrimSpace(string(raw))
	}
	if rawJSON == "" {
		return fmt.Errorf("collect plan JSON is required (stdin or --json)")
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &body); err != nil {
		return fmt.Errorf("collect plan must be JSON: %w", err)
	}

	var out map[string]any
	path := "/api/agent/notes/period-briefs/" + draftPageID + "/submit-collect-plan"
	if err := client.PostJSON(ctx, path, body, &out); err != nil {
		return fmt.Errorf("submit collect plan: %w", err)
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
