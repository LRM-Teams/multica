package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

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

func newMessageSendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send a visible chat message from the running agent task",
		Long: "Send a visible message from the running agent task to an explicit " +
			"target. Target syntax: #channel, #channel:<threadId>, " +
			"dm:@handle, or dm:@handle:<threadId>. Use --sticker for a sticker-only " +
			"reply, or combine --sticker with --message for an acknowledgement sticker " +
			"followed by explanatory text in one message. Attach files with " +
			"--attachment-id from `multica attachment upload` (repeatable). If the " +
			"human used voice input or explicitly requested spoken output, add --voice; " +
			"the message text remains the accessible transcript. Do not generate or " +
			"attach an audio file for a voice reply; Multica synthesizes the transcript. If the " +
			"server holds a send because newer messages arrived, it remains an unsent " +
			"draft. Do not automatically retry it or send it later; after reviewing the newer " +
			"context, compose a fresh response if one is still needed.",
		RunE: runAgentMessageSend,
	}
	cmd.Flags().String("target", "", messageTargetFlagUsage())
	cmd.Flags().String("message", "", "Message to send (decodes \\n, \\r, \\t, \\\\; use --message-stdin to preserve literal backslashes)")
	cmd.Flags().Bool("message-stdin", false, "Read the message from stdin (preserves multi-line content verbatim)")
	cmd.Flags().String("message-file", "", "Read the message from a UTF-8 file")
	cmd.Flags().String("sticker", "", "Builtin sticker id (see `multica sticker list`); sticker-only when --message is omitted")
	cmd.Flags().Bool("voice", false, "Deliver the message text as synthesized speech and an accessible transcript")
	cmd.Flags().StringSlice("attachment-id", nil, "Attachment id to link (repeatable). Get one from `multica attachment upload`")
	cmd.Flags().String("client-message-id", "", "Idempotency key; generated automatically when omitted")
	cmd.Flags().String("output", "json", "Output format: json or text")
	return cmd
}

func newMessageReactCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "react",
		Short: "React to a channel or thread message from the running agent task",
		Long: "Add a reaction from the running agent task to an explicit target. " +
			"Target syntax: #channel, #channel:<threadId>, dm:@handle, " +
			"or dm:@handle:<threadId>. Omit --message-id to react to the message " +
			"that triggered the task when the task context provides one.",
		RunE: runAgentMessageReact,
	}
	cmd.Flags().String("target", "", messageTargetFlagUsage())
	cmd.Flags().String("message-id", "", "Message UUID to react to; omit to use the triggering message when available")
	cmd.Flags().String("emoji", "", "Emoji reaction to add")
	cmd.Flags().String("client-message-id", "", "Idempotency/audit key; generated automatically when omitted")
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
var messageA2AControlCmd = newMessageA2AControlCmd()

func init() {
	messageCmd.AddCommand(messageSendCmd)
	messageCmd.AddCommand(messageReactCmd)
	messageCmd.AddCommand(messageAskChoiceCmd)
	messageCmd.AddCommand(messageReadCmd)
	messageCmd.AddCommand(messageSearchCmd)
	messageCmd.AddCommand(messageA2AControlCmd)
}

func newMessageReadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "read",
		Short: "Read recent messages from a targeted chat surface",
		RunE:  runAgentMessageRead,
	}
	cmd.Flags().String("target", "", messageTargetFlagUsage())
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
	content, contentOK, err := resolveTextFlag(cmd, "message")
	if err != nil {
		return err
	}
	stickerID := strings.TrimSpace(flagString(cmd, "sticker"))
	voice, _ := cmd.Flags().GetBool("voice")
	text := ""
	if contentOK {
		text = strings.TrimSpace(content)
	}
	attachmentIDs, _ := cmd.Flags().GetStringSlice("attachment-id")
	attachmentIDs = appendUniqueStrings(nil, attachmentIDs...)
	if voice && text == "" {
		return fmt.Errorf("--voice requires message text; pass --message, --message-stdin, or --message-file")
	}
	if stickerID == "" && text == "" && len(attachmentIDs) == 0 {
		return fmt.Errorf("message, sticker, or attachment is required; pass --message, --message-stdin, --message-file, --sticker, and/or --attachment-id")
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), cli.APITimeout())
	defer cancel()

	body := map[string]any{
		"target":            target,
		"client_message_id": clientMessageIDFlag(cmd),
	}
	if text != "" {
		body["content"] = content
	}
	// Chat attachments are structured parts only. --attachment-id is sugar that
	// becomes {type:attachment, attachment_id} before POST; do not send a
	// sidecar attachment_ids field (server binds from parts).
	if parts := buildAgentSendParts(stickerID, text, attachmentIDs, voice); len(parts) > 0 {
		body["parts"] = parts
	}
	var out map[string]any
	if err := turntransport.RecordAttemptFromEnvironment(); err != nil {
		return err
	}
	if err := client.PostJSON(ctx, "/api/agent/messages/send", body, &out); err != nil {
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

// buildAgentSendParts assembles the structured chat message body for agent
// transport send. Order: sticker (if any), text (if any), then attachment parts
// in --attachment-id order. Attachments are never encoded as markdown embeds.
func buildAgentSendParts(stickerID, text string, attachmentIDs []string, voice bool) []protocol.MessagePart {
	var parts []protocol.MessagePart
	if stickerID != "" {
		parts = append(parts, protocol.MessagePart{
			Type:      protocol.MessagePartTypeSticker,
			StickerID: stickerID,
		})
	}
	if text != "" {
		parts = append(parts, protocol.MessagePart{
			Type: protocol.MessagePartTypeText,
			Text: text,
		})
	}
	if voice {
		parts = append(parts, protocol.MessagePart{Type: protocol.MessagePartTypeVoice})
	}
	for _, id := range attachmentIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		parts = append(parts, protocol.MessagePart{
			Type:         protocol.MessagePartTypeAttachment,
			AttachmentID: id,
		})
	}
	return parts
}

func runAgentMessageReact(cmd *cobra.Command, _ []string) error {
	target, err := requiredMessageTarget(cmd)
	if err != nil {
		return err
	}
	emoji := strings.TrimSpace(flagString(cmd, "emoji"))
	if emoji == "" {
		return fmt.Errorf("emoji is required; pass --emoji")
	}
	body := map[string]any{
		"target":            target,
		"message_id":        flagString(cmd, "message-id"),
		"emoji":             emoji,
		"client_message_id": clientMessageIDFlag(cmd),
	}
	var out map[string]any
	if err := turntransport.RecordAttemptFromEnvironment(); err != nil {
		return err
	}
	if err := postAgentTransport(cmd, "/api/agent/messages/react", body, &out); err != nil {
		return fmt.Errorf("react to message: %w", err)
	}
	return printAgentTransportOutput(cmd, out, "Reaction sent.")
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
	var out map[string]any
	if err := postAgentTransport(cmd, "/api/agent/messages/read", body, &out); err != nil {
		return fmt.Errorf("read messages: %w", err)
	}
	return printAgentTransportOutput(cmd, out, "")
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
