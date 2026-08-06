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

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/turntransport"
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
		Short: "Send a visible chat message from the running agent task",
		Long: "Send a visible message from the running agent task to an explicit " +
			"target. Target syntax: #channel, #channel:<threadId>, " +
			"dm:@handle, or dm:@handle:<threadId>. A normal send requires a non-empty " +
			"body on stdin. Attach completed files with repeatable --attachment-id values " +
			"from `multica attachment upload`; attachment-only sends are rejected. If the " +
			"freshness check holds a send because newer messages arrived, it remains an unsent " +
			"draft. Do not automatically retry it or send it later; after reviewing the newer " +
			"context, compose a fresh response if one is still needed.",
		RunE: runAgentMessageSend,
	}
	cmd.Flags().String("target", "", messageTargetFlagUsage())
	cmd.Flags().StringSlice("attachment-id", nil, "Attachment id to link (repeatable). Get one from `multica attachment upload`")
	cmd.Flags().Bool("send-draft", false, "Send the current local Draft for this target")
	cmd.Flags().Bool("anyway", false, "Send a saved Draft despite the freshness check")
	cmd.Flags().String("output", "json", "Output format: json or text")
	return cmd
}

func newMessageReactCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "react",
		Short: "React to one visible canonical message from the running agent task",
		Long: "Add or remove the running agent task's reaction on one visible canonical " +
			"message. Use a full message UUID or a unique short ID prefix; no target is " +
			"accepted or inferred.",
		Args: cobra.NoArgs,
		RunE: runAgentMessageReact,
	}
	cmd.Flags().String("message-id", "", "Full message UUID or unique short ID prefix")
	cmd.Flags().String("emoji", "", "Emoji reaction to add or remove")
	cmd.Flags().Bool("remove", false, "Remove this reaction instead of adding it")
	cmd.Flags().String("output", "json", "Output format: json or text")
	return cmd
}

// Canonical grouped forms: `multica message send` / `multica message react`.
var messageSendCmd = newMessageSendCmd()
var messageReactCmd = newMessageReactCmd()
var messageAskChoiceCmd = newMessageAskChoiceCmd()

// Top-level `multica react` remains as a compatibility alias while older
// runtimes still learn the ungrouped reaction command. Sending has no
// top-level alias: agents must use `multica message send`.
var reactCmd = newCompatMessageAlias(newMessageReactCmd(), "message react")

func newMessageAskChoiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ask-choice",
		Short: "Send a Multica choice card (binary/list) from the running agent task",
		Long: "Emit a platform-native choice MessagePart so the human can tap an " +
			"option in chat/DM. Cursor / PI / Claude and other runtimes all use this " +
			"path — do not rely on vendor AskUserQuestion UIs. Layout binary = two " +
			"horizontal buttons; list = 2–4 vertical options. Repeat --option as " +
			"id=...,label=... (optional description=...).",
		RunE: runAgentMessageAskChoice,
	}
	cmd.Flags().String("target", "", messageTargetFlagUsage())
	cmd.Flags().String("prompt", "", "Short question shown above the options")
	cmd.Flags().String("layout", "binary", "binary (exactly 2 options) or list (2–4 options)")
	cmd.Flags().StringSlice("option", nil, "Option as id=...,label=...[,description=...] (repeatable)")
	cmd.Flags().String("choice-id", "", "Stable choice id; generated when omitted")
	cmd.Flags().String("message", "", "Optional text above the choice card")
	cmd.Flags().Bool("message-stdin", false, "Read optional preamble text from stdin")
	cmd.Flags().String("message-file", "", "Read optional preamble text from a UTF-8 file")
	cmd.Flags().String("client-message-id", "", "Idempotency key; generated automatically when omitted")
	cmd.Flags().String("output", "json", "Output format: json or text")
	return cmd
}

func newCompatMessageAlias(cmd *cobra.Command, canonical string) *cobra.Command {
	cmd.Short += " (alias of `multica " + canonical + "`)"
	cmd.Long += "\n\nThis top-level form is a compatibility alias of `multica " +
		canonical + "`; prefer the grouped form."
	return cmd
}

var messageCmd = &cobra.Command{
	Use:   "message",
	Short: "Send, react to, read, and search chat messages for the running agent task",
}

var messageReadCmd = newMessageReadCmd()
var messageSearchCmd = newMessageSearchCmd()
var messageResolveCmd = newMessageResolveCmd()
var messageA2AControlCmd = newMessageA2AControlCmd()
var messageCheckCmd = newMessageCheckCmd()

