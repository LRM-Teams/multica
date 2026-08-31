package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestConsumeInboxResponseOutputsBeforeCallerACK(t *testing.T) {
	var output bytes.Buffer
	var result map[string]any
	var events []string
	err := consumeInboxResponse(context.Background(), strings.NewReader(`{"messages":[],"status":"complete","revision":7}`), &output, &result, func(w io.Writer) error {
		events = append(events, "output")
		return json.NewEncoder(w).Encode(result)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"revision":7`)) || len(events) != 1 {
		t.Fatalf("output=%q events=%v", output.String(), events)
	}
}

func TestConsumeInboxResponseRejectsMalformedJSONBeforeOutput(t *testing.T) {
	called := false
	err := consumeInboxResponse(context.Background(), strings.NewReader(`{"revision":`), io.Discard, &map[string]any{}, func(io.Writer) error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatalf("err=%v outputCalled=%v", err, called)
	}
}
