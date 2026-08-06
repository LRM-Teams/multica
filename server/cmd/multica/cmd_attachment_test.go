package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/cli"
)

func TestResolveAttachmentViewID(t *testing.T) {
	t.Parallel()

	id, err := resolveAttachmentViewID("abc", "")
	if err != nil || id != "abc" {
		t.Fatalf("positional: id=%q err=%v", id, err)
	}

	id, err = resolveAttachmentViewID("", "def")
	if err != nil || id != "def" {
		t.Fatalf("flag: id=%q err=%v", id, err)
	}

	if _, err := resolveAttachmentViewID("a", "b"); err == nil {
		t.Fatal("expected error when both positional and --id are set")
	}

	if _, err := resolveAttachmentViewID("", ""); err == nil {
		t.Fatal("expected error when neither id is set")
	}

	if _, err := resolveAttachmentViewID("  ", "  "); err == nil {
		t.Fatal("expected error for whitespace-only ids")
	}
}

func TestAttachmentCmdRegistersViewAndUploadNotDownload(t *testing.T) {
	t.Parallel()

	var names []string
	for _, c := range attachmentCmd.Commands() {
		names = append(names, c.Name())
	}
	has := func(n string) bool {
		for _, x := range names {
			if x == n {
				return true
			}
		}
		return false
	}
	if !has("view") {
		t.Fatalf("expected view subcommand, got %v", names)
	}
	if !has("upload") {
		t.Fatalf("expected upload subcommand, got %v", names)
	}
	if has("download") {
		t.Fatalf("download must be removed, got %v", names)
	}
}

func TestResolveChannelIDFromUploadTarget_UUID(t *testing.T) {
	t.Parallel()
	id := uuid.New().String()
	got, err := resolveChannelIDFromUploadTarget(t.Context(), nil, id)
	if err != nil {
		t.Fatal(err)
	}
	if got != id {
		t.Fatalf("got %q want %q", got, id)
	}
}

func TestResolveChannelIDFromUploadTarget_Empty(t *testing.T) {
	t.Parallel()
	got, err := resolveChannelIDFromUploadTarget(t.Context(), nil, "")
	if err != nil || got != "" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestResolveChannelIDFromUploadTarget_DMRejected(t *testing.T) {
	t.Parallel()
	_, err := resolveChannelIDFromUploadTarget(t.Context(), nil, "dm:@alice")
	if err == nil {
		t.Fatal("expected error for dm target")
	}
}

func TestResolveChannelIDFromUploadTarget_ThreadRejected(t *testing.T) {
	t.Parallel()
	_, err := resolveChannelIDFromUploadTarget(t.Context(), nil, "#eng:msg-123")
	if err == nil {
		t.Fatal("expected error for thread target")
	}
}

func TestRunAgentAttachmentUploadSessionUsesCapabilityDestinationAndCompletion(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "report.png")
	if err := os.WriteFile(filePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write upload fixture: %v", err)
	}

	var created map[string]any
	var uploaded bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/attachment-upload-capabilities":
			if r.Method != http.MethodGet {
				t.Errorf("capabilities method = %s", r.Method)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"max_size_bytes": 100, "session_ttl_seconds": 900})
		case "/api/agent/attachment-upload-sessions":
			if r.Method != http.MethodPost {
				t.Errorf("create method = %s", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Errorf("decode create upload session: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "session-1", "upload_url": "/api/agent/attachment-upload-sessions/session-1/object",
				"method": "PUT", "headers": map[string]string{"Content-Type": "image/png"},
			})
		case "/api/agent/attachment-upload-sessions/session-1/object":
			if r.Method != http.MethodPut {
				t.Errorf("upload method = %s", r.Method)
			}
			if r.Header.Get("Authorization") != "Bearer mat_test" || r.Header.Get("Content-Type") != "image/png" {
				t.Errorf("upload headers = %#v", r.Header)
			}
			data, _ := io.ReadAll(r.Body)
			uploaded = string(data) == "data"
			w.WriteHeader(http.StatusNoContent)
		case "/api/agent/attachment-upload-sessions/session-1/complete":
			if !uploaded {
				t.Error("completion arrived before upload")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "attachment-1", "filename": "report.png"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := cli.NewAPIClient(srv.URL, "workspace-1", "mat_test")
	if err := runAgentAttachmentUploadSession(t.Context(), client, filePath, 4, "#eng"); err != nil {
		t.Fatalf("run agent attachment upload session: %v", err)
	}
	digest := sha256.Sum256([]byte("data"))
	if created["target"] != "#eng" || created["filename"] != "report.png" || created["size_bytes"] != float64(4) || created["content_type"] != "image/png" || created["checksum_sha256"] != fmt.Sprintf("%x", digest) || created["client_request_id"] == "" {
		t.Fatalf("create session request = %#v", created)
	}
	if !uploaded {
		t.Fatal("session object was not uploaded")
	}
}

func TestRunAgentAttachmentUploadSessionResumeAndCancel(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "report.png")
	if err := os.WriteFile(filePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write upload fixture: %v", err)
	}
	digest := sha256.Sum256([]byte("data"))
	checksum := fmt.Sprintf("%x", digest)
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/agent/attachment-upload-sessions/session-1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "session-1", "state": "pending", "content_type": "image/png", "size_bytes": 4, "checksum_sha256": checksum,
			})
		case "/api/agent/attachment-upload-sessions/session-1/retry":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "session-1", "state": "pending", "upload_url": "/upload", "method": "PUT", "headers": map[string]string{"Content-Type": "image/png"},
			})
		case "/upload":
			data, _ := io.ReadAll(r.Body)
			if r.Method != http.MethodPut || string(data) != "data" {
				t.Errorf("retry upload = %s %q", r.Method, data)
			}
			w.WriteHeader(http.StatusNoContent)
		case "/api/agent/attachment-upload-sessions/session-1/complete":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "attachment-1"})
		case "/api/agent/attachment-upload-sessions/session-2/cancel":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "session-2", "state": "cancelled"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := cli.NewAPIClient(srv.URL, "workspace-1", "mat_test")
	if err := runAgentAttachmentUploadSessionResume(t.Context(), client, "session-1", filePath, 4); err != nil {
		t.Fatalf("resume upload session: %v", err)
	}
	if err := runAgentAttachmentUploadSessionCancel(t.Context(), client, "session-2"); err != nil {
		t.Fatalf("cancel upload session: %v", err)
	}
	wantPaths := []string{
		"GET /api/agent/attachment-upload-sessions/session-1",
		"POST /api/agent/attachment-upload-sessions/session-1/retry",
		"PUT /upload",
		"POST /api/agent/attachment-upload-sessions/session-1/complete",
		"POST /api/agent/attachment-upload-sessions/session-2/cancel",
	}
	if len(paths) != len(wantPaths) {
		t.Fatalf("paths = %v, want %v", paths, wantPaths)
	}
	for i, want := range wantPaths {
		if paths[i] != want {
			t.Fatalf("paths = %v, want %v", paths, wantPaths)
		}
	}
}
