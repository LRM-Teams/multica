package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var attachmentCmd = &cobra.Command{
	Use:   "attachment",
	Short: "Work with attachments",
}

var attachmentViewCmd = &cobra.Command{
	Use:   "view [attachment-id]",
	Short: "Download an attachment to a local file path",
	Long:  "Download an attachment by its ID and write the bytes to --output (a file path, not a directory).",
	Example: `  # Positional id
  $ multica attachment view abc123 --output /tmp/a.png

  # Flag id (Raft-compatible)
  $ multica attachment view --id abc123 --output /tmp/a.png`,
	Args: cobra.MaximumNArgs(1),
	RunE: runAttachmentView,
}

var attachmentUploadCmd = &cobra.Command{
	Use:   "upload",
	Short: "Upload a local file as an attachment",
	Long: "Upload a local file and print its attachment id. Use the id with " +
		"`multica message send --attachment-id` or `multica issue create --attachment-id`.\n\n" +
		"An Agent upload requires the same canonical --target as its later message send. " +
		"It creates a bounded Upload Session, uploads directly to its authorized destination, " +
		"then asks the Server to verify the resulting object before returning an attachment id.",
	Example: `  $ multica attachment upload --path ./shot.png --target '#eng'
  $ multica attachment upload --path ./notes.md --target 'dm:@user'
  $ printf 'see file\n' | multica message send --target 'dm:@user' --attachment-id <id>`,
	Args: cobra.NoArgs,
	RunE: runAttachmentUpload,
}

func init() {
	attachmentCmd.AddCommand(attachmentViewCmd)
	attachmentCmd.AddCommand(attachmentUploadCmd)
	attachmentCmd.AddCommand(attachmentListCmd)

	attachmentViewCmd.Flags().String("id", "", "Attachment UUID (transition alias; prefer positional <attachment-id>)")
	attachmentViewCmd.Flags().String("output", "", "Local file path to write the downloaded bytes to (required)")
	_ = attachmentViewCmd.MarkFlagRequired("output")

	attachmentUploadCmd.Flags().String("path", "", "Absolute or relative path to the local file to upload (required)")
	attachmentUploadCmd.Flags().String("target", "", "Agent upload target (required for Agent tokens): #channel, #channel:<threadId>, dm:@handle, or dm:@handle:<threadId>")
	attachmentUploadCmd.Flags().String("resume-session", "", "Retry a pending Agent upload session with --path")
	attachmentUploadCmd.Flags().String("cancel-session", "", "Cancel a pending Agent upload session")

	attachmentListCmd.Flags().String("issue", "", "Issue id or key (list issue attachments)")
	attachmentListCmd.Flags().String("channel", "", "Channel name or UUID (list channel attachments)")
	attachmentListCmd.Flags().String("output", "table", "Output format: table|json")
}

// resolveAttachmentViewID returns the attachment id from either a positional
// arg or --id. Both or neither is an error.
func resolveAttachmentViewID(positional, flagID string) (string, error) {
	positional = strings.TrimSpace(positional)
	flagID = strings.TrimSpace(flagID)
	if positional != "" && flagID != "" {
		return "", fmt.Errorf("pass the attachment id either positionally or with --id, not both")
	}
	id := positional
	if id == "" {
		id = flagID
	}
	if id == "" {
		return "", fmt.Errorf("attachment id is required (pass <attachment-id> or --id)")
	}
	return id, nil
}

func runAttachmentView(cmd *cobra.Command, args []string) error {
	positional := ""
	if len(args) > 0 {
		positional = args[0]
	}
	flagID, _ := cmd.Flags().GetString("id")
	id, err := resolveAttachmentViewID(positional, flagID)
	if err != nil {
		return err
	}
	output, _ := cmd.Flags().GetString("output")
	output = strings.TrimSpace(output)
	if output == "" {
		return fmt.Errorf("--output is required")
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), cli.AtLeastAPITimeout(60*time.Second))
	defer cancel()

	var att map[string]any
	attPath := "/api/attachments/" + id
	if isAgentAPIToken(cmd) {
		attPath = "/api/agent/attachments/" + id
	}
	if err := client.GetJSON(ctx, attPath, &att); err != nil {
		return fmt.Errorf("get attachment: %w", err)
	}

	downloadURL := strVal(att, "download_url")
	if downloadURL == "" {
		return fmt.Errorf("attachment has no download URL")
	}

	filename := filepath.Base(strVal(att, "filename"))
	if filename == "" || filename == "." {
		filename = id
	}

	data, err := client.DownloadFile(ctx, downloadURL)
	if err != nil {
		return fmt.Errorf("download file: %w", err)
	}

	if err := os.WriteFile(output, data, 0o644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	abs, err := filepath.Abs(output)
	if err != nil {
		abs = output
	}
	fmt.Fprintln(os.Stderr, "Downloaded:", abs)

	return cli.PrintJSON(os.Stdout, map[string]any{
		"id":       strVal(att, "id"),
		"filename": filename,
		"path":     abs,
		"size":     strVal(att, "size_bytes"),
	})
}

