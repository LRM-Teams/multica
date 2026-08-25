package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunWithTerminalProgressFallsBackToPlainOutput(t *testing.T) {
	var output bytes.Buffer
	value, err := runWithTerminalProgress(&output, false, "Starting Computer", func() (int, error) {
		return 42, nil
	})
	if err != nil || value != 42 {
		t.Fatalf("progress result = %d, %v", value, err)
	}
	if got := output.String(); got != "… Starting Computer\n" || strings.Contains(got, "\033[") {
		t.Fatalf("plain progress output = %q", got)
	}
}

func TestRunWithTerminalProgressRendersAndClearsTTYSpinner(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	var output bytes.Buffer
	_, err := runWithTerminalProgress(&output, true, "Restarting Computer", func() (struct{}, error) {
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.Contains(got, "\033[36m⠋\033[0m Restarting Computer") || !strings.HasSuffix(got, "\r\033[2K") {
		t.Fatalf("interactive progress output = %q", got)
	}
}
