package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestValidateEnvRelativePath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw     string
		want    string
		wantErr string
	}{
		{raw: "output/answer.json", want: "output/answer.json"},
		{raw: "notes.md", want: "notes.md"},
		{raw: " data/task.json ", want: "data/task.json"},
		{raw: "foo/./bar", want: "foo/bar"},
		{raw: "", wantErr: "non-empty"},
		{raw: "   ", wantErr: "non-empty"},
		{raw: "/etc/passwd", wantErr: "workspace-relative"},
		{raw: "../evil.txt", wantErr: "workspace-relative"},
		{raw: "foo/../bar", wantErr: "workspace-relative"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := validateEnvRelativePath(tc.raw, "path")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCollectEnvDispatchStagedFilesMergesRecipeFiles(t *testing.T) {
	t.Parallel()
	files, err := collectEnvDispatchStagedFiles(EnvDispatchRequest{
		StageFiles: []EnvDispatchStagedFile{
			{Path: "notes.md", Content: "hi"},
			{Path: "data/task.json", Content: "{}"},
		},
		Environment: &EnvDispatchEnvironment{
			Image: "gdpevo:022",
			Files: []EnvDispatchStagedFile{
				{Path: "output/seed.txt", Content: "seed"},
				{Path: "skip-me", Content: ""},
			},
		},
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("got %d files, want 3: %+v", len(files), files)
	}
	if files[0].Path != "notes.md" || files[1].Path != "data/task.json" || files[2].Path != "output/seed.txt" {
		t.Fatalf("unexpected paths: %+v", files)
	}
}

func TestCollectEnvDispatchStagedFilesRejectsEscapes(t *testing.T) {
	t.Parallel()
	_, err := collectEnvDispatchStagedFiles(EnvDispatchRequest{
		StageFiles: []EnvDispatchStagedFile{{Path: "../evil.txt", Content: "x"}},
	})
	if err == nil || !strings.Contains(err.Error(), "workspace-relative") {
		t.Fatalf("err = %v, want workspace-relative", err)
	}
}

func TestEnvDispatch_AcceptsStageFilesAndEnvironment(t *testing.T) {
	body := `{
		"mode":"scratch",
		"env_id":"` + validUUID + `",
		"domain":"self_play",
		"dispatch_type":"message",
		"group_size":1,
		"agent_id":"` + validUUID + `",
		"message":{"content":"hi"},
		"stage_files":[{"path":"notes.md","content":"hi"}],
		"environment":{"image":"gdpevo:022","services":["db"],"files":[{"path":"data/task.json","content":"{}"}]}
	}`
	rr := doEnvDispatch(t, body)
	if rr.Code == http.StatusBadRequest && strings.Contains(rr.Body.String(), "malformed request body") {
		t.Fatalf("stage_files/environment must be known fields, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestEnvDispatch_RejectsEscapedStageFiles(t *testing.T) {
	body := `{
		"mode":"scratch",
		"env_id":"` + validUUID + `",
		"domain":"self_play",
		"dispatch_type":"message",
		"group_size":1,
		"agent_id":"` + validUUID + `",
		"message":{"content":"hi"},
		"stage_files":[{"path":"../evil.txt","content":"x"}]
	}`
	rr := doEnvDispatch(t, body)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "workspace-relative") {
		t.Fatalf("escaped stage_files = %d %s, want 400 workspace-relative", rr.Code, rr.Body.String())
	}
}

func TestDownloadEnvDispatchFile_RequiresAuth(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/env-dispatch/"+validUUID+"/files?path=output/answer.json", nil)
	h.DownloadEnvDispatchFile(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestDownloadEnvDispatchFile_RejectsEscapedPath(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/env-dispatch/"+validUUID+"/files?path="+url.QueryEscape("../evil.txt"), nil)
	r.Header.Set("X-User-ID", "u1")
	r = r.WithContext(middleware.SetMemberContext(r.Context(), "ws1", db.Member{}))
	r = withURLParam(r, "projectID", validUUID)
	h.DownloadEnvDispatchFile(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "workspace-relative") {
		t.Fatalf("status = %d %s, want 400 workspace-relative", w.Code, w.Body.String())
	}
}

func TestUploadEnvDispatchFile_RejectsEscapedPath(t *testing.T) {
	h := newTestHandler(Config{})
	body, _ := json.Marshal(EnvDispatchStagedFile{Path: "/etc/passwd", Content: "x"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/env-dispatch/"+validUUID+"/files", bytes.NewReader(body))
	r.Header.Set("X-User-ID", "u1")
	r = r.WithContext(middleware.SetMemberContext(r.Context(), "ws1", db.Member{}))
	r = withURLParam(r, "projectID", validUUID)
	h.UploadEnvDispatchFile(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "workspace-relative") {
		t.Fatalf("status = %d %s, want 400 workspace-relative", w.Code, w.Body.String())
	}
}

func TestDownloadEnvDispatchChannelFile_RejectsInvalidUUID(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/env-dispatch/channels/not-a-uuid/files?path=output/answer.json", nil)
	r.Header.Set("X-User-ID", "u1")
	r = r.WithContext(middleware.SetMemberContext(r.Context(), "ws1", db.Member{}))
	r = withURLParam(r, "channelID", "not-a-uuid")
	h.DownloadEnvDispatchChannelFile(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d %s, want 400", w.Code, w.Body.String())
	}
}