func runAttachmentUpload(cmd *cobra.Command, _ []string) error {
	resumeSession, _ := cmd.Flags().GetString("resume-session")
	cancelSession, _ := cmd.Flags().GetString("cancel-session")
	resumeSession = strings.TrimSpace(resumeSession)
	cancelSession = strings.TrimSpace(cancelSession)
	if resumeSession != "" && cancelSession != "" {
		return fmt.Errorf("pass either --resume-session or --cancel-session, not both")
	}
	if cancelSession != "" {
		if _, err := uuid.Parse(cancelSession); err != nil {
			return fmt.Errorf("--cancel-session must be a UUID")
		}
		if !isAgentAPIToken(cmd) {
			return fmt.Errorf("--cancel-session is available only with an Agent token")
		}
		client, err := newAPIClient(cmd)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), cli.AtLeastAPITimeout(60*time.Second))
		defer cancel()
		return runAgentAttachmentUploadSessionCancel(ctx, client, cancelSession)
	}
	path, _ := cmd.Flags().GetString("path")
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("--path is required")
	}
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("--path does not exist: %w", err)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("--path is not a regular file: %s", path)
	}
	if st.Size() == 0 {
		return fmt.Errorf("--path is empty; refusing to upload a 0-byte attachment")
	}
	target, _ := cmd.Flags().GetString("target")
	target = strings.TrimSpace(target)
	if isAgentAPIToken(cmd) && target == "" && resumeSession == "" {
		return fmt.Errorf("--target is required for attachment upload")
	}
	if resumeSession != "" {
		if _, err := uuid.Parse(resumeSession); err != nil {
			return fmt.Errorf("--resume-session must be a UUID")
		}
		if !isAgentAPIToken(cmd) {
			return fmt.Errorf("--resume-session is available only with an Agent token")
		}
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), cli.AtLeastAPITimeout(60*time.Second))
	defer cancel()
	if isAgentAPIToken(cmd) {
		if resumeSession != "" {
			return runAgentAttachmentUploadSessionResume(ctx, client, resumeSession, path, st.Size())
		}
		return runAgentAttachmentUploadSession(ctx, client, path, st.Size(), target)
	}

	channelID, err := resolveChannelIDFromUploadTarget(ctx, client, target)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	uploadOpts := cli.UploadFileOptions{ChannelID: channelID}
	id, err := client.UploadFileOpts(ctx, data, path, uploadOpts)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}

	fmt.Fprintf(os.Stderr, "File uploaded: %s (%s)\n", filepath.Base(path), formatByteSize(int64(len(data))))
	fmt.Fprintf(os.Stderr, "Attachment ID: %s\n\n", id)
	fmt.Fprintf(os.Stderr, "Use this ID with multica message send --attachment-id %s (or issue create --attachment-id).\n", id)

	out := map[string]any{
		"id":       id,
		"filename": filepath.Base(path),
		"path":     path,
		"size":     len(data),
	}
	if channelID != "" {
		out["channel_id"] = channelID
	}
	return cli.PrintJSON(os.Stdout, out)
}

type agentAttachmentUploadCapabilities struct {
	MaxSizeBytes      int64 `json:"max_size_bytes"`
	SessionTTLSeconds int64 `json:"session_ttl_seconds"`
}

type agentAttachmentUploadSession struct {
	ID             string            `json:"id"`
	UploadURL      string            `json:"upload_url"`
	Method         string            `json:"method"`
	Headers        map[string]string `json:"headers"`
	State          string            `json:"state"`
	ContentType    string            `json:"content_type"`
	SizeBytes      int64             `json:"size_bytes"`
	ChecksumSHA256 string            `json:"checksum_sha256"`
	AttachmentID   string            `json:"attachment_id"`
}

func runAgentAttachmentUploadSession(ctx context.Context, client *cli.APIClient, filePath string, sizeBytes int64, target string) error {
	var capabilities agentAttachmentUploadCapabilities
	if err := client.GetJSON(ctx, "/api/agent/attachment-upload-capabilities", &capabilities); err != nil {
		return fmt.Errorf("get attachment upload capabilities: %w", err)
	}
	if capabilities.MaxSizeBytes <= 0 {
		return fmt.Errorf("attachment upload capabilities returned an invalid size limit")
	}
	if sizeBytes > capabilities.MaxSizeBytes {
		return fmt.Errorf("file exceeds server upload size limit (%s)", formatByteSize(capabilities.MaxSizeBytes))
	}
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer file.Close()
	contentType, err := detectAttachmentUploadContentType(file, filepath.Base(filePath))
	if err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind file: %w", err)
	}
	checksumSHA256, err := attachmentUploadChecksum(file)
	if err != nil {
		return err
	}
	clientRequestID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("create upload request id: %w", err)
	}
	var session agentAttachmentUploadSession
	if err := client.PostJSON(ctx, "/api/agent/attachment-upload-sessions", map[string]any{
		"target":            target,
		"filename":          filepath.Base(filePath),
		"content_type":      contentType,
		"size_bytes":        sizeBytes,
		"checksum_sha256":   checksumSHA256,
		"client_request_id": clientRequestID.String(),
	}, &session); err != nil {
		return fmt.Errorf("create attachment upload session: %w", err)
	}
	return completeAgentAttachmentUploadSession(ctx, client, file, filePath, sizeBytes, session)
}