func init() {
	messageCmd.AddCommand(messageSendCmd)
	messageCmd.AddCommand(messageReactCmd)
	messageCmd.AddCommand(messageAskChoiceCmd)
	messageCmd.AddCommand(messageReadCmd)
	messageCmd.AddCommand(messageSearchCmd)
	messageCmd.AddCommand(messageResolveCmd)
	messageCmd.AddCommand(messageA2AControlCmd)
	messageCmd.AddCommand(messageCheckCmd)
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
	agentID := strings.TrimSpace(os.Getenv("MULTICA_AGENT_ID"))
	taskID := strings.TrimSpace(os.Getenv("MULTICA_TASK_ID"))
	port := strings.TrimSpace(os.Getenv("MULTICA_DAEMON_PORT"))
	if agentID == "" || taskID == "" {
		return errors.New("message check requires an active daemon Agent turn")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return errors.New("message check requires a valid MULTICA_DAEMON_PORT")
	}
	body, err := json.Marshal(map[string]string{"agent_id": agentID, "task_id": taskID})
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
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&result); err != nil {
		return fmt.Errorf("decode message check response: %w", err)
	}
	return printMessageCheckResult(os.Stdout, result)
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
			"Use at most one of --before, --after, or --around with a full message ID, a unique 8+ character ID prefix, or a positive target sequence. " +
			"A digits-only anchor is interpreted as a target sequence. " +
			"--before and --after exclude the anchor; --around includes it. Results are returned in ascending sequence order.",
		RunE: runAgentMessageRead,
	}
	cmd.Flags().String("target", "", messageTargetFlagUsage())
	cmd.Flags().String("before", "", "Read messages before a full id, unique short id, or target sequence")
	cmd.Flags().String("after", "", "Read messages after a full id, unique short id, or target sequence")
	cmd.Flags().String("around", "", "Read a window around a full id, unique short id, or target sequence")
	cmd.Flags().Int("limit", 20, "Maximum messages to return")
	cmd.Flags().String("output", "json", "Output format: json or text")
	return cmd
}

func newMessageSearchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search messages in a targeted chat surface",
		Args:  exactArgs(1),
		RunE:  runAgentMessageSearch,
	}
	cmd.Flags().String("target", "", messageTargetFlagUsage())
	cmd.Flags().Int("limit", 50, "Maximum matches to return")
	cmd.Flags().String("output", "json", "Output format: json or text")
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

func newMessageA2AControlCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "a2a-control",
		Short: "Apply an owner-authorized control to an agent direct message",
		Long: "Pause, resume, or extend an existing agent-to-agent direct message. " +
			"This command succeeds only while executing a task explicitly initiated " +
			"by the source agent's owner; peer-only tasks cannot grant themselves more budget.",
		RunE: runAgentMessageA2AControl,
	}
	cmd.Flags().String("target", "", "Required existing agent DM target: dm:@<agent-handle>")
	cmd.Flags().String("action", "", "Required action: pause_pair, resume_pair, grant_rounds, pause_global, or resume_global")
	cmd.Flags().String("exchange-id", "", "Exchange UUID to extend; defaults to the latest exchange for grant_rounds")
	cmd.Flags().Int("rounds", 0, "Additional rounds for grant_rounds")
	cmd.Flags().String("output", "json", "Output format: json or text")
	return cmd
}

func runAgentMessageA2AControl(cmd *cobra.Command, _ []string) error {
	target, err := requiredMessageTarget(cmd)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(target, "dm:@") || strings.Contains(strings.TrimPrefix(target, "dm:@"), ":") {
		return fmt.Errorf("--target must be an agent DM target: dm:@<agent-handle>")
	}
	action := strings.TrimSpace(flagString(cmd, "action"))
	switch action {
	case "pause_pair", "resume_pair", "grant_rounds", "pause_global", "resume_global":
	default:
		return fmt.Errorf("--action must be pause_pair, resume_pair, grant_rounds, pause_global, or resume_global")
	}
	rounds, _ := cmd.Flags().GetInt("rounds")
	if action == "grant_rounds" && rounds <= 0 {
		return fmt.Errorf("--rounds must be positive for grant_rounds")
	}
	if action != "grant_rounds" && rounds != 0 {
		return fmt.Errorf("--rounds is only valid with grant_rounds")
	}
	body := map[string]any{
		"target": target,
		"action": action,
	}
	if exchangeID := strings.TrimSpace(flagString(cmd, "exchange-id")); exchangeID != "" {
		body["exchange_id"] = exchangeID
	}
	if rounds > 0 {
		body["rounds"] = rounds
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cli.APITimeout())
	defer cancel()
	var out map[string]any
	if err := client.PostJSON(ctx, "/api/agent/messages/a2a-control", body, &out); err != nil {
		return fmt.Errorf("update agent dm control: %w", err)
	}
	return printAgentTransportOutput(cmd, out, "Agent DM control updated.")
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
		for _, name := range []string{"attachment-id"} {
			if cmd.Flags().Changed(name) {
				return fmt.Errorf("--send-draft does not accept --%s; it reuses the saved payload", name)
			}
		}
		body := map[string]any{"target": target, "send_draft": true}
		if anyway {
			body["anyway"] = true
		}
		var out map[string]any
		if err := turntransport.RecordAttemptFromEnvironment(); err != nil {
			return err
		}
		if err := postAgentMessageSendThroughCredentialProxy(body, &out); err != nil {
			return fmt.Errorf("send saved Draft: %w", err)
		}
		return printAgentTransportOutput(cmd, out, agentMessageSendTextFallback(out))
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
	var out map[string]any
	if err := turntransport.RecordAttemptFromEnvironment(); err != nil {
		return err
	}
	if err := postAgentMessageSendThroughCredentialProxy(body, &out); err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	return printAgentTransportOutput(cmd, out, agentMessageSendTextFallback(out))
}

