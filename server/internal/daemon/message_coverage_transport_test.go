package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMessageCoverageCommitRouteIsRemoved(t *testing.T) {
	path := filepath.Join("..", "..", "cmd", "server", "router.go")
	router, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(router), "/credential-proxy/messages/coverage/commit") {
		t.Fatal("removed receipt endpoint is still registered")
	}
}
