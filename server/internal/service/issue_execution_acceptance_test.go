package service

import "testing"

func TestIssueExecutionHasAcceptanceCriteria(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "one criterion", raw: `["verified"]`, want: true},
		{name: "trimmed criterion", raw: `["  verified  "]`, want: true},
		{name: "empty list", raw: `[]`, want: false},
		{name: "blank criterion", raw: `[" "]`, want: false},
		{name: "invalid json", raw: `{}`, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := issueExecutionHasAcceptanceCriteria([]byte(test.raw)); got != test.want {
				t.Fatalf("issueExecutionHasAcceptanceCriteria(%s)=%v, want %v", test.raw, got, test.want)
			}
		})
	}
}
