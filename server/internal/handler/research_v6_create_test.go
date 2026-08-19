package handler

import "testing"

func TestResearchV6UserCreateDoesNotRequireBootstrapFlag(t *testing.T) {
	if !researchV6UserCreateEnabled(Config{ResearchV6BootstrapEnabled: false}) {
		t.Fatal("users must be able to create V6 runs without RESEARCH_V6_BOOTSTRAP_ENABLED")
	}
}