func agentMessageSendTextFallback(out map[string]any) string {
	if agentTransportOutputIsHeld(out) {
		var b strings.Builder
		b.WriteString("Message held by freshness check (not delivered; saved as an unsent draft and CLI exits non-zero). Do not automatically retry or send the draft.")
		if window, ok := out["contextWindow"].(map[string]any); ok {
			older := strings.TrimSpace(fmt.Sprint(window["olderBoundary"]))
			newer := strings.TrimSpace(fmt.Sprint(window["newerBoundary"]))
			if older != "" || newer != "" {
				b.WriteString("\nBounded context:")
				if older != "" {
					b.WriteString(" ")
					b.WriteString(older)
				}
				if newer != "" {
					b.WriteString(" ")
					b.WriteString(newer)
				}
			}
		}
		b.WriteString("\nThis send attempt is finished. Review the newer context and, if a reply is still needed, compose and send a new message; the held draft is never retried.")
		return b.String()
	}
	return "Message sent."
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
	if err := postAgentTransport(cmd, "/api/agent/messages/react", body, &out); err != nil {
		return fmt.Errorf("react to message: %w", err)
	}
	fallback := "Reaction sent."
	if remove {
		fallback = "Reaction removed."
	}
	return printAgentTransportOutput(cmd, out, fallback)
}

func runAgentMessageAskChoice(cmd *cobra.Command, _ []string) error {
	target, err := requiredMessageTarget(cmd)
	if err != nil {
		return err
	}
	prompt := strings.TrimSpace(flagString(cmd, "prompt"))
	if prompt == "" {
		return fmt.Errorf("prompt is required; pass --prompt")
	}
	layout := strings.TrimSpace(strings.ToLower(flagString(cmd, "layout")))
	if layout == "" {
		layout = protocol.ChoiceLayoutBinary
	}
	optionFlags, _ := cmd.Flags().GetStringSlice("option")
	options, err := parseChoiceOptionFlags(optionFlags)
	if err != nil {
		return err
	}
	if layout == protocol.ChoiceLayoutBinary && len(options) != 2 {
		return fmt.Errorf("binary layout requires exactly 2 --option flags")
	}
	if len(options) < 2 || len(options) > 4 {
		return fmt.Errorf("provide 2–4 --option flags")
	}
	choiceID := strings.TrimSpace(flagString(cmd, "choice-id"))
	if choiceID == "" {
		choiceID = uuid.NewString()
	}
	preamble, preambleOK, err := resolveTextFlag(cmd, "message")
	if err != nil {
		return err
	}
	text := ""
	if preambleOK {
		text = strings.TrimSpace(preamble)
	}
	parts := make([]protocol.MessagePart, 0, 2)
	if text != "" {
		parts = append(parts, protocol.MessagePart{Type: protocol.MessagePartTypeText, Text: text})
	}
	parts = append(parts, protocol.MessagePart{
		Type:     protocol.MessagePartTypeChoice,
		ChoiceID: choiceID,
		Prompt:   prompt,
		Layout:   layout,
		Options:  options,
	})
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	body := map[string]any{
		"target":            target,
		"client_message_id": clientMessageIDFlag(cmd),
		"parts":             parts,
	}
	if text != "" {
		body["content"] = text
	}
	ctx, cancel := context.WithTimeout(context.Background(), cli.APITimeout())
	defer cancel()
	var out map[string]any
	if err := turntransport.RecordAttemptFromEnvironment(); err != nil {
		return err
	}
	if err := client.PostJSON(ctx, "/api/agent/messages/send", body, &out); err != nil {
		return fmt.Errorf("ask choice: %w", err)
	}
	return printAgentTransportOutput(cmd, out, agentMessageSendTextFallback(out))
}

