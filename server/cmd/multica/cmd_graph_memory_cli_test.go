package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func graphMemoryCLICommandForTest() *cobra.Command {
	// init() already attached flags to the package-level commands; rebuild the
	// argument surface the way cobra would parse it.
	return graphMemoryCmd
}

func TestGraphMemoryCLIRequiresAgentProxyEnv(t *testing.T) {
	start, _, err := graphMemoryCmd.Find([]string{"start"})
	if err != nil {
		t.Fatal(err)
	}
	if err := start.ParseFlags([]string{
		"--channel", "11111111-1111-1111-1111-111111111111",
		"--query", "hello", "--idempotency-key", "msg-1-start",
	}); err != nil {
		t.Fatal(err)
	}
	err = start.RunE(start, nil)
	if err == nil {
		t.Fatal("expected error outside an agent launch environment")
	}
	if !strings.Contains(err.Error(), "Agent Proxy") {
		t.Fatalf("error should name the Agent Proxy requirement: %v", err)
	}
}

func TestGraphMemoryCLINodeIDsAndRefParsing(t *testing.T) {
	cmd := graphMemoryCLICommandForTest()
	start, _, err := cmd.Find([]string{"explore", "--node-ids", " a , b ,, ", "--ref", `{"kind":"memory"}`})
	if err != nil {
		t.Fatal(err)
	}
	if err := start.ParseFlags([]string{"--node-ids", " a , b ,, ", "--ref", `{"kind":"memory"}`}); err != nil {
		t.Fatal(err)
	}
	ids := graphMemoryCLINodeIDs(start)
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("node ids = %v", ids)
	}
	ref := graphMemoryCLIRef(start)
	if string(ref) != `{"kind":"memory"}` {
		t.Fatalf("ref = %s", ref)
	}
	if err := start.ParseFlags([]string{"--ref", "not-json"}); err != nil {
		t.Fatal(err)
	}
	if graphMemoryCLIRef(start) != nil {
		t.Fatal("invalid JSON ref must be rejected")
	}
}
