package handler

import (
	"strings"
	"testing"
)

func init() {
	periodBriefProgressSilenceBound = 0
}

func TestFormatPeriodBriefProgressBoardIsAShortUntrustedBoard(t *testing.T) {
	got := formatPeriodBriefProgressBoard(periodBriefProgressBoard{
		Event:     "pack_received",
		RunStatus: "collecting",
		Settled:   []periodBriefProgressRow{{Name: "Laptop A", Status: "ready"}},
		Waiting:   []periodBriefProgressRow{{Name: "Ubuntu", Status: "pending"}},
		Retryable: false,
	})
	for _, want := range []string{
		"<period_brief_progress>",
		"</period_brief_progress>",
		"event: pack_received",
		"run_status: collecting",
		"name: Laptop A status: ready",
		"name: Ubuntu status: pending",
		"retryable: no",
		"Write one short reminder",
		"Do not start synthesis",
		"Do not emit chat XML",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("progress board missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<period_brief_compose") || strings.Contains(got, "<period_brief_start") {
		t.Fatalf("progress board still teaches dispatch fences:\n%s", got)
	}
	if strings.Contains(got, "# 采集包") || strings.Contains(got, "刚刚收到了") {
		t.Fatalf("progress board dumped pack body or canned copy:\n%s", got)
	}
}

func TestFormatPeriodBriefProgressBoardRunStartedRestatesPlan(t *testing.T) {
	got := formatPeriodBriefProgressBoard(periodBriefProgressBoard{
		Event:     "run_started",
		RunStatus: "collecting",
		Window:    "本周",
		Focus:     "",
		Waiting:   []periodBriefProgressRow{{Name: "Laptop A", Status: "pending"}},
	})
	for _, want := range []string{
		"event: run_started",
		"window: 本周",
		"focus: unconstrained",
		"collection already started",
		"ask them to wait",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("run_started board missing %q:\n%s", want, got)
		}
	}
}

func TestPeriodBriefProgressBoardFromRunUsesPackMarkdownAsReady(t *testing.T) {
	board := periodBriefProgressBoardFromRun(
		notePeriodBriefRunRow{
			Status: "collecting",
			Collectors: []notePeriodBriefCollectorRef{
				{AgentID: "a", PackMarkdown: "# pack"},
				{AgentID: "b"},
			},
		},
		map[string]string{"a": "Laptop A", "b": "Ubuntu"},
		"pack_received",
		nil,
	)
	if board.Event != "pack_received" || len(board.Settled) != 1 || board.Settled[0].Name != "Laptop A" {
		t.Fatalf("settled = %+v", board.Settled)
	}
	if len(board.Waiting) != 1 || board.Waiting[0].Name != "Ubuntu" {
		t.Fatalf("waiting = %+v", board.Waiting)
	}
}
