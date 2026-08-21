package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/memoryrecall"
)

func TestMemorySearchAndGetCommands(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "projects", "proj-1", "MEMORY.md"), "- Fixture conflicts cause make check retry loops.\n")
	writeFile(t, filepath.Join(root, "users", "other", "USER.md"), "- Secret preference.\n")

	searchOut, err := captureStdout(t, func() error {
		cmd := memorySearchCmd
		if err := cmd.Flags().Set("agent-root", root); err != nil {
			return err
		}
		if err := cmd.Flags().Set("project-id", "proj-1"); err != nil {
			return err
		}
		if err := cmd.Flags().Set("output", "json"); err != nil {
			return err
		}
		return runMemorySearch(cmd, []string{"fixture conflict"})
	})
	if err != nil {
		t.Fatal(err)
	}
	var search memoryrecall.SearchResult
	if err := json.Unmarshal([]byte(searchOut), &search); err != nil {
		t.Fatalf("search json: %v\n%s", err, searchOut)
	}
	if len(search.Hits) == 0 || search.Hits[0].Path != "projects/proj-1/MEMORY.md" {
		t.Fatalf("search hits = %+v", search.Hits)
	}

	getOut, err := captureStdout(t, func() error {
		cmd := memoryGetCmd
		if err := cmd.Flags().Set("agent-root", root); err != nil {
			return err
		}
		if err := cmd.Flags().Set("project-id", "proj-1"); err != nil {
			return err
		}
		if err := cmd.Flags().Set("output", "json"); err != nil {
			return err
		}
		return runMemoryGet(cmd, []string{"projects/proj-1/MEMORY.md"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(getOut, "Fixture conflicts") {
		t.Fatalf("get = %s", getOut)
	}

	if err := runMemoryGet(memoryGetCmd, []string{"users/other/USER.md"}); err == nil {
		t.Fatal("expected other-member get to fail")
	}
}

func TestMemorySearchRejectsEmptyRoot(t *testing.T) {
	t.Setenv("MULTICA_AGENT_ROOT", "")
	cmd := memorySearchCmd
	_ = cmd.Flags().Set("agent-root", "")
	if err := runMemorySearch(cmd, []string{"anything"}); err == nil {
		t.Fatal("expected missing root")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
