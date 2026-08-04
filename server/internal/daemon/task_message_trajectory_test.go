package daemon

import (
	"strings"
	"testing"
	"time"
)

func TestTaskMessageTrajectoryBufferCoalescesUntilQuiet(t *testing.T) {
	var got []TaskMessageData
	emit := func(kind, text, lineage string) {
		got = append(got, TaskMessageData{Type: kind, Content: text, Lineage: lineage})
	}

	var buffer taskMessageTrajectoryBuffer
	start := time.Unix(100, 0)
	buffer.append("thinking", "Frank ", "main", start, emit)
	buffer.append("thinking", "said hi.", "main", start.Add(100*time.Millisecond), emit)
	buffer.flush(start.Add(300*time.Millisecond), false, emit)
	if len(got) != 0 {
		t.Fatalf("flush before quiet window emitted %+v", got)
	}

	buffer.flush(start.Add(451*time.Millisecond), false, emit)
	if len(got) != 1 {
		t.Fatalf("expected one coalesced row, got %+v", got)
	}
	if got[0].Type != "thinking" || got[0].Content != "Frank said hi." {
		t.Fatalf("unexpected row: %+v", got[0])
	}
}

func TestTaskMessageTrajectoryBufferFlushesOnKindSwitch(t *testing.T) {
	var got []TaskMessageData
	emit := func(kind, text, lineage string) {
		got = append(got, TaskMessageData{Type: kind, Content: text, Lineage: lineage})
	}

	var buffer taskMessageTrajectoryBuffer
	start := time.Unix(100, 0)
	buffer.append("thinking", "thinking", "main", start, emit)
	buffer.append("text", "answer", "main", start.Add(10*time.Millisecond), emit)
	buffer.flush(start.Add(20*time.Millisecond), true, emit)

	if len(got) != 2 {
		t.Fatalf("expected thinking then text rows, got %+v", got)
	}
	if got[0].Type != "thinking" || got[0].Content != "thinking" {
		t.Fatalf("unexpected first row: %+v", got[0])
	}
	if got[1].Type != "text" || got[1].Content != "answer" {
		t.Fatalf("unexpected second row: %+v", got[1])
	}
}

func TestTaskMessageTrajectoryBufferTruncatesLikeRaft(t *testing.T) {
	var got []TaskMessageData
	emit := func(kind, text, lineage string) {
		got = append(got, TaskMessageData{Type: kind, Content: text, Lineage: lineage})
	}

	var buffer taskMessageTrajectoryBuffer
	buffer.append("thinking", strings.Repeat("你", taskMessageTrajectoryMaxChars+5), "main", time.Unix(100, 0), emit)
	buffer.flush(time.Unix(101, 0), true, emit)

	if len(got) != 1 {
		t.Fatalf("expected one row, got %+v", got)
	}
	if count := len([]rune(got[0].Content)); count != taskMessageTrajectoryMaxChars+1 {
		t.Fatalf("truncated rune count = %d, want %d", count, taskMessageTrajectoryMaxChars+1)
	}
	if !strings.HasSuffix(got[0].Content, "…") {
		t.Fatalf("truncated content missing ellipsis suffix")
	}
}

func TestTaskMessageTrajectoryBufferFlushesOnLineageSwitch(t *testing.T) {
	var got []TaskMessageData
	emit := func(kind, text, lineage string) {
		got = append(got, TaskMessageData{Type: kind, Content: text, Lineage: lineage})
	}

	var buffer taskMessageTrajectoryBuffer
	start := time.Unix(100, 0)
	buffer.append("thinking", "main plan", "main", start, emit)
	buffer.append("thinking", "child plan", "subagent:review", start.Add(10*time.Millisecond), emit)
	buffer.flush(start.Add(20*time.Millisecond), true, emit)

	if len(got) != 2 {
		t.Fatalf("expected lineage boundary to flush, got %+v", got)
	}
	if got[0].Content != "main plan" || got[0].Lineage != "main" {
		t.Fatalf("unexpected first lineage row: %+v", got[0])
	}
	if got[1].Content != "child plan" || got[1].Lineage != "subagent:review" {
		t.Fatalf("unexpected second lineage row: %+v", got[1])
	}
}
