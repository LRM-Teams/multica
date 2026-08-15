package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// errAgentMessageHeld is returned after a successful HTTP freshness hold so the
// process exits non-zero. A hold is terminal for the current send attempt: the
// saved draft must never be retried merely because a runtime recovered or
// followed an instruction from the held response.
var errAgentMessageHeld = errors.New("message held by freshness check (not delivered); saved as an unsent draft and requires an explicit new decision before sending")

const maxAgentMessageStdinBytes = 1 << 20

func newMessageSendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send a visible chat message from the running agent",
		Long: "Send a visible message from the running agent to an explicit " +
			"target. Target syntax: #channel, #channel:<threadId>, " +
			"dm:@handle, or dm:@handle:<threadId>. A normal send requires a non-empty " +
			"body on stdin. Attach completed files with repeatable --attachment-id values " +
			"from `multica attachment upload`; attachment-only sends are rejected. If the " +
			"freshness check holds a send because newer messages arrived, it remains an unsent " +
			"draft. Do not automatically retry it. After reviewing the newer context, choose one " +
			"path: revise with a normal send, `multica message send --send-draft` for the saved " +
			"draft unchanged, or send nothing. Use `--send-draft --anyway` only after repeated " +
			"holds when that draft is still correct. To propose a workspace note for human confirm, " +
			"add `--note-write` and pipe only the cleaned note markdown; omit `--note-page-id` unless " +
			"the human specified a page.",
		RunE: runAgentMessageSend,
	}
	cmd.Flags().String("target", "", messageTargetFlagUsage())
	cmd.Flags().StringSlice("attachment-id", nil, "Attachment id to link (repeatable). Get one from `multica attachment upload`")
	cmd.Flags().Bool("send-draft", false, "Send the current local Draft for this target")
	cmd.Flags().Bool("anyway", false, "Send a saved Draft despite the freshness check")
	cmd.Flags().String("kind", "", "Structured output kind (content|confirmation|status|handoff|delegation|review|deliverable)")
	cmd.Flags().Bool("note-write", false, "Propose a product-note write for human confirm (create note, or insert/child when --note-page-id is set)")
	cmd.Flags().String("note-page-id", "", "Existing note page id to target; requires --note-write")
	return cmd
}

func newMessageReactCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "react",
		Short: "React to one visible canonical message from the running agent",
		Long: "Add or remove the running agent's reaction on one visible canonical " +
			"message. Use a full message UUID or a unique short ID prefix; no target is " +
			"accepted or inferred.",
		Args: cobra.NoArgs,
		RunE: runAgentMessageReact,
	}
	cmd.Flags().String("message-id", "", "Full message UUID or unique short ID prefix")
	cmd.Flags().String("emoji", "", "Emoji reaction to add or remove")
	cmd.Flags().Bool("remove", false, "Remove this reaction instead of adding it")
	return cmd
}

var messageSendCmd = newMessageSendCmd()
var messageReactCmd = newMessageReactCmd()
var messageCmd = &cobra.Command{
	Use:   "message",
	Short: "Send, check, read, search, resolve, and react to canonical chat messages",
}

var messageReadCmd = newMessageReadCmd()
var messageSearchCmd = newMessageSearchCmd()
var messageResolveCmd = newMessageResolveCmd()
var messageCheckCmd = newMessageCheckCmd()

func init() {
	messageCmd.AddCommand(messageSendCmd)
	messageCmd.AddCommand(messageCheckCmd)
	messageCmd.AddCommand(messageReadCmd)
	messageCmd.AddCommand(messageSearchCmd)
	messageCmd.AddCommand(messageResolveCmd)
	messageCmd.AddCommand(messageReactCmd)
}

type messageCheckCLIResponse struct {
	Messages  []protocol.AgentMessageProjection `json:"messages"`
	HasMore   bool                              `json:"has_more"`
	Remaining int                               `json:"remaining"`
	Status    string                            `json:"status"`
}

func newMessageCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Drain a bounded window of Pending messages",
		Args:  cobra.NoArgs,
		RunE:  runAgentMessageCheck,
	}
}

func runAgentMessageCheck(_ *cobra.Command, _ []string) error {
	return runAgentMessageCheckWithWriter(os.Stdout)
}

