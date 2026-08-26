package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
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

var researchV6WorkManifestCmd = &cobra.Command{
	Use:   "work-manifest <session-id> <work-item-id> <attempt-id>",
	Short: "Read the frozen V6 Work Manifest for this attempt",
	Args:  exactArgs(3),
	RunE:  runResearchV6WorkManifest,
}

var researchV6WorkArtifactCmd = &cobra.Command{
	Use:   "work-artifact <session-id> <work-item-id> <attempt-id> <artifact-version-id>",
	Short: "Read one frozen V6 Work artifact representation",
	Args:  exactArgs(4),
	RunE:  runResearchV6WorkArtifact,
}

var researchV6DirectorBriefCmd = &cobra.Command{
	Use:   "director-brief <session-id> <work-item-id> <attempt-id>",
	Short: "Read one frozen V6 Director Brief page",
	Args:  exactArgs(3),
	RunE:  runResearchV6DirectorBrief,
}

var researchV6DirectorBriefAckCmd = &cobra.Command{
	Use:   "director-brief-ack <session-id> <work-item-id> <attempt-id>",
	Short: "Acknowledge one frozen V6 Director Brief page",
	Args:  exactArgs(3),
	RunE:  runResearchV6DirectorBriefAck,
}

var researchV6WorkCatalogCmd = &cobra.Command{
	Use:   "work-catalog <session-id> <work-item-id> <attempt-id>",
	Short: "Read one authorized V6 work catalog page",
	Args:  exactArgs(3),
	RunE:  runResearchV6WorkCatalog,
}

var researchV6WorkCatalogAckCmd = &cobra.Command{
	Use:   "work-catalog-ack <session-id> <work-item-id> <attempt-id>",
	Short: "Acknowledge one authorized V6 work catalog page",
	Args:  exactArgs(3),
	RunE:  runResearchV6WorkCatalogAck,
}

var researchV6WorkSubmitCmd = &cobra.Command{
	Use:   "work-submit <session-id> <work-item-id> <attempt-id>",
	Short: "Submit the strict V6 result envelope for this attempt",
	Args:  exactArgs(3),
	RunE:  runResearchV6WorkSubmit,
}

