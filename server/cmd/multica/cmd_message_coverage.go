package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon"
)

const (
	messageCoverageReceiptField = daemon.MessageCoverageReceiptField
	messageCoverageCommitPath   = daemon.MessageCoverageCommitPath
	maxMessageCoverageResponse  = 1 << 20
	maxAgentProxyTokenBytes     = 4 << 10
)

type messageCoverageCommitFunc func(context.Context, string) error

// consumeMessageCoverageResponse is the shared command seam. It owns the
// decode -> visible output -> local commit order so individual message commands
// cannot accidentally advance coverage before their selected formatter returns.
func consumeMessageCoverageResponse(
	ctx context.Context,
	r io.Reader,
	w io.Writer,
	out any,
	writeOutput func(io.Writer) error,
	commit messageCoverageCommitFunc,
) error {
	receiptID, err := decodeMessageCoverageResponse(r, out)
	if err != nil {
		return fmt.Errorf("decode message coverage response: %w", err)
	}
	return outputThenCommitMessageCoverage(ctx, w, receiptID, writeOutput, commit)
}

// decodeMessageCoverageResponse removes the machine-local receipt before
// decoding output for the human/Agent-visible formatter. The receipt is never
// part of normal text or JSON output.
func decodeMessageCoverageResponse(r io.Reader, out any) (string, error) {
	if r == nil {
		return "", errors.New("message coverage response is unavailable")
	}
	var object map[string]json.RawMessage
	decoder := json.NewDecoder(io.LimitReader(r, maxMessageCoverageResponse+1))
	if err := decoder.Decode(&object); err != nil {
		return "", err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", errors.New("message coverage response contains trailing data")
	}

	receiptID := ""
	if raw, found := object[messageCoverageReceiptField]; found {
		if err := json.Unmarshal(raw, &receiptID); err != nil {
			return "", fmt.Errorf("decode internal coverage receipt: %w", err)
		}
		receiptID = strings.TrimSpace(receiptID)
		if receiptID == "" {
			return "", errors.New("internal coverage receipt is empty")
		}
		delete(object, messageCoverageReceiptField)
	}

	visible, err := json.Marshal(object)
	if err != nil {
		return "", fmt.Errorf("encode visible message response: %w", err)
	}
	if err := json.Unmarshal(visible, out); err != nil {
		return "", err
	}
	return receiptID, nil
}

// outputThenCommitMessageCoverage preserves safe replay: an output error makes
// no commit attempt, while a post-output commit failure is returned explicitly
// so the command cannot report the context as safely covered.
func outputThenCommitMessageCoverage(
	ctx context.Context,
	w io.Writer,
	receiptID string,
	writeOutput func(io.Writer) error,
	commit messageCoverageCommitFunc,
) error {
	if writeOutput == nil {
		return errors.New("message output formatter is unavailable")
	}
	if err := writeOutput(w); err != nil {
		return fmt.Errorf("write message output: %w", err)
	}
	receiptID = strings.TrimSpace(receiptID)
	if receiptID == "" {
		return nil
	}
	if commit == nil {
		return errors.New("message output was written but coverage commit is unavailable; context may be replayed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := commit(ctx, receiptID); err != nil {
		return fmt.Errorf("message output was written but coverage commit failed; context may be replayed: %w", err)
	}
	return nil
}

// commitLocalMessageCoverage authenticates only through the launch-pinned
// Agent Proxy token file. Workspace and Agent request fields are deliberately
// absent; the Machine Service resolves their fixed scope from the token.
func commitLocalMessageCoverage(ctx context.Context, receiptID string) error {
	receiptID = strings.TrimSpace(receiptID)
	if receiptID == "" {
		return errors.New("coverage receipt is required")
	}
	proxyURL, err := localAgentProxyURL()
	if err != nil {
		return err
	}
	token, err := readAgentProxyTokenFile(strings.TrimSpace(os.Getenv(daemon.AgentProxyTokenFileEnv)))
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{"receipt_id": receiptID})
	if err != nil {
		return fmt.Errorf("encode coverage commit: %w", err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestContext, cancel := context.WithTimeout(ctx, cli.APITimeout())
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, proxyURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("prepare coverage commit: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(daemon.AgentProxyTokenHeader, token)
	response, err := (&http.Client{Timeout: cli.APITimeout()}).Do(request)
	if err != nil {
		return fmt.Errorf("commit coverage through Agent Proxy: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("commit coverage through Agent Proxy: %s", response.Status)
	}
	var result struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<10)).Decode(&result); err != nil {
		return fmt.Errorf("decode coverage commit response: %w", err)
	}
	if result.Status != "committed" {
		return fmt.Errorf("coverage commit returned unexpected status %q", result.Status)
	}
	return nil
}

func localAgentProxyURL() (string, error) {
	raw := strings.TrimSpace(os.Getenv(daemon.AgentProxyURLEnv))
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("message coverage commit requires a valid %s", daemon.AgentProxyURLEnv)
	}
	host := parsed.Hostname()
	address := net.ParseIP(host)
	if host != "localhost" && (address == nil || !address.IsLoopback()) {
		return "", fmt.Errorf("%s must use a loopback address", daemon.AgentProxyURLEnv)
	}
	parsed.Path = messageCoverageCommitPath
	return parsed.String(), nil
}

func readAgentProxyTokenFile(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("message coverage commit requires an absolute %s", daemon.AgentProxyTokenFileEnv)
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return "", errors.New("Agent Proxy token file is unavailable")
	}
	if !pathInfo.Mode().IsRegular() {
		return "", errors.New("Agent Proxy token file must be a regular file, not a link")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("Agent Proxy token file is unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", errors.New("Agent Proxy token file is unavailable")
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxAgentProxyTokenBytes {
		return "", errors.New("Agent Proxy token file is not a bounded regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("Agent Proxy token file permissions must be 0600 or stricter")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxAgentProxyTokenBytes+1))
	if err != nil {
		return "", errors.New("Agent Proxy token file is unreadable")
	}
	token := strings.TrimSpace(string(raw))
	if token == "" || len(raw) > maxAgentProxyTokenBytes {
		return "", errors.New("Agent Proxy token file is invalid")
	}
	return token, nil
}