func runAgentMessageCheckWithWriter(output io.Writer) error {
	agentID := strings.TrimSpace(os.Getenv("MULTICA_AGENT_ID"))
	port := strings.TrimSpace(os.Getenv("MULTICA_DAEMON_PORT"))
	if agentID == "" {
		return errors.New("message check requires MULTICA_AGENT_ID")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return errors.New("message check requires a valid MULTICA_DAEMON_PORT")
	}
	body, err := json.Marshal(map[string]string{"agent_id": agentID})
	if err != nil {
		return fmt.Errorf("encode message check request: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/credential-proxy/messages/check", portNumber), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("prepare message check: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("message check through Credential Proxy: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("message check through Credential Proxy: %s: %s", response.Status, strings.TrimSpace(string(detail)))
	}
	var result messageCheckCLIResponse
	if err := consumeMessageCoverageResponse(
		ctx,
		response.Body,
		output,
		&result,
		func(w io.Writer) error { return printMessageCheckResult(w, result) },
		commitLocalMessageCoverage,
	); err != nil {
		return fmt.Errorf("consume message check response: %w", err)
	}
	return nil
}

func printMessageCheckResult(w io.Writer, result messageCheckCLIResponse) error {
	for _, message := range result.Messages {
		if _, err := fmt.Fprintf(w, "Message %s (%s seq %d)\n", message.ID, message.Target, message.Seq); err != nil {
			return err
		}
		if message.Content != "" {
			if _, err := fmt.Fprintln(w, message.Content); err != nil {
				return err
			}
		}
		if len(message.Parts) > 0 {
			parts, err := json.Marshal(message.Parts)
			if err != nil {
				return fmt.Errorf("encode Message parts: %w", err)
			}
			if _, err := fmt.Fprintf(w, "Parts: %s\n", parts); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	if result.HasMore {
		_, err := fmt.Fprintf(w, "More Pending messages remain (%d); run `multica message check` again.\n", result.Remaining)
		return err
	}
	_, err := fmt.Fprintln(w, "Message check complete.")
	return err
}

func newMessageReadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "read",
		Short: "Read bounded canonical history from a targeted chat surface",
		Long: "Read bounded canonical history from a targeted chat surface. " +
			"Use at most one anchor: --before-id, --after-id, or --around-id with a full message ID or a unique 8+ character ID prefix. " +
			"--before-id and --after-id exclude the anchor; --around-id includes it. Results are returned in ascending sequence order. " +
			"The --before-seq/--after-seq/--around-seq variants address messages by target sequence and exist for runtime bookkeeping; prefer the id flags.",
		RunE: runAgentMessageRead,
	}
	cmd.Flags().String("target", "", messageTargetFlagUsage())
	cmd.Flags().String("before-id", "", "Read messages before a full message id or unique 8+ character id prefix")
	cmd.Flags().String("after-id", "", "Read messages after a full message id or unique 8+ character id prefix")
	cmd.Flags().String("around-id", "", "Read a window around a full message id or unique 8+ character id prefix")
	cmd.Flags().Int64("before-seq", 0, "Read messages before a target sequence (runtime bookkeeping; prefer --before-id)")
	cmd.Flags().Int64("after-seq", 0, "Read messages after a target sequence (runtime bookkeeping; prefer --after-id)")
	cmd.Flags().Int64("around-seq", 0, "Read a window around a target sequence (runtime bookkeeping; prefer --around-id)")
	cmd.Flags().Int("limit", 20, "Maximum messages to return")
	return cmd
}

func newMessageSearchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Search canonical messages visible to the running agent",
		Long: "Search canonical messages visible to the running agent. Supply a query or at least one filter. " +
			"--sender accepts user:<uuid> or agent:<uuid>; --before and --after use RFC3339 timestamps. " +
			"Results are ordered newest first by default, with a stable message-id tie-breaker.",
		Args: cobra.MaximumNArgs(1),
		RunE: runAgentMessageSearch,
	}
	cmd.Flags().String("target", "", messageTargetFlagUsage())
	cmd.Flags().String("sender", "", "Filter by canonical sender: user:<uuid> or agent:<uuid>")
	cmd.Flags().String("sort", "newest", "Result order: newest or oldest")
	cmd.Flags().String("before", "", "Only messages before this RFC3339 timestamp")
	cmd.Flags().String("after", "", "Only messages after this RFC3339 timestamp")
	cmd.Flags().Int("limit", 50, "Maximum matches to return (1-100)")
	cmd.Flags().Int("offset", 0, "Number of matching messages to skip (0-10000)")
	return cmd
}

func newMessageResolveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resolve <message-id>",
		Short: "Resolve one canonical message by its full or unique short ID",
		Long: "Resolve exactly one canonical message visible to the running agent. " +
			"The identity may be a full UUID or a unique short UUID prefix.",
		Args: cobra.ExactArgs(1),
		RunE: runAgentMessageResolve,
	}
}

func messageTargetFlagUsage() string {
	return "Required target: #channel, #channel:<threadId>, dm:@handle, or dm:@handle:<threadId>"
}

func requiredMessageTarget(cmd *cobra.Command) (string, error) {
	target := strings.TrimSpace(flagString(cmd, "target"))
	if target == "" {
		return "", fmt.Errorf("target is required; --target accepts #channel, #channel:<threadId>, dm:@<handle>, or dm:@<handle>:<threadId>")
	}
	return target, nil
}