func runAgentAttachmentUploadSessionResume(ctx context.Context, client *cli.APIClient, sessionID, filePath string, sizeBytes int64) error {
	var session agentAttachmentUploadSession
	if err := client.GetJSON(ctx, "/api/agent/attachment-upload-sessions/"+sessionID, &session); err != nil {
		return fmt.Errorf("get attachment upload session: %w", err)
	}
	if session.State == "completed" && strings.TrimSpace(session.AttachmentID) != "" {
		fmt.Fprintf(os.Stderr, "Upload session already completed. Attachment ID: %s\n", session.AttachmentID)
		return cli.PrintJSON(os.Stdout, map[string]any{"id": session.AttachmentID, "session_id": sessionID})
	}
	if session.State != "pending" {
		return fmt.Errorf("upload session is %s and cannot be retried", session.State)
	}
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer file.Close()
	contentType, err := detectAttachmentUploadContentType(file, filepath.Base(filePath))
	if err != nil {
		return err
	}
	checksumSHA256, err := attachmentUploadChecksum(file)
	if err != nil {
		return err
	}
	if sizeBytes != session.SizeBytes || !strings.EqualFold(contentType, session.ContentType) || !strings.EqualFold(checksumSHA256, session.ChecksumSHA256) {
		return fmt.Errorf("local file does not match the declared upload session metadata")
	}
	if err := client.PostJSON(ctx, "/api/agent/attachment-upload-sessions/"+sessionID+"/retry", map[string]any{}, &session); err != nil {
		return fmt.Errorf("retry attachment upload session: %w", err)
	}
	return completeAgentAttachmentUploadSession(ctx, client, file, filePath, sizeBytes, session)
}

func runAgentAttachmentUploadSessionCancel(ctx context.Context, client *cli.APIClient, sessionID string) error {
	var session agentAttachmentUploadSession
	if err := client.PostJSON(ctx, "/api/agent/attachment-upload-sessions/"+sessionID+"/cancel", map[string]any{}, &session); err != nil {
		return fmt.Errorf("cancel attachment upload session: %w", err)
	}
	if session.State != "cancelled" {
		return fmt.Errorf("cancel attachment upload session returned state %q", session.State)
	}
	fmt.Fprintf(os.Stderr, "Upload session cancelled: %s\n", session.ID)
	return cli.PrintJSON(os.Stdout, map[string]any{"id": session.ID, "state": session.State})
}

func completeAgentAttachmentUploadSession(ctx context.Context, client *cli.APIClient, file *os.File, filePath string, sizeBytes int64, session agentAttachmentUploadSession) error {
	if strings.TrimSpace(session.ID) == "" || strings.TrimSpace(session.UploadURL) == "" {
		return fmt.Errorf("attachment upload session returned no destination")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind file: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Upload session: %s\n", session.ID)
	if err := client.UploadToDestination(ctx, session.UploadURL, session.Method, session.Headers, file, sizeBytes); err != nil {
		return fmt.Errorf("upload attachment bytes for session %s: %w (retry with multica attachment upload --path %q --resume-session %s)", session.ID, err, filePath, session.ID)
	}
	var attachment map[string]any
	if err := client.PostJSON(ctx, "/api/agent/attachment-upload-sessions/"+session.ID+"/complete", map[string]any{}, &attachment); err != nil {
		return fmt.Errorf("complete attachment upload session: %w", err)
	}
	id := strings.TrimSpace(strVal(attachment, "id"))
	if id == "" {
		return fmt.Errorf("completed upload response missing attachment id")
	}
	fmt.Fprintf(os.Stderr, "File uploaded: %s (%s)\n", filepath.Base(filePath), formatByteSize(sizeBytes))
	fmt.Fprintf(os.Stderr, "Attachment ID: %s\n\n", id)
	fmt.Fprintf(os.Stderr, "Use this ID with multica message send --attachment-id %s.\n", id)
	return cli.PrintJSON(os.Stdout, attachment)
}

func attachmentUploadChecksum(file *os.File) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("rewind file: %w", err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash file: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("rewind file: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func detectAttachmentUploadContentType(file *os.File, filename string) (string, error) {
	buf := make([]byte, 512)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read file type: %w", err)
	}
	if byExtension := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))); byExtension != "" {
		contentType, _, err := mime.ParseMediaType(byExtension)
		if err == nil && contentType != "" {
			return strings.ToLower(contentType), nil
		}
	}
	return http.DetectContentType(buf[:n]), nil
}

