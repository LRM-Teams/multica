package memorysignal

import "testing"

func TestFrictionVectorIsZeroAndSummary(t *testing.T) {
	var zero FrictionVector
	if !zero.IsZero() {
		t.Fatal("empty vector should be zero")
	}
	if zero.Summary() != "" {
		t.Fatalf("zero summary should be empty, got %q", zero.Summary())
	}
	v := FrictionVector{RetryLoop: 1, HumanCorrection: 2}
	if v.IsZero() {
		t.Fatal("non-empty vector should not be zero")
	}
	if got := v.Summary(); got != "human_correction×2, retry_loop×1" {
		t.Fatalf("summary=%q", got)
	}
}

func TestFrictionTrackerRetryLoopEpisodes(t *testing.T) {
	tr := NewFrictionTracker()
	// Two identical calls stay below the threshold.
	tr.ObserveToolUse("bash", "hash-a")
	tr.ObserveToolUse("bash", "hash-a")
	if v := tr.Vector(); v.RetryLoop != 0 {
		t.Fatalf("below threshold should not count, got %+v", v)
	}
	// Third identical call opens one episode; further repeats do not re-count.
	tr.ObserveToolUse("bash", "hash-a")
	tr.ObserveToolUse("bash", "hash-a")
	if v := tr.Vector(); v.RetryLoop != 1 {
		t.Fatalf("one episode expected, got %+v", v)
	}
	// Different args reset the run; a fresh threshold opens a second episode.
	tr.ObserveToolUse("bash", "hash-b")
	tr.ObserveToolUse("bash", "hash-b")
	tr.ObserveToolUse("bash", "hash-b")
	if v := tr.Vector(); v.RetryLoop != 2 {
		t.Fatalf("two episodes expected, got %+v", v)
	}
}

func TestFrictionTrackerErrorStreak(t *testing.T) {
	tr := NewFrictionTracker()
	tr.ObserveError()
	tr.ObserveError()
	if v := tr.Vector(); v.SelfErrorStreak != 0 {
		t.Fatalf("below threshold should not count, got %+v", v)
	}
	tr.ObserveError()
	tr.ObserveError()
	if v := tr.Vector(); v.SelfErrorStreak != 1 {
		t.Fatalf("one streak expected, got %+v", v)
	}
	// Progress (text/thinking) breaks the streak; a new run must re-reach the threshold.
	tr.ObserveProgress()
	tr.ObserveError()
	tr.ObserveError()
	if v := tr.Vector(); v.SelfErrorStreak != 1 {
		t.Fatalf("broken streak should not re-count early, got %+v", v)
	}
	tr.ObserveError()
	if v := tr.Vector(); v.SelfErrorStreak != 2 {
		t.Fatalf("second streak expected, got %+v", v)
	}
}

func TestLooksLikeDecisionFinal(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"行，就用 B 方案", true},
		{"那就按方案二来", true},
		{"定了，走 PostgreSQL", true},
		{"我们决定采用行级锁", true},
		{"以后一律先跑 lint", true},
		{"统一改成驼峰命名", true},
		{"这个事就这么敲定了", true},
		{"let's go with option B", true},
		{"we'll use the queue-based approach", true},
		{"decided to keep the legacy path", true},
		{"我还没决定用哪个", false},
		{"你觉得应该用哪个方案？", false},
		{"帮我看一下这个 bug", false},
		{"hello", false},
	}
	for _, tc := range cases {
		if got := LooksLikeDecisionFinal(tc.text); got != tc.want {
			t.Fatalf("%q: got %v want %v", tc.text, got, tc.want)
		}
	}
}

func TestDetectMissedDecision(t *testing.T) {
	// Decision phrasing with no durable write queues a decision_guard candidate.
	miss, ok := DetectMissedDecision("行，就用 B 方案", nil, nil)
	if !ok {
		t.Fatal("expected missed decision")
	}
	if miss.Source != SourceDecisionGuard {
		t.Fatalf("source=%q", miss.Source)
	}
	if miss.Scope != "project" {
		t.Fatalf("default scope should be project, got %q", miss.Scope)
	}

	// A project DECISIONS write satisfies the guard.
	writes := []WriteEntry{{RelPath: "projects/p1/DECISIONS.md", ScopeType: "project", FileKey: "DECISIONS"}}
	if _, ok := DetectMissedDecision("行，就用 B 方案", nil, writes); ok {
		t.Fatal("project DECISIONS write should clear the decision guard")
	}

	// A channel CONTEXT write also satisfies it.
	channel := []WriteEntry{{RelPath: "channels/c1/CONTEXT.md", ScopeType: "channel", FileKey: "CONTEXT"}}
	if _, ok := DetectMissedDecision("定了，走 PostgreSQL", nil, channel); ok {
		t.Fatal("channel CONTEXT write should clear the decision guard")
	}

	// A user-preference write must NOT satisfy a decision.
	user := []WriteEntry{{RelPath: "users/m1/USER.md", ScopeType: "user", FileKey: "USER"}}
	if _, ok := DetectMissedDecision("定了，走 PostgreSQL", nil, user); !ok {
		t.Fatal("user-scope write must not clear the decision guard")
	}

	// An explicit decision signal triggers even without phrasing.
	sig := []Signal{{Action: ActionDecision, Scope: "project", Topic: "storage-engine", Summary: "评估三方案后定 B"}}
	miss, ok = DetectMissedDecision("好的收到", sig, nil)
	if !ok {
		t.Fatal("decision signal should trigger the guard")
	}
	if miss.Topic != "storage_engine" {
		t.Fatalf("topic=%q", miss.Topic)
	}

	// No phrasing and no signal: no candidate.
	if _, ok := DetectMissedDecision("帮我看一下这个 bug", nil, nil); ok {
		t.Fatal("plain request must not trigger the decision guard")
	}
}