func parseChoiceOptionFlags(raw []string) ([]protocol.ChoiceOption, error) {
	out := make([]protocol.ChoiceOption, 0, len(raw))
	for i, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		opt := protocol.ChoiceOption{}
		for _, field := range strings.Split(item, ",") {
			field = strings.TrimSpace(field)
			key, val, ok := strings.Cut(field, "=")
			if !ok {
				return nil, fmt.Errorf("--option %d: expected id=...,label=... got %q", i+1, item)
			}
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "id":
				opt.ID = strings.TrimSpace(val)
			case "label":
				opt.Label = strings.TrimSpace(val)
			case "description", "desc":
				opt.Description = strings.TrimSpace(val)
			default:
				return nil, fmt.Errorf("--option %d: unknown field %q", i+1, key)
			}
		}
		if opt.ID == "" || opt.Label == "" {
			return nil, fmt.Errorf("--option %d: id and label are required", i+1)
		}
		out = append(out, opt)
	}
	return out, nil
}

func runAgentMessageRead(cmd *cobra.Command, _ []string) error {
	target, err := requiredMessageTarget(cmd)
	if err != nil {
		return err
	}
	limit, _ := cmd.Flags().GetInt("limit")
	body := map[string]any{
		"target": target,
		"limit":  limit,
	}
	for _, name := range []string{"before", "after", "around"} {
		if value := strings.TrimSpace(flagString(cmd, name)); value != "" {
			body[name] = value
		}
	}
	var out map[string]any
	if err := postAgentMessageReadThroughCredentialProxy(body, &out); err != nil {
		return fmt.Errorf("read messages: %w", err)
	}
	return printAgentTransportOutput(cmd, out, "")
}

func postAgentMessageReadThroughCredentialProxy(body map[string]any, out any) error {
	return postAgentMessageThroughCredentialProxy("read", body, out)
}

func postAgentMessageSendThroughCredentialProxy(body map[string]any, out any) error {
	return postAgentMessageThroughCredentialProxy("send", body, out)
}

func postAgentMessageThroughCredentialProxy(operation string, body map[string]any, out any) error {
	agentID := strings.TrimSpace(os.Getenv("MULTICA_AGENT_ID"))
	taskID := strings.TrimSpace(os.Getenv("MULTICA_TASK_ID"))
	workspaceID := strings.TrimSpace(os.Getenv("MULTICA_WORKSPACE_ID"))
	port := strings.TrimSpace(os.Getenv("MULTICA_DAEMON_PORT"))
	if agentID == "" || taskID == "" || workspaceID == "" {
		return fmt.Errorf("message %s requires an active daemon Agent turn", operation)
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
	requestBody["task_id"] = taskID
	requestBody["workspace_id"] = workspaceID
	for env, field := range map[string]string{
		"MULTICA_AGENT_INBOX_EVENT_ID":    "agent_inbox_event_id",
		"MULTICA_AGENT_INBOX_DELIVERY_ID": "agent_inbox_delivery_id",
		"MULTICA_AGENT_INBOX_LEASE_TOKEN": "agent_inbox_lease_token",
	} {
		if value := strings.TrimSpace(os.Getenv(env)); value != "" {
			requestBody[field] = value
		}
	}
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
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("decode message %s response: %w", operation, err)
	}
	return nil
}

func runAgentMessageSearch(cmd *cobra.Command, args []string) error {
	target, err := requiredMessageTarget(cmd)
	if err != nil {
		return err
	}
	limit, _ := cmd.Flags().GetInt("limit")
	body := map[string]any{
		"target": target,
		"query":  args[0],
		"limit":  limit,
	}
	var out map[string]any
	if err := postAgentTransport(cmd, "/api/agent/messages/search", body, &out); err != nil {
		return fmt.Errorf("search messages: %w", err)
	}
	return printAgentTransportOutput(cmd, out, "")
}

func runAgentMessageResolve(cmd *cobra.Command, args []string) error {
	body := map[string]string{"message_id": strings.TrimSpace(args[0])}
	var out map[string]any
	if err := postAgentTransport(cmd, "/api/agent/messages/resolve", body, &out); err != nil {
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

func clientMessageIDFlag(cmd *cobra.Command) string {
	if v := strings.TrimSpace(flagString(cmd, "client-message-id")); v != "" {
		return v
	}
	return uuid.NewString()
}

func printAgentTransportOutput(cmd *cobra.Command, out map[string]any, textFallback string) error {
	output := strings.ToLower(strings.TrimSpace(flagString(cmd, "output")))
	switch {
	case output == "" || output == "json":
		if err := cli.PrintJSON(os.Stdout, out); err != nil {
			return err
		}
	case output == "text":
		if textFallback != "" {
			fmt.Fprintln(os.Stdout, textFallback)
		} else if err := cli.PrintJSON(os.Stdout, out); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported output format %q; use json or text", output)
	}
	if agentTransportOutputIsHeld(out) {
		return errAgentMessageHeld
	}
	return nil
}