// resolveChannelIDFromUploadTarget maps an optional --target to a channel UUID
// for upload-time binding. Empty target → unbound upload. DM/thread forms are
// rejected with guidance to upload unbound and bind via message send.
func resolveChannelIDFromUploadTarget(ctx context.Context, client *cli.APIClient, target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", nil
	}
	if strings.HasPrefix(strings.ToLower(target), "dm:") {
		return "", fmt.Errorf("upload --target does not resolve DM targets; omit --target and pass --attachment-id to multica message send --target %q", target)
	}
	// Thread form: #channel:message-id — bind at send time instead.
	if i := strings.LastIndex(target, ":"); i > 0 {
		// UUID channel:msg is rare; treat any ":" after first char as thread form
		// unless the whole string is a bare UUID (no colon).
		if _, err := uuid.Parse(target); err != nil {
			return "", fmt.Errorf("upload --target does not resolve thread targets (%q); omit --target and pass --attachment-id to multica message send --target %q", target, target)
		}
	}
	if _, err := uuid.Parse(target); err == nil {
		return target, nil
	}

	name := strings.TrimPrefix(target, "#")
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("invalid --target %q", target)
	}

	var channels []map[string]any
	listPath := "/api/channels"
	if isAgentAPITokenAmbient() {
		listPath = "/api/agent/channels"
	}
	if err := client.GetJSON(ctx, listPath, &channels); err != nil {
		return "", fmt.Errorf("list channels: %w", err)
	}

	var exact, insensitive []map[string]any
	for _, ch := range channels {
		n, _ := ch["name"].(string)
		if n == name {
			exact = append(exact, ch)
		} else if strings.EqualFold(n, name) {
			insensitive = append(insensitive, ch)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = insensitive
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no joined channel matching %q", target)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("ambiguous channel target %q (%d matches)", target, len(matches))
	}
	id, _ := matches[0]["id"].(string)
	if id == "" {
		return "", fmt.Errorf("channel %q has no id", name)
	}
	return id, nil
}

func formatByteSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
}

var attachmentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List attachments for an issue or channel",
	Long:  "List attachments bound to an issue (--issue) or channel (--channel). Exactly one of the two flags is required.",
	RunE:  runAttachmentList,
}

func runAttachmentList(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	issueFlag, _ := cmd.Flags().GetString("issue")
	channelFlag, _ := cmd.Flags().GetString("channel")
	issueFlag = strings.TrimSpace(issueFlag)
	channelFlag = strings.TrimSpace(channelFlag)
	if (issueFlag == "") == (channelFlag == "") {
		return fmt.Errorf("exactly one of --issue or --channel is required")
	}

	var path string
	if issueFlag != "" {
		issueRef, err := resolveIssueRef(ctx, client, issueFlag)
		if err != nil {
			return fmt.Errorf("resolve issue: %w", err)
		}
		path = "/api/issues/" + url.PathEscape(issueRef.ID) + "/attachments"
		if isAgentAPIToken(cmd) {
			path = "/api/agent/issues/" + url.PathEscape(issueRef.ID) + "/attachments"
		}
	} else {
		channelID, err := resolveChannelRef(ctx, client, channelFlag)
		if err != nil {
			return fmt.Errorf("resolve channel: %w", err)
		}
		path = "/api/channels/" + url.PathEscape(channelID) + "/attachments"
		if isAgentAPIToken(cmd) {
			path = "/api/agent/channels/" + url.PathEscape(channelID) + "/attachments"
		}
	}

	var result any
	if err := client.GetJSON(ctx, path, &result); err != nil {
		return fmt.Errorf("list attachments: %w", err)
	}
	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, result)
	}
	raw, _ := result.([]any)
	if raw == nil {
		if m, ok := result.(map[string]any); ok {
			if arr, ok := m["attachments"].([]any); ok {
				raw = arr
			}
		}
	}
	headers := []string{"ID", "NAME", "TYPE", "SIZE"}
	rows := make([][]string, 0, len(raw))
	for _, item := range raw {
		a, ok := item.(map[string]any)
		if !ok {
			continue
		}
		rows = append(rows, []string{
			strVal(a, "id"),
			firstNonEmpty(strVal(a, "filename"), strVal(a, "name")),
			strVal(a, "content_type"),
			fmt.Sprintf("%v", a["size"]),
		})
	}
	cli.PrintTable(os.Stdout, headers, rows)
	return nil
}
