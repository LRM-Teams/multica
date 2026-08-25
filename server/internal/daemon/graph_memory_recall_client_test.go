package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const graphMemoryRecallPath = "/api/daemon/graph-memory/recalls"

func newGraphRecallTestDaemon(t *testing.T, baseURL string) *Daemon {
	t.Helper()
	d := New(Config{MemoryType: MemoryTypeGraph, ServerBaseURL: baseURL}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.client.SetWorkspaceDaemonToken("workspace-1", "daemon-token", time.Now().Add(time.Hour))
	return d
}

func graphRecallTestTask() Task {
	return Task{
		ID:          "task-1",
		RuntimeID:   "runtime-1",
		WorkspaceID: "workspace-1",
		ChatMessage: "How should dispatch retries work?",
	}
}

func TestGraphExecutionMemoriesRequestsServerRecall(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != graphMemoryRecallPath {
			t.Fatalf("request = %s %s, want POST %s", r.Method, r.URL.Path, graphMemoryRecallPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer daemon-token" {
			t.Fatalf("Authorization = %q, want daemon capability", got)
		}
		var request struct {
			TraceID   string `json:"trace_id"`
			TaskID    string `json:"task_id"`
			RuntimeID string `json:"runtime_id"`
			Query     string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if _, err := uuid.Parse(request.TraceID); err != nil || request.TaskID != "task-1" || request.RuntimeID != "runtime-1" || request.Query != "How should dispatch retries work?" {
			t.Fatalf("request body = %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"recall_id":"recall-1","trace_id":"`+request.TraceID+`","status":"accepted","replayed":false,"k":1,"graph_kind":"project","graph_version":7,"found":true,"summary":"server summary","citations":[{"node_id":"node-1","level":0,"epistemic":"asserted"}],"rounds":2,"injection":"## Graph Memory Recall\nserver summary"}`)
	}))
	defer server.Close()

	d := newGraphRecallTestDaemon(t, server.URL)
	memories := d.graphExecutionMemories(context.Background(), graphRecallTestTask(), d.logger)
	if hits.Load() != 1 {
		t.Fatalf("recall requests = %d, want 1", hits.Load())
	}
	if len(memories) != 1 || memories[0].Name != "Graph memory recall" || memories[0].Content != "## Graph Memory Recall\nserver summary" || memories[0].Scope != "workspace" {
		t.Fatalf("memories = %+v, want one server injection", memories)
	}
}

func TestGraphExecutionMemoriesNonInjectionOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "disabled", status: http.StatusOK, body: `{"status":"disabled"}`},
		{name: "no scope", status: http.StatusOK, body: `{"status":"no_scope"}`},
		{name: "not found", status: http.StatusNotFound, body: `{"error":"not found"}`},
		{name: "conflict", status: http.StatusConflict, body: `{"error":"RECALL_CONFLICT"}`},
		{name: "not found result", status: http.StatusAccepted, body: `{"status":"accepted","found":false,"injection":""}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			d := newGraphRecallTestDaemon(t, server.URL)
			if memories := d.graphExecutionMemories(context.Background(), graphRecallTestTask(), d.logger); memories != nil {
				t.Fatalf("memories = %+v, want nil", memories)
			}
		})
	}
}

func TestGraphExecutionMemoriesRecallTransportFailureIsNonFatal(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer server.Close()

	d := newGraphRecallTestDaemon(t, server.URL)
	d.logger = logger
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if memories := d.graphExecutionMemories(ctx, graphRecallTestTask(), logger); memories != nil {
		t.Fatalf("memories = %+v, want nil", memories)
	}
	close(release)
	if !strings.Contains(logs.String(), "graph memory recall failed") {
		t.Fatalf("logs = %q, want non-fatal recall failure", logs.String())
	}
}

func TestGraphExecutionMemoriesSkipsRecallWhenGated(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	t.Run("legacy profile", func(t *testing.T) {
		d := newGraphRecallTestDaemon(t, server.URL)
		task := graphRecallTestTask()
		task.MemoryType = MemoryTypeLegacy
		if memories := d.graphExecutionMemories(context.Background(), task, d.logger); memories != nil {
			t.Fatalf("memories = %+v, want nil", memories)
		}
	})
	t.Run("empty query", func(t *testing.T) {
		d := newGraphRecallTestDaemon(t, server.URL)
		task := graphRecallTestTask()
		task.ChatMessage = ""
		if memories := d.graphExecutionMemories(context.Background(), task, d.logger); memories != nil {
			t.Fatalf("memories = %+v, want nil", memories)
		}
	})
	if hits.Load() != 0 {
		t.Fatalf("recall requests = %d, want 0", hits.Load())
	}
}

func TestRequestGraphMemoryRecallReturnsTypedStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"RECALL_CONFLICT"}`)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	client.SetWorkspaceDaemonToken("workspace-1", "daemon-token", time.Now().Add(time.Hour))
	_, err := client.RequestGraphMemoryRecall(context.Background(), "workspace-1", protocol.GraphMemoryRecallRequest{
		TraceID: "trace-1", TaskID: "task-1", RuntimeID: "runtime-1", Query: "query",
	})
	var requestErr *requestError
	if !errors.As(err, &requestErr) || requestErr.StatusCode != http.StatusConflict {
		t.Fatalf("error = %v, want typed 409 request error", err)
	}
}
