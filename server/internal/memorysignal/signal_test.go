package memorysignal

import "testing"

func TestLooksLikeDurableFeedback(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"记住这个：发材料前去掉第三方个人信息", true},
		{"记住到memory 里", true},
		{"记住到 memory", true},
		{"写进 memory", true},
		{"以后都要先反馈一下进度", true},
		{"别再只说好", true},
		{"下次先确认再动手", true},
		{"pipeline也有错，以后都得记住", true}, // 以后都
		{"hello", false},
		{"帮我看一下这个 bug", false},
		{"那你为什么又犯错了", false},                // agent judges in-prompt; not a rigid platform phrase
		{"先 rebase/merge dev，再建 MR", false}, // soft lesson — agent judges; platform only catches explicit remember / clear standing cues
		{"from now on always report progress first", true},
		{"remember this for later", true},
	}
	for _, tc := range cases {
		if got := LooksLikeDurableFeedback(tc.text); got != tc.want {
			t.Fatalf("%q: got %v want %v", tc.text, got, tc.want)
		}
	}
}

func TestInferTopicMRWorkflow(t *testing.T) {
	if got := InferTopic("提 MR 前先 rebase/merge dev，pipeline 绿再合并", ""); got != "mr_workflow" {
		t.Fatalf("got %q", got)
	}
	miss, ok := DetectMissedWrite("记住到memory 里\n以后先 rebase/merge dev，再建 MR", nil, nil, "member-1")
	if !ok {
		t.Fatal("expected missed write for 记住到memory")
	}
	if miss.Topic != "mr_workflow" {
		t.Fatalf("topic=%q miss=%+v", miss.Topic, miss)
	}
}

func TestNotesAndMemoryCountAsDurableWrite(t *testing.T) {
	notes := []WriteEntry{{RelPath: "notes/agents.md", ScopeType: "agent_notes", FileKey: "AGENTS"}}
	if !HasDurableWrite(notes) {
		t.Fatal("notes should count as remembered")
	}
	mem := []WriteEntry{{RelPath: "memory/MEMORY.md", ScopeType: "agent_global", FileKey: "MEMORY"}}
	if !HasDurableWrite(mem) {
		t.Fatal("MEMORY.md should count as remembered")
	}
	if _, ok := DetectMissedWrite("记住到 memory", nil, notes, "m1"); !ok {
		t.Fatal("agent notes must not clear a user-scoped missed-write")
	}
	agentSignal := []Signal{{Action: ActionWrite, Scope: "agent", Summary: "standing agent rule"}}
	if _, ok := DetectMissedWrite("记住到 memory", agentSignal, notes, "m1"); ok {
		t.Fatal("agent notes should clear an agent-scoped missed-write")
	}
}

func TestParseSignalJSON(t *testing.T) {
	sig, ok := ParseSignalJSON(`{"memory":{"action":"write","kind":"feedback","scope":"user","topic":"progress_feedback","summary":"长任务先报进度"}}`)
	if !ok {
		t.Fatal("expected parse ok")
	}
	if sig.Action != ActionWrite || sig.Topic != "progress_feedback" || sig.Summary == "" {
		t.Fatalf("unexpected signal: %+v", sig)
	}
	none, ok := ParseSignalJSON(`{"memory":{"action":"none"}}`)
	if !ok || none.Action != ActionNone {
		t.Fatalf("none: ok=%v sig=%+v", ok, none)
	}
}

func TestDetectMissedWrite(t *testing.T) {
	miss, ok := DetectMissedWrite("以后都要先反馈进度", nil, nil, "member-1")
	if !ok {
		t.Fatal("expected missed write")
	}
	if miss.Scope != "user" || miss.Topic != "progress_feedback" || miss.SubjectID != "member-1" {
		t.Fatalf("unexpected miss: %+v", miss)
	}
	if miss.DedupeKey == "" || !stringsContains(miss.DedupeKey, "progress_feedback") {
		t.Fatalf("bad dedupe key %q", miss.DedupeKey)
	}

	writes := []WriteEntry{{RelPath: "users/member-1/USER.md", ScopeType: "user", FileKey: "USER"}}
	if _, ok := DetectMissedWrite("以后都要先反馈进度", nil, writes, "member-1"); ok {
		t.Fatal("durable write should clear miss")
	}
	wrongUser := []WriteEntry{{RelPath: "users/member-2/USER.md", ScopeType: "user", FileKey: "USER"}}
	if _, ok := DetectMissedWrite("以后都要先反馈进度", nil, wrongUser, "member-1"); !ok {
		t.Fatal("a different user's file must not clear this user's missed-write")
	}

	dailyOnly := []WriteEntry{{RelPath: "memory/daily/2026-07-29.md", ScopeType: "agent_daily", FileKey: "DAILY"}}
	if _, ok := DetectMissedWrite("记住这个", nil, dailyOnly, "member-1"); !ok {
		t.Fatal("daily-only should still be a miss for explicit remember")
	}
}

func TestDetectMissedWriteFromSignal(t *testing.T) {
	signals := []Signal{{Action: ActionWrite, Kind: KindPreference, Scope: "user", Topic: "direct_critique", Summary: "发现问题直接指出"}}
	miss, ok := DetectMissedWrite("随便聊聊", signals, nil, "member-1")
	if !ok {
		t.Fatal("expected miss from signal")
	}
	if miss.Source != SourceMemorySignal || miss.Topic != "direct_critique" {
		t.Fatalf("unexpected: %+v", miss)
	}
}

func TestDetectMissedWriteFromCompactionFlush(t *testing.T) {
	signals := []Signal{{Action: ActionCompactionFlush, Kind: "missed", Summary: "context compaction ran without a durable memory write"}}
	miss, ok := DetectMissedWrite("随便聊聊", signals, nil, "member-1")
	if !ok {
		t.Fatal("expected miss from compaction_flush")
	}
	if miss.Source != ActionCompactionFlush {
		t.Fatalf("source = %q", miss.Source)
	}
}

func TestNormalizeTopicAndDedupeKey(t *testing.T) {
	if got := NormalizeTopic(" Progress Feedback "); got != "progress_feedback" {
		t.Fatalf("got %q", got)
	}
	key := DedupeKey("user", "cf7670af", "preference", "progress_feedback")
	if key != "user:cf7670af+preference+progress_feedback" {
		t.Fatalf("got %q", key)
	}
}

func stringsContains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || stringIndex(s, sub) >= 0)
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
