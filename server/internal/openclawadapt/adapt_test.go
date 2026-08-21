package openclawadapt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyWritesIsolatedOverlay(t *testing.T) {
	root := t.TempDir()
	if err := Apply(root, "member-1"); err != nil {
		t.Fatal(err)
	}
	cfg, err := os.ReadFile(filepath.Join(root, ConfigRel))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), `"enabled": false`) || !strings.Contains(string(cfg), "dreaming") {
		t.Fatalf("config = %s", cfg)
	}
	user, err := os.ReadFile(filepath.Join(root, "USER.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(user), "users/member-1/USER.md") {
		t.Fatalf("user pointer = %s", user)
	}
	env := Env(root)
	if env["OPENCLAW_STATE_DIR"] != filepath.Join(root, StateDirName) || env["OPENCLAW_WORKSPACE"] != root {
		t.Fatalf("env = %+v", env)
	}
}

func TestApplyDoesNotOverwriteExistingPointer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Apply(root, ""); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "MEMORY.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep\n" {
		t.Fatalf("overwrote pointer: %s", got)
	}
}

func TestApplyEmptyRootIsNoop(t *testing.T) {
	if err := Apply("", "x"); err != nil {
		t.Fatal(err)
	}
}