func runAgentMessageSend(cmd *cobra.Command, _ []string) error {
	return runAgentMessageSendWithWriter(cmd, os.Stdout)
}

func runAgentMessageSendWithWriter(cmd *cobra.Command, output io.Writer) error {
	target, err := requiredMessageTarget(cmd)
	if err != nil {
		return err
	}
	sendDraft, _ := cmd.Flags().GetBool("send-draft")
	anyway, _ := cmd.Flags().GetBool("anyway")
	if anyway && !sendDraft {
		return fmt.Errorf("--anyway is only valid with --send-draft")
	}
	if sendDraft {
		for _, name := range []string{"attachment-id", "kind", "note-write", "note-page-id"} {
			if cmd.Flags().Changed(name) {
				return fmt.Errorf("--send-draft does not accept --%s; it reuses the saved payload", name)
			}
		}
		body := map[string]any{"target": target, "send_draft": true}
		if anyway {
			body["anyway"] = true
		}
		if err := outputAgentMessageSendThroughCredentialProxy(body, output); err != nil {
			return fmt.Errorf("send saved Draft: %w", err)
		}
		return nil
	}
	contentBytes, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), maxAgentMessageStdinBytes+1))
	if err != nil {
		return fmt.Errorf("read message stdin: %w", err)
	}
	content := string(contentBytes)
	if len(contentBytes) > maxAgentMessageStdinBytes {
		return fmt.Errorf("message content is too large")
	}
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("message content is required on stdin")
	}
	attachmentIDs, _ := cmd.Flags().GetStringSlice("attachment-id")
	for i, attachmentID := range attachmentIDs {
		if strings.TrimSpace(attachmentID) == "" {
			return fmt.Errorf("--attachment-id %d is empty", i+1)
		}
	}

	body := map[string]any{
		"target":         target,
		"content":        content,
		"attachment_ids": attachmentIDs,
	}
	if kind := strings.TrimSpace(flagString(cmd, "kind")); kind != "" {
		normalized := protocol.NormalizeChannelMessageKind(kind)
		if normalized == "" || normalized == protocol.ChannelMessageKindSystemReminder {
			return fmt.Errorf("invalid --kind %q; use content|confirmation|status|handoff|delegation|review|deliverable", kind)
		}
		body["kind"] = normalized
	}
	noteWrite, _ := cmd.Flags().GetBool("note-write")
	notePageID := strings.TrimSpace(flagString(cmd, "note-page-id"))
	if notePageID != "" && !noteWrite {
		return fmt.Errorf("--note-page-id requires --note-write")
	}
	if noteWrite {
		body["note_write"] = true
		if notePageID != "" {
			body["note_page_id"] = notePageID
		}
	}
	// Raft-aligned: chat send identity is minted by the Credential Proxy
	// (independent uuid / draft reuse). CLI does not stamp turn batch keys.
	if err := outputAgentMessageSendThroughCredentialProxy(body, output); err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	return nil
}

func outputAgentMessageSendThroughCredentialProxy(body map[string]any, output io.Writer) error {
	var result map[string]any
	err := withAgentMessageCredentialProxyResponse("send", body, func(ctx context.Context, responseBody io.Reader) error {
		return consumeMessageCoverageResponse(
			ctx,
			responseBody,
			output,
			&result,
			func(w io.Writer) error { return cli.PrintJSON(w, result) },
			commitLocalMessageCoverage,
		)
	})
	if err != nil {
		return err
	}
	if agentTransportOutputIsHeld(result) {
		return errAgentMessageHeld
	}
	return nil
}

func agentTransportOutputIsHeld(out map[string]any) bool {
	return strings.EqualFold(fmt.Sprint(out["state"]), "held") || strings.EqualFold(fmt.Sprint(out["outcome"]), "held")
}

func runAgentMessageReact(cmd *cobra.Command, _ []string) error {
	messageID := strings.TrimSpace(flagString(cmd, "message-id"))
	if messageID == "" {
		return fmt.Errorf("message-id is required; pass --message-id")
	}
	emoji := strings.TrimSpace(flagString(cmd, "emoji"))
	if emoji == "" {
		return fmt.Errorf("emoji is required; pass --emoji")
	}
	remove, _ := cmd.Flags().GetBool("remove")
	body := map[string]any{
		"message_id": messageID,
		"emoji":      emoji,
		"remove":     remove,
	}
	var out map[string]any
	if err := postAgentMessageThroughCredentialProxy("react", body, &out); err != nil {
		return fmt.Errorf("react to message: %w", err)
	}
	return printAgentTransportOutput(out)
}

func runAgentMessageRead(cmd *cobra.Command, _ []string) error {
	return runAgentMessageReadWithWriter(cmd, os.Stdout)
}

