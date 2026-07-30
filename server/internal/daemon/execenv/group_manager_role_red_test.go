package execenv

import (
	"strings"
	"testing"
)

// TestStartupBriefNeverRendersGroupManagerDutySegment guards the retirement
// of the create-time group-manager brief: manager duties are now injected
// per-wake by daemon.currentStateOverlay (server-claimed, refreshed every
// turn) instead of baked into the startup AGENTS materialization, so a
// resumed session can no longer retain a stale duty segment from before a
// promotion, demotion, or channel rename. TaskContextForEnv has no
// ManagerChannels field to feed this any longer; this test documents the
// contract so it cannot silently come back.
func TestStartupBriefNeverRendersGroupManagerDutySegment(t *testing.T) {
	out := buildMetaSkillContent("codex", TaskContextForEnv{AgentName: "any-agent"})
	if strings.Contains(out, "Group manager") {
		t.Fatalf("startup brief must not contain group-manager duty text\n---\n%s", out)
	}
}
