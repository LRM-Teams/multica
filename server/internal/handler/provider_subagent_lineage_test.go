package handler

import "testing"

func TestParseProviderLineageSubagentType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		lineage string
		want    string
		ok      bool
	}{
		{name: "empty", lineage: "", ok: false},
		{name: "explore", lineage: `{"parent_tool_use_id":"toolu_parent","subagent_type":"Explore"}`, want: "Explore", ok: true},
		{name: "parent only", lineage: `{"parent_tool_use_id":"toolu_parent"}`, want: "", ok: true},
		{name: "empty object", lineage: `{}`, ok: false},
		{name: "non json still nested", lineage: "subagent:review", want: "", ok: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseProviderLineageSubagentType(tc.lineage)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if got != tc.want {
				t.Fatalf("type = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProviderSubagentStartedMessage(t *testing.T) {
	t.Parallel()
	if got := providerSubagentStartedMessage("Explore"); got != "Subagent started: Explore" {
		t.Fatalf("got %q", got)
	}
	if got := providerSubagentStartedMessage(""); got != "Subagent started" {
		t.Fatalf("got %q", got)
	}
	if got := providerSubagentStartedMessage("  "); got != "Subagent started" {
		t.Fatalf("got %q", got)
	}
}
