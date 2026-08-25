package service

import "testing"

func TestVisibleV6ResearchReplyRejectsEnglishOperationalNarration(t *testing.T) {
	for _, body := range []string{
		"The CLI predates V6 commands. Switching to the daemon credential proxy.",
		"处理中。The CLI predates V6 commands. Switching to the daemon credential proxy.",
	} {
		if got := visibleV6ResearchReply(body, false); got != v6ResearchReplyCompleted {
			t.Fatalf("visible reply = %q, want Chinese completion summary", got)
		}
	}
}

func TestVisibleV6ResearchReplyKeepsChineseSummary(t *testing.T) {
	body := "已完成三条独立方向的派发，下一轮将汇总来源证据。"
	if got := visibleV6ResearchReply(body, false); got != body {
		t.Fatalf("visible reply = %q, want original Chinese summary", got)
	}
}

func TestVisibleV6ResearchReplyUsesChineseStopSummary(t *testing.T) {
	if got := visibleV6ResearchReply("partial English transcript", true); got != v6ResearchReplyStopped {
		t.Fatalf("visible reply = %q, want Chinese stop summary", got)
	}
}
