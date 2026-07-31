package handler

import "testing"

func TestShouldAwardIssueUpdate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		actorType string
		changed   bool
		want      bool
	}{
		{name: "member advances issue", actorType: "member", changed: true, want: true},
		{name: "member no-op update", actorType: "member", changed: false, want: false},
		{name: "agent update", actorType: "agent", changed: true, want: false},
		{name: "system update", actorType: "system", changed: true, want: false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldAwardIssueUpdate(test.actorType, test.changed); got != test.want {
				t.Fatalf(
					"shouldAwardIssueUpdate(%q, %t) = %t, want %t",
					test.actorType,
					test.changed,
					got,
					test.want,
				)
			}
		})
	}
}
