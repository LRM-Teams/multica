package daemon

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Spec §1/A15: a graph recall failure injects nothing and never restores
// legacy project/channel/daily memory. The task continues with only its
// permitted non-graph memory.
func TestGraphExecutionMemoriesFailureInjectsNothing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"recall unavailable"}`)
	}))
	defer server.Close()

	d := newGraphRecallTestDaemon(t, server.URL)
	if out := d.graphExecutionMemories(context.Background(), graphRecallTestTask(), d.logger); out != nil {
		t.Fatalf("graphExecutionMemories = %+v, want no injection on recall failure", out)
	}
}
