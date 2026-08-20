// SPDX-License-Identifier: Apache-2.0

package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

func TestGraphMemoryRecallInjectionContentBoundsCitations(t *testing.T) {
	citations := make([]memorygraph.Citation, graphMemoryRecallMaxCitationCount+1)
	for i := range citations {
		citations[i] = memorygraph.Citation{NodeID: fmt.Sprintf("n%d", i), Level: i, Epistemic: "observed"}
	}
	content := graphMemoryRecallInjectionContent(strings.Repeat("x", graphMemoryRecallMaxSummaryChars+1), citations)
	if !strings.Contains(content, graphMemoryRecallTruncationMarker) {
		t.Fatalf("content missing summary truncation marker: %q", content)
	}
	if strings.Count(content, "\n- n") != graphMemoryRecallMaxCitationCount || !strings.Contains(content, "\n- …and 1 more") {
		t.Fatalf("content did not cap citations: %q", content)
	}
}
