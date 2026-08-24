package main

import (
	"io"
	"os"
	"testing"
)

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	runErr := fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return string(out), runErr
}

func TestSkillCommandExposesReadOnlyInspectionOnly(t *testing.T) {
	want := map[string]bool{"list": true, "get": true}
	for _, command := range skillCmd.Commands() {
		if !want[command.Name()] {
			t.Errorf("unexpected Agent skill command %q", command.Name())
		}
		delete(want, command.Name())
	}
	for name := range want {
		t.Errorf("missing Agent skill command %q", name)
	}
}
