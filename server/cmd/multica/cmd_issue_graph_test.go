package main

import "testing"

func TestIssueGraphCommandsRequireDurableInputFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		flag string
	}{{"create", "plan-file"}, {"create", "idempotency-key"}, {"revise", "input-file"}, {"artifact", "input-file"}, {"verification", "input-file"}, {"invalidate", "reason"}} {
		var commandFound bool
		for _, command := range issueGraphCmd.Commands() {
			if command.Name() == tc.name {
				commandFound = true
				if command.Flag(tc.flag) == nil {
					t.Errorf("%s missing --%s", tc.name, tc.flag)
				}
			}
		}
		if !commandFound {
			t.Errorf("missing graph command %s", tc.name)
		}
	}
}
