package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon"
)

func TestOutputThenCommitMessageCoverageOrdersTextAndJSON(t *testing.T) {
	for _, test := range []struct {
		name  string
		raw   string
		write func(io.Writer, any) error
	}{
		{
			name: "text",
			raw:  `{"messages":[],"status":"complete","_coverage_receipt":"receipt-1"}`,
			write: func(w io.Writer, decoded any) error {
				result := decoded.(map[string]any)
				_, err := io.WriteString(w, result["status"].(string)+"\n")
				return err
			},
		},
		{
			name: "json",
			raw:  `{"action":"message_read","messages":[],"_coverage_receipt":"receipt-1"}`,
			write: func(w io.Writer, decoded any) error {
				return cli.PrintJSON(w, decoded)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var decoded map[string]any
			var output bytes.Buffer
			var events []string
			err := consumeMessageCoverageResponse(
				context.Background(),
				strings.NewReader(test.raw),
				&output,
				&decoded,
				func(w io.Writer) error {
					events = append(events, "output")
					return test.write(w, decoded)
				},
				func(_ context.Context, receiptID string) error {
					events = append(events, "commit:"+receiptID)
					return nil
				},
			)
			if err != nil {
				t.Fatalf("consume coverage response: %v", err)
			}
			if _, leaked := decoded["_coverage_receipt"]; leaked {
				t.Fatalf("decoded output leaked internal receipt: %#v", decoded)
			}
			if !reflect.DeepEqual(events, []string{"output", "commit:receipt-1"}) {
				t.Fatalf("events = %v", events)
			}
			if test.name == "json" {
				var printed map[string]any
				if err := json.Unmarshal(output.Bytes(), &printed); err != nil {
					t.Fatalf("printed JSON is invalid: %v: %s", err, output.String())
				}
				if _, leaked := printed["_coverage_receipt"]; leaked {
					t.Fatalf("printed JSON leaked receipt: %#v", printed)
				}
			}
		})
	}
}

func TestOutputThenCommitMessageCoverageDoesNotCommitAfterOutputFailure(t *testing.T) {
	outputErr := errors.New("stdout closed")
	commitCalls := 0
	err := outputThenCommitMessageCoverage(
		context.Background(),
		messageCoverageFailingWriter{err: outputErr},
		"receipt-1",
		func(w io.Writer) error {
			_, err := io.WriteString(w, "partial output")
			return err
		},
		func(context.Context, string) error {
			commitCalls++
			return nil
		},
	)
	if !errors.Is(err, outputErr) {
		t.Fatalf("error = %v, want output error", err)
	}
	if commitCalls != 0 {
		t.Fatalf("commit calls = %d, want 0", commitCalls)
	}
}

type messageCoverageFailingWriter struct{ err error }

func (w messageCoverageFailingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestConsumeMessageCoverageResponseDoesNotOutputOrCommitMalformedReceipt(t *testing.T) {
	outputCalls := 0
	commitCalls := 0
	var decoded map[string]any
	err := consumeMessageCoverageResponse(
		context.Background(),
		strings.NewReader(`{"messages":[],"_coverage_receipt":{"receipt_id":"receipt-1"}}`),
		io.Discard,
		&decoded,
		func(io.Writer) error {
			outputCalls++
			return nil
		},
		func(context.Context, string) error {
			commitCalls++
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "internal coverage receipt") {
		t.Fatalf("error = %v, want malformed receipt error", err)
	}
	if outputCalls != 0 || commitCalls != 0 {
		t.Fatalf("malformed receipt output=%d commit=%d, want 0/0", outputCalls, commitCalls)
	}
}

func TestOutputThenCommitMessageCoverageReportsPostOutputCommitFailure(t *testing.T) {
	commitErr := errors.New("local proxy unavailable")
	var output bytes.Buffer
	err := outputThenCommitMessageCoverage(
		context.Background(),
		&output,
		"receipt-1",
		func(w io.Writer) error {
			_, err := io.WriteString(w, "visible output\n")
			return err
		},
		func(context.Context, string) error { return commitErr },
	)
	if output.String() != "visible output\n" {
		t.Fatalf("output = %q", output.String())
	}
	if !errors.Is(err, commitErr) || !strings.Contains(err.Error(), "output was written") || !strings.Contains(err.Error(), "replayed") {
		t.Fatalf("error = %v, want visible post-output commit failure", err)
	}
}

func TestOutputThenCommitMessageCoverageKeepsLegacyReceiptlessOutputWorking(t *testing.T) {
	commitCalls := 0
	err := outputThenCommitMessageCoverage(
		context.Background(),
		io.Discard,
		"",
		func(io.Writer) error { return nil },
		func(context.Context, string) error {
			commitCalls++
			return nil
		},
	)
	if err != nil {
		t.Fatalf("receiptless output: %v", err)
	}
	if commitCalls != 0 {
		t.Fatalf("legacy receiptless output attempted %d commits", commitCalls)
	}
}

func TestCommitLocalMessageCoverageUsesPinnedProxyCredential(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/credential-proxy/messages/coverage/commit" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get(daemon.AgentProxyTokenHeader); got != "map_test-token" {
			t.Errorf("Agent Proxy token header = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode commit request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "committed"})
	}))
	defer server.Close()

	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("map_test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(daemon.AgentProxyURLEnv, server.URL)
	t.Setenv(daemon.AgentProxyTokenFileEnv, tokenFile)

	if err := commitLocalMessageCoverage(context.Background(), "receipt-1"); err != nil {
		t.Fatalf("commit local coverage: %v", err)
	}
	if !reflect.DeepEqual(requestBody, map[string]any{"receipt_id": "receipt-1"}) {
		t.Fatalf("commit request = %#v", requestBody)
	}
}

func TestCommitLocalMessageCoverageRejectsPermissiveTokenFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits do not represent the Windows token ACL")
	}
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("map_test-token"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(daemon.AgentProxyURLEnv, "http://127.0.0.1:19514")
	t.Setenv(daemon.AgentProxyTokenFileEnv, tokenFile)

	err := commitLocalMessageCoverage(context.Background(), "receipt-1")
	if err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("error = %v, want private token-file permissions error", err)
	}
}
