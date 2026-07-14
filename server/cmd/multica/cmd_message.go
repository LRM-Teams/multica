package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func newMessageSendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send a visible chat message from the running agent task",
		Long: "Send a visible message from the running agent task to an explicit " +
			"target. Target syntax matches Raft: #channel, #channel:<threadId>, " +
			"dm:@handle, or dm:@handle:<threadId>. Use --sticker for a sticker-only " +
			"reply, or combine --sticker with --message for an acknowledgement sticker " +
			"followed by explanatory text in one message. Attach files with " +
			"--attachment-id from `multica attachment upload` (repeatable).",
		RunE: runAgentMessageSend,
	}
	cmd.Flags().String("target", "", messageTargetFlagUsage())
	cmd.Flags().String("message", "", "Message to send (decodes \\n, \\r, \\t, \\\\; use --message-stdin to preserve literal backslashes)")
	cmd.Flags().Bool("message-stdin", false, "Read the message from stdin (preserves multi-line content verbatim)")
	cmd.Flags().String("message-file", "", "Read the message from a UTF-8 file")
	cmd.Flags().String("sticker", "", "Builtin sticker id (see `multica sticker list`); sticker-only when --message is omitted")
	cmd.Flags().StringSlice("attachment-id", nil, "Attachment id to link (repeatable). Get one from `multica attachment upload`")
	cmd.Flags().String("client-message-id", "", "Idempotency key; generated automatically when omitted")
	cmd.Flags().Bool("show-in-channel", false, "For thread targets, also show the reply on the parent channel timeline")
	cmd.Flags().String("output", "json", "Output format: json or text")
	return cmd
}

func newMessageReactCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "react",
		Short: "React to a channel or thread message from the running agent task",
		Long: "Add a reaction from the running agent task to an explicit target. " +
			"Target syntax matches Raft: #channel, #channel:<threadId>, dm:@handle, " +
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

// Top-level `multica react` remains as a compatibility alias while older
// runtimes still learn the ungrouped reaction command. Sending has no
// top-level alias: agents must use `multica message send`.
var reactCmd = newCompatMessageAlias(newMessageReactCmd(), "message react")

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

func init() {
	messageCmd.AddCommand(messageSendCmd)
	messageCmd.AddCommand(messageReactCmd)
	messageCmd.AddCommand(messageReadCmd)
	messageCmd.AddCommand(messageSearchCmd)
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

func messageTargetFlagUsage() string {
	return "Required target: #channel, #channel:<threadId>, dm:@handle, or dm:@handle:<threadId>"
}

func requiredMessageTarget(cmd *cobra.Command) (string, error) {
	target := strings.TrimSpace(flagString(cmd, "target"))
	if target == "" {
		return "", fmt.Errorf("target is required; pass --target as #channel, #channel:<threadId>, dm:@handle, or dm:@handle:<threadId>")
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
	text := ""
	if contentOK {
		text = strings.TrimSpace(content)
	}
	attachmentIDs, _ := cmd.Flags().GetStringSlice("attachment-id")
	attachmentIDs = appendUniqueStrings(nil, attachmentIDs...)
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
	if parts := buildAgentSendParts(stickerID, text, attachmentIDs); len(parts) > 0 {
		body["parts"] = parts
	}
	if cmd.Flags().Changed("show-in-channel") {
		show, _ := cmd.Flags().GetBool("show-in-channel")
		body["options"] = map[string]any{"show_in_channel": show}
	}
	var out map[string]any
	if err := client.PostJSON(ctx, "/api/agent/messages/send", body, &out); err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	return printAgentTransportOutput(cmd, out, "Message sent.")
}

// buildAgentSendParts assembles the structured chat message body for agent
// transport send. Order: sticker (if any), text (if any), then attachment parts
// in --attachment-id order. Attachments are never encoded as markdown embeds.
func buildAgentSendParts(stickerID, text string, attachmentIDs []string) []protocol.MessagePart {
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
	if err := postAgentTransport(cmd, "/api/agent/messages/react", body, &out); err != nil {
		return fmt.Errorf("react to message: %w", err)
	}
	return printAgentTransportOutput(cmd, out, "Reaction sent.")
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
	if output == "" || output == "json" {
		return cli.PrintJSON(os.Stdout, out)
	}
	if output != "text" {
		return fmt.Errorf("unsupported output format %q; use json or text", output)
	}
	if textFallback != "" {
		fmt.Fprintln(os.Stdout, textFallback)
		return nil
	}
	return cli.PrintJSON(os.Stdout, out)
}
