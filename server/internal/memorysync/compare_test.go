package memorysync

import "testing"

func TestIdentityKeyAndTopic(t *testing.T) {
	key := IdentityKey("user", "m1", KindPreference, "progress_feedback", "长任务先报进度")
	if key != "user:m1+preference+progress_feedback" {
		t.Fatalf("key=%q", key)
	}
	if got := InferTopic("以后都要先反馈进度"); got != "progress_feedback" {
		t.Fatalf("topic=%q", got)
	}
}

func TestCompareSameMoreSpecificOpposed(t *testing.T) {
	if d := Compare("长任务先报进度", "长任务先报进度"); d.Decision != DecisionSame {
		t.Fatalf("same: %+v", d)
	}
	if d := Compare("先报进度", "长任务开始前先报进度，并持续汇报"); d.Decision != DecisionMoreSpecific {
		t.Fatalf("more specific: %+v", d)
	}
	if d := Compare("紧急也要先报进度", "紧急时别报进度，直接干"); d.Decision != DecisionOpposed {
		t.Fatalf("opposed: %+v", d)
	}
	if d := Compare("必须先确认再动手", "不要先确认，直接干"); d.Decision != DecisionOpposed {
		t.Fatalf("negation opposed: %+v", d)
	}
}

func TestScopeFromRelPathAndEntries(t *testing.T) {
	scope, subject, kind := ScopeFromRelPath("users/abc/USER.md")
	if scope != "user" || subject != "abc" || kind != KindPreference {
		t.Fatalf("%s %s %s", scope, subject, kind)
	}
	entries := EntriesFromFile("users/abc/USER.md", "# User Preferences\n\n- 希望发现问题直接指出\n- 长任务先反馈进度\n")
	if len(entries) != 2 {
		t.Fatalf("entries=%+v", entries)
	}
	if entries[0].Scope != "user" || entries[0].SubjectID != "abc" {
		t.Fatalf("entry0=%+v", entries[0])
	}
}

func TestIsDurableRelPath(t *testing.T) {
	if !IsDurableRelPath("memory/MEMORY.md") {
		t.Fatal("MEMORY should be durable")
	}
	if IsDurableRelPath("memory/daily/2026-07-29.md") {
		t.Fatal("daily should not be durable center sync")
	}
	if IsDurableRelPath("memory/REVIEW.md") {
		t.Fatal("REVIEW should not be durable center sync")
	}
}

func TestPortabilityReason(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "portable preference", content: "长任务开始前先反馈进度", want: ""},
		{name: "portable repository URL", content: "Repository is https://github.com/LRM-Teams/multica", want: ""},
		{name: "linux home path", content: "Use /home/alice/work/multica on this machine", want: "absolute_local_path"},
		{name: "mac home path", content: "Checkout lives at /Users/alice/src/multica", want: "absolute_local_path"},
		{name: "windows path", content: `Checkout lives at C:\\Users\\alice\\src`, want: "absolute_local_path"},
		{name: "loopback", content: "Backend is http://localhost:8080", want: "loopback_endpoint"},
		{name: "credential", content: "api_key=sk-example-secret-value", want: "credential_like"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PortabilityReason(tt.content); got != tt.want {
				t.Fatalf("PortabilityReason() = %q, want %q", got, tt.want)
			}
		})
	}
}