func runAgentMessageReadWithWriter(cmd *cobra.Command, output io.Writer) error {
	target, err := requiredMessageTarget(cmd)
	if err != nil {
		return err
	}
	limit, _ := cmd.Flags().GetInt("limit")
	body := map[string]any{
		"target": target,
		"limit":  limit,
	}
	for _, name := range []string{"before-id", "after-id", "around-id"} {
		if value := strings.TrimSpace(flagString(cmd, name)); value != "" {
			body[strings.ReplaceAll(name, "-", "_")] = value
		}
	}
	for _, name := range []string{"before-seq", "after-seq", "around-seq"} {
		if value, _ := cmd.Flags().GetInt64(name); value != 0 {
			body[strings.ReplaceAll(name, "-", "_")] = value
		}
	}
	if err := outputAgentMessageReadThroughCredentialProxy(body, output); err != nil {
		return fmt.Errorf("read messages: %w", err)
	}
	return nil
}

func outputAgentMessageReadThroughCredentialProxy(body map[string]any, output io.Writer) error {
	var result map[string]any
	return withAgentMessageCredentialProxyResponse("read", body, func(ctx context.Context, responseBody io.Reader) error {
		return consumeMessageCoverageResponse(
			ctx,
			responseBody,
			output,
			&result,
			func(w io.Writer) error { return cli.PrintJSON(w, result) },
			commitLocalMessageCoverage,
		)
	})
}

func postAgentMessageThroughCredentialProxy(operation string, body map[string]any, out any) error {
	return withAgentMessageCredentialProxyResponse(operation, body, func(_ context.Context, responseBody io.Reader) error {
		if err := json.NewDecoder(io.LimitReader(responseBody, 1<<20)).Decode(out); err != nil {
			return fmt.Errorf("decode message %s response: %w", operation, err)
		}
		return nil
	})
}

func withAgentMessageCredentialProxyResponse(
	operation string,
	body map[string]any,
	consume func(context.Context, io.Reader) error,
) error {
	agentID := strings.TrimSpace(os.Getenv("MULTICA_AGENT_ID"))
	workspaceID := strings.TrimSpace(os.Getenv("MULTICA_WORKSPACE_ID"))
	port := strings.TrimSpace(os.Getenv("MULTICA_DAEMON_PORT"))
	if agentID == "" || workspaceID == "" {
		return fmt.Errorf("message %s requires MULTICA_AGENT_ID and MULTICA_WORKSPACE_ID", operation)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("message %s requires a valid MULTICA_DAEMON_PORT", operation)
	}
	requestBody := make(map[string]any, len(body)+6)
	for key, value := range body {
		requestBody[key] = value
	}
	requestBody["agent_id"] = agentID
	requestBody["workspace_id"] = workspaceID
	raw, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("encode message %s request: %w", operation, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cli.APITimeout())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/credential-proxy/messages/%s", portNumber, operation), bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("prepare message %s: %w", operation, err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: cli.APITimeout()}).Do(request)
	if err != nil {
		return fmt.Errorf("message %s through Credential Proxy: %w", operation, err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("message %s through Credential Proxy: %s: %s", operation, response.Status, strings.TrimSpace(string(detail)))
	}
	if consume == nil {
		return fmt.Errorf("message %s response consumer is unavailable", operation)
	}
	return consume(ctx, response.Body)
}

func runAgentMessageSearch(cmd *cobra.Command, args []string) error {
	limit, _ := cmd.Flags().GetInt("limit")
	offset, _ := cmd.Flags().GetInt("offset")
	query := ""
	if len(args) > 0 {
		query = strings.TrimSpace(args[0])
	}
	body := map[string]any{
		"query":  query,
		"limit":  limit,
		"offset": offset,
	}
	for _, name := range []string{"target", "sender", "sort", "before", "after"} {
		if value := strings.TrimSpace(flagString(cmd, name)); value != "" {
			body[name] = value
		}
	}
	var out map[string]any
	if err := postAgentMessageThroughCredentialProxy("search", body, &out); err != nil {
		return fmt.Errorf("search messages: %w", err)
	}
	return printAgentTransportOutput(out)
}

func runAgentMessageResolve(_ *cobra.Command, args []string) error {
	body := map[string]any{"message_id": strings.TrimSpace(args[0])}
	var out map[string]any
	if err := postAgentMessageThroughCredentialProxy("resolve", body, &out); err != nil {
		return fmt.Errorf("resolve message: %w", err)
	}
	return cli.PrintJSON(os.Stdout, out)
}

func postAgentTransport(cmd *cobra.Command, path string, body any, out any) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	return client.PostJSON(ctx, path, body, out)
}
func printAgentTransportOutput(out map[string]any) error {
	if err := cli.PrintJSON(os.Stdout, out); err != nil {
		return err
	}
	if agentTransportOutputIsHeld(out) {
		return errAgentMessageHeld
	}
	return nil
}