func TestDetectFrictionMiss(t *testing.T) {
	friction := FrictionVector{RetryLoop: 2, SelfErrorStreak: 1}

	// Non-zero friction with no durable write queues a friction_guard candidate.
	miss, ok := DetectFrictionMiss(friction, nil, nil)
	if !ok {
		t.Fatal("expected friction miss")
	}
	if miss.Source != SourceFrictionGuard {
		t.Fatalf("source=%q", miss.Source)
	}
	if miss.Scope != "agent" {
		t.Fatalf("default scope should be agent, got %q", miss.Scope)
	}

	// Zero friction never queues.
	if _, ok := DetectFrictionMiss(FrictionVector{}, nil, nil); ok {
		t.Fatal("zero friction must not trigger the guard")
	}

	// Any durable lesson write satisfies the guard.
	writes := []WriteEntry{{RelPath: "memory/MEMORY.md", ScopeType: "agent_global", FileKey: "MEMORY"}}
	if _, ok := DetectFrictionMiss(friction, nil, writes); ok {
		t.Fatal("durable write should clear the friction guard")
	}

	// Daily-only writes do not satisfy it.
	daily := []WriteEntry{{RelPath: "memory/daily/2026-08-20.md", ScopeType: "agent_daily", FileKey: "DAILY"}}
	if _, ok := DetectFrictionMiss(friction, nil, daily); !ok {
		t.Fatal("daily-only write must not clear the friction guard")
	}

	// Agent-declared infra friction suppresses the guard (spec §3.1).
	infra := []Signal{{Action: ActionFriction, Kind: "infra", Summary: "sandbox disk full"}}
	if _, ok := DetectFrictionMiss(friction, infra, nil); ok {
		t.Fatal("infra friction must not queue a method-lesson candidate")
	}

	// A method friction signal enriches topic and summary.
	method := []Signal{{Action: ActionFriction, Kind: "method", Scope: "project", Topic: "fixture-conflict", Summary: "共享 fixture 冲突需先 make db-reset"}}
	miss, ok = DetectFrictionMiss(friction, method, nil)
	if !ok {
		t.Fatal("method friction should trigger the guard")
	}
	if miss.Scope != "project" || miss.Topic != "fixture_conflict" {
		t.Fatalf("miss=%+v", miss)
	}
}

func TestLooksLikeCorrection(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"不对，先停下", true},
		{"这里不对，你搞错了方向", true},
		{"不是这样，换个思路", true},
		{"重来一遍，别用那个库", true},
		{"改回去，撤销刚才的修改", true},
		{"that's wrong, start over", true},
		{"not what I asked for", true},
		{"你看这样对不对？", false},
		{"帮我看一下这个 bug", false},
		{"继续", false},
	}
	for _, tc := range cases {
		if got := LooksLikeCorrection(tc.text); got != tc.want {
			t.Fatalf("%q: got %v want %v", tc.text, got, tc.want)
		}
	}
}

func TestAugmentFrictionFromTrigger(t *testing.T) {
	base := FrictionVector{RetryLoop: 1}
	augmented := AugmentFrictionFromTrigger(base, "不对，先停下")
	if augmented.HumanCorrection != 1 || augmented.RetryLoop != 1 {
		t.Fatalf("augmented=%+v", augmented)
	}
	// A correcting trigger alone makes a zero vector non-zero.
	if AugmentFrictionFromTrigger(FrictionVector{}, "不是这样，换个思路").IsZero() {
		t.Fatal("correction must produce non-zero friction")
	}
	// Plain triggers change nothing.
	if got := AugmentFrictionFromTrigger(base, "帮我看一下这个 bug"); got != base {
		t.Fatalf("plain trigger must not change the vector, got %+v", got)
	}
}

func TestShouldReportEvenWithoutWritesDecisionAndFriction(t *testing.T) {
	if !ShouldReportEvenWithoutWrites("行，就用 B 方案", nil) {
		t.Fatal("decision phrasing should force an empty-write report")
	}
	sig := []Signal{{Action: ActionDecision, Summary: "定 B"}}
	if !ShouldReportEvenWithoutWrites("好的", sig) {
		t.Fatal("decision signal should force an empty-write report")
	}
	frictionSig := []Signal{{Action: ActionFriction, Kind: "method", Summary: "fixture 冲突"}}
	if !ShouldReportEvenWithoutWrites("好的", frictionSig) {
		t.Fatal("friction signal should force an empty-write report")
	}
}
