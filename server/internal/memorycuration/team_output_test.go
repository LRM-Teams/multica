package memorycuration

import "testing"

func TestCountTeamCurationOutputUsesJSONNotSubstring(t *testing.T) {
	jsonOut := `{"team_knowledge":[{"kind":"memory","title":"A","content":"a"},{"kind":"pattern","title":"B","content":"b"}],"decisions":[],"conflicts":[{"title":"c1","content":"x"},{"title":"c2","content":"y"}]}`
	tk, conflicts := CountTeamCurationOutput(jsonOut)
	if tk != 2 || conflicts != 2 {
		t.Fatalf("got team=%d conflicts=%d, want 2/2", tk, conflicts)
	}

	// Free-form prose that mentions "team" many times must not inflate counts.
	prose := "已完成 team curation。team knowledge 写入 sync_queue，无新 team 晋升。连续 team 级联。"
	tk, conflicts = CountTeamCurationOutput(prose)
	if tk != 0 || conflicts != 0 {
		t.Fatalf("prose counts = team=%d conflicts=%d, want 0/0", tk, conflicts)
	}

	// Embedded JSON after prose still counts.
	mixed := "正在生成 2026-07-12 团队整理结果。\n" + jsonOut
	tk, conflicts = CountTeamCurationOutput(mixed)
	if tk != 2 || conflicts != 2 {
		t.Fatalf("mixed counts = team=%d conflicts=%d, want 2/2", tk, conflicts)
	}
}
