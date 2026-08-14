package main

import "testing"

func TestResearchSessionGetAttemptIDFlagIsRegistered(t *testing.T) {
	flag := researchSessionGetCmd.Flags().Lookup("attempt-id")
	if flag == nil {
		t.Fatal("research session get must expose --attempt-id for frozen task context")
	}
	if flag.DefValue != "" {
		t.Fatalf("attempt-id default=%q want empty bounded-overview mode", flag.DefValue)
	}
}