var researchV6ReportUploadCmd = &cobra.Command{
	Use:   "report-upload <session-id> <work-item-id> <attempt-id>",
	Short: "Upload and verify one immutable V6 report resource",
	Args:  exactArgs(3),
	RunE:  runResearchV6ReportUpload,
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
	researchCmd.PersistentFlags().String("output", "json", "Output format: json")
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
	researchV6DirectorBriefCmd.Flags().String("cursor", "", "Director Brief cursor returned by the previous page")
	researchV6DirectorBriefAckCmd.Flags().String("client-request-id", "", "Idempotency UUID; generated when omitted")
	researchV6DirectorBriefAckCmd.Flags().String("brief-id", "", "Brief ID from the page")
	researchV6DirectorBriefAckCmd.Flags().String("brief-hash", "", "Brief hash from the page")
	researchV6DirectorBriefAckCmd.Flags().String("page-key", "", "Page key from the page")
	researchV6DirectorBriefAckCmd.Flags().String("page-hash", "", "Page hash from the page")
	_ = researchV6DirectorBriefAckCmd.MarkFlagRequired("brief-id")
	_ = researchV6DirectorBriefAckCmd.MarkFlagRequired("brief-hash")
	_ = researchV6DirectorBriefAckCmd.MarkFlagRequired("page-key")
	_ = researchV6DirectorBriefAckCmd.MarkFlagRequired("page-hash")
	researchV6WorkCatalogCmd.Flags().String("view", "same_tier", "Catalog view: same_tier or higher_candidates")
	researchV6WorkCatalogCmd.Flags().String("cursor", "", "Catalog cursor returned by the previous page")
	researchV6WorkCatalogAckCmd.Flags().String("client-request-id", "", "Idempotency UUID; generated when omitted")
	researchV6WorkCatalogAckCmd.Flags().String("page-key", "", "Page key from the catalog response")
	researchV6WorkCatalogAckCmd.Flags().String("page-hash", "", "Page hash from the catalog response")
	_ = researchV6WorkCatalogAckCmd.MarkFlagRequired("page-key")
	_ = researchV6WorkCatalogAckCmd.MarkFlagRequired("page-hash")
	researchV6WorkSubmitCmd.Flags().String("file", "", "JSON result file path, or - for stdin")
	_ = researchV6WorkSubmitCmd.MarkFlagRequired("file")
	researchV6ReportUploadCmd.Flags().String("file", "", "Resource file path")
	researchV6ReportUploadCmd.Flags().String("path", "", "Relative path inside the report package; defaults to the file name")
	researchV6ReportUploadCmd.Flags().String("role", "", "Resource role: document, script, style, image, font, or data")
	researchV6ReportUploadCmd.Flags().String("media-type", "", "Exact resource media type; inferred from the file extension when omitted")
	researchV6ReportUploadCmd.Flags().String("client-request-id", "", "Create idempotency UUID; generated when omitted")
	researchV6ReportUploadCmd.Flags().String("completion-request-id", "", "Completion idempotency UUID; generated when omitted")
	_ = researchV6ReportUploadCmd.MarkFlagRequired("file")
	_ = researchV6ReportUploadCmd.MarkFlagRequired("role")

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
	researchCmd.AddCommand(researchV6WorkManifestCmd)
	researchCmd.AddCommand(researchV6WorkArtifactCmd)
	researchCmd.AddCommand(researchV6DirectorBriefCmd)
	researchCmd.AddCommand(researchV6DirectorBriefAckCmd)
	researchCmd.AddCommand(researchV6WorkCatalogCmd)
	researchCmd.AddCommand(researchV6WorkCatalogAckCmd)
	researchCmd.AddCommand(researchV6WorkSubmitCmd)
	researchCmd.AddCommand(researchV6ReportUploadCmd)
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
	raw, err := readResearchJSONInput(cmd, path)
	if err != nil {
		return fmt.Errorf("read research result: %w", err)
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

func researchV6AttemptPath(args []string) string {
	return fmt.Sprintf("/api/agent/research/sessions/%s/work-items/%s/attempts/%s", args[0], args[1], args[2])
}

func runResearchV6WorkManifest(cmd *cobra.Command, args []string) error {
	return researchV6GetJSON(cmd, researchV6AttemptPath(args)+"/manifest")
}

func runResearchV6WorkArtifact(cmd *cobra.Command, args []string) error {
	return researchV6GetJSON(cmd, researchV6AttemptPath(args)+"/artifacts/"+args[3])
}

func runResearchV6DirectorBrief(cmd *cobra.Command, args []string) error {
	cursor, _ := cmd.Flags().GetString("cursor")
	query := url.Values{}
	if cursor = strings.TrimSpace(cursor); cursor != "" {
		query.Set("cursor", cursor)
	}
	return researchV6GetJSON(cmd, researchV6PathWithQuery(researchV6AttemptPath(args)+"/director-brief", query))
}

func runResearchV6DirectorBriefAck(cmd *cobra.Command, args []string) error {
	requestID, _ := cmd.Flags().GetString("client-request-id")
	if strings.TrimSpace(requestID) == "" {
		requestID = uuid.NewString()
	}
	briefID, _ := cmd.Flags().GetString("brief-id")
	briefHash, _ := cmd.Flags().GetString("brief-hash")
	pageKey, _ := cmd.Flags().GetString("page-key")
	pageHash, _ := cmd.Flags().GetString("page-hash")
	return researchV6PostJSON(cmd, researchV6AttemptPath(args)+"/director-brief-acks", map[string]any{
		"client_request_id": requestID, "brief_id": briefID, "brief_hash": briefHash,
		"page_key": pageKey, "page_hash": pageHash,
	})
}

func runResearchV6WorkCatalog(cmd *cobra.Command, args []string) error {
	view, _ := cmd.Flags().GetString("view")
	cursor, _ := cmd.Flags().GetString("cursor")
	query := url.Values{}
	query.Set("view", strings.TrimSpace(view))
	if cursor = strings.TrimSpace(cursor); cursor != "" {
		query.Set("cursor", cursor)
	}
	return researchV6GetJSON(cmd, researchV6PathWithQuery(researchV6AttemptPath(args)+"/catalog", query))
}

func runResearchV6WorkCatalogAck(cmd *cobra.Command, args []string) error {
	requestID, _ := cmd.Flags().GetString("client-request-id")
	if strings.TrimSpace(requestID) == "" {
		requestID = uuid.NewString()
	}
	pageKey, _ := cmd.Flags().GetString("page-key")
	pageHash, _ := cmd.Flags().GetString("page-hash")
	return researchV6PostJSON(cmd, researchV6AttemptPath(args)+"/catalog-acks", map[string]any{
		"client_request_id": requestID, "page_key": pageKey, "page_hash": pageHash,
	})
}

func runResearchV6WorkSubmit(cmd *cobra.Command, args []string) error {
	path, _ := cmd.Flags().GetString("file")
	raw, err := readResearchJSONInput(cmd, path)
	if err != nil {
		return fmt.Errorf("read V6 work submission: %w", err)
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var out map[string]any
	if err = client.PostJSON(ctx, researchV6AttemptPath(args)+"/submission", json.RawMessage(raw), &out); err != nil {
		return fmt.Errorf("submit V6 research work: %w", err)
	}
	return cli.PrintJSON(os.Stdout, out)
}

func runResearchV6ReportUpload(cmd *cobra.Command, args []string) error {
	file, _ := cmd.Flags().GetString("file")
	content, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read report resource: %w", err)
	}
	packagePath, _ := cmd.Flags().GetString("path")
	if strings.TrimSpace(packagePath) == "" {
		packagePath = filepath.Base(file)
	}
	role, _ := cmd.Flags().GetString("role")
	mediaType, _ := cmd.Flags().GetString("media-type")
	if strings.TrimSpace(mediaType) == "" {
		mediaType = mime.TypeByExtension(filepath.Ext(file))
	}
	if strings.TrimSpace(mediaType) == "" {
		return fmt.Errorf("report resource media type is required")
	}
	requestID, _ := cmd.Flags().GetString("client-request-id")
	if strings.TrimSpace(requestID) == "" {
		requestID = uuid.NewString()
	}
	completionID, _ := cmd.Flags().GetString("completion-request-id")
	if strings.TrimSpace(completionID) == "" {
		completionID = uuid.NewString()
	}
	hash := sha256.Sum256(content)
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var capability struct {
		ResourceID string            `json:"resource_id"`
		Status     string            `json:"status"`
		Method     string            `json:"method"`
		URL        string            `json:"url"`
		Headers    map[string]string `json:"headers"`
	}
	base := researchV6AttemptPath(args) + "/report-uploads"
	if err = client.PostJSON(ctx, base, map[string]any{
		"client_request_id": requestID, "path": packagePath, "role": role, "media_type": mediaType,
		"content_hash": fmt.Sprintf("sha256:%x", hash), "byte_size": len(content),
	}, &capability); err != nil {
		return fmt.Errorf("create V6 report upload: %w", err)
	}
	if capability.Status == "verified" {
		return cli.PrintJSON(os.Stdout, capability)
	}
	headers := capability.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	if _, exists := headers["Content-Type"]; !exists {
		headers["Content-Type"] = mediaType
	}
	if err = client.UploadToDestination(ctx, capability.URL, capability.Method, headers, bytes.NewReader(content), int64(len(content))); err != nil {
		return fmt.Errorf("upload V6 report resource: %w", err)
	}
	var out map[string]any
	if err = client.PostJSON(ctx, base+"/"+capability.ResourceID+"/complete", map[string]any{"client_request_id": completionID}, &out); err != nil {
		return fmt.Errorf("complete V6 report upload: %w", err)
	}
	return cli.PrintJSON(os.Stdout, out)
}

func readResearchJSONInput(cmd *cobra.Command, path string) ([]byte, error) {
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), (2<<20)+1))
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > 2<<20 {
		return nil, fmt.Errorf("research result exceeds 2 MiB")
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("research result is not valid JSON")
	}
	return raw, nil
}

func researchV6GetJSON(cmd *cobra.Command, path string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var out any
	if err = client.GetJSON(ctx, path, &out); err != nil {
		return err
	}
	return cli.PrintJSON(os.Stdout, out)
}

func researchV6PostJSON(cmd *cobra.Command, path string, body map[string]any) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var out map[string]any
	if err = client.PostJSON(ctx, path, body, &out); err != nil {
		return err
	}
	return cli.PrintJSON(os.Stdout, out)
}

func researchV6PathWithQuery(path string, query url.Values) string {
	if encoded := query.Encode(); encoded != "" {
		return path + "?" + encoded
	}
	return path
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
