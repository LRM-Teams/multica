package main

import (
	"strings"
	"testing"
)

func TestAutopilotCLIRetired(t *testing.T) {
	err := runAutopilotRetired(nil, nil)
	if err == nil {
		t.Fatal("expected retired error")
	}
	if !strings.Contains(err.Error(), "removed") {
		t.Fatalf("got %v, want removed message", err)
	}
}
