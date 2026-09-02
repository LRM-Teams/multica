// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/researchrun"
)

// Phase 2 slice P2.2 (unification spec §5): the handler wires the background
// knowledge provider into the research store so every Director cycle recalls
// memory-graph background. The probe fails if handler construction drops the
// wiring.
func TestResearchRunEngineWiredWithBackgroundKnowledge(t *testing.T) {
	if testHandler == nil || testHandler.ResearchRun == nil {
		t.Skip("handler suite database not available")
	}
	probe, ok := testHandler.ResearchRun.(researchrun.BackgroundKnowledgeProbe)
	if !ok {
		t.Fatal("research run engine does not expose the background knowledge probe")
	}
	if !probe.BackgroundKnowledgeConfigured() {
		t.Fatal("research run engine lacks the background knowledge provider; handler wiring is missing")
	}
}
