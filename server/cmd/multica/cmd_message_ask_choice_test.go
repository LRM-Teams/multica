package main

import (
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestParseChoiceOptionFlags(t *testing.T) {
	opts, err := parseChoiceOptionFlags([]string{
		"id=yes,label=是",
		"id=no,label=否,description=先别动",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(opts) != 2 {
		t.Fatalf("len=%d", len(opts))
	}
	if opts[0] != (protocol.ChoiceOption{ID: "yes", Label: "是"}) {
		t.Fatalf("opt0=%+v", opts[0])
	}
	if opts[1].ID != "no" || opts[1].Label != "否" || opts[1].Description != "先别动" {
		t.Fatalf("opt1=%+v", opts[1])
	}
}

func TestParseChoiceOptionFlagsRejectsMissingLabel(t *testing.T) {
	_, err := parseChoiceOptionFlags([]string{"id=yes"})
	if err == nil {
		t.Fatal("expected error")
	}
}
