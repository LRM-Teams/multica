package secretscoped

import (
	"testing"
)

func TestFilterKeepsChannelSecretsOnMatchingChannelOnly(t *testing.T) {
	secrets := []Secret{
		{Key: "CHANNEL_A_TOKEN", Value: "a", Scope: ScopeChannel, ChannelID: "chan-a"},
		{Key: "CHANNEL_B_TOKEN", Value: "b", Scope: ScopeChannel, ChannelID: "chan-b"},
		{Key: "AGENT_KEY", Value: "agent", Scope: ScopeAgent},
	}
	got := Filter(secrets, TaskScope{ChannelID: "chan-a"})
	if got["CHANNEL_A_TOKEN"] != "a" {
		t.Fatalf("chan-a secret missing: %#v", got)
	}
	if _, ok := got["CHANNEL_B_TOKEN"]; ok {
		t.Fatalf("chan-b secret leaked into chan-a: %#v", got)
	}
	if got["AGENT_KEY"] != "agent" {
		t.Fatalf("agent secret dropped: %#v", got)
	}
}

func TestFilterRequiresBoundProjectForProjectSecrets(t *testing.T) {
	secrets := []Secret{
		{Key: "PROJ_A", Value: "1", Scope: ScopeProject, ProjectID: "proj-a"},
	}
	if got := Filter(secrets, TaskScope{}); len(got) != 0 {
		t.Fatalf("unbound project leaked secrets: %#v", got)
	}
	if got := Filter(secrets, TaskScope{ProjectID: "proj-b"}); len(got) != 0 {
		t.Fatalf("other project leaked secrets: %#v", got)
	}
	got := Filter(secrets, TaskScope{ProjectID: "proj-a"})
	if got["PROJ_A"] != "1" {
		t.Fatalf("matching project secret missing: %#v", got)
	}
}

func TestFilterUnknownScopeFailsClosed(t *testing.T) {
	got := Filter([]Secret{{Key: "X", Value: "1", Scope: "org"}}, TaskScope{})
	if len(got) != 0 {
		t.Fatalf("unknown scope must fail closed: %#v", got)
	}
}
