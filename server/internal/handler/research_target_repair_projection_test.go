package handler

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/researchrun"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestProjectResearchTargetRepairPreservesClassAndAction(t *testing.T) {
	nodeType, title, summary, status := projectResearchEvent(
		researchrun.RunEvent{Type: "target_repair_decided"},
		db.ResearchSession{},
		map[string]any{
			"failure_class": "credential",
			"repair_kind":   "request_configuration",
			"repair_key":    "research-repair:session:task:1:1:fingerprint:request_configuration",
		},
	)
	if nodeType != "agent_activity" || title != "已确定执行失败修复动作" ||
		summary != "credential · request_configuration" || status != "done" {
		t.Fatalf("projected target repair=(%q, %q, %q, %q)", nodeType, title, summary, status)
	}
}
