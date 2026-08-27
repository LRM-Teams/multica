package service

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestEffectiveGraphMemoryMode(t *testing.T) {
	tests := []struct {
		name, memoryType, workspaceMode, override, want string
	}{
		{"non graph never activates agent", "legacy", "agent", "agent", "inject"},
		{"workspace agent default", "graph", "agent", "inherit", "agent"},
		{"workspace inject default", "graph", "inject", "inherit", "inject"},
		{"channel inject override", "graph", "agent", "inject", "inject"},
		{"channel agent override", "graph", "inject", "agent", "agent"},
		{"invalid persisted default fails toward agent migration default", "graph", "", "inherit", "agent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EffectiveGraphMemoryMode(tt.memoryType, tt.workspaceMode, tt.override); got != tt.want {
				t.Fatalf("EffectiveGraphMemoryMode(%q,%q,%q)=%q want %q", tt.memoryType, tt.workspaceMode, tt.override, got, tt.want)
			}
		})
	}
}

func TestMergeGraphMemoryAgentCustomEnv(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want map[string]string
	}{
		{
			name: "empty environment gets safe system CA default",
			raw:  `{}`,
			want: map[string]string{"NODE_OPTIONS": "--use-openssl-ca"},
		},
		{
			name: "existing environment is preserved",
			raw:  `{"MEMORY_SCOPE":"channel"}`,
			want: map[string]string{
				"MEMORY_SCOPE": "channel",
				"NODE_OPTIONS": "--use-openssl-ca",
			},
		},
		{
			name: "explicit node options are preserved",
			raw:  `{"NODE_OPTIONS":"--max-old-space-size=4096","OTHER":"value"}`,
			want: map[string]string{
				"NODE_OPTIONS": "--max-old-space-size=4096",
				"OTHER":        "value",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := mergeGraphMemoryAgentCustomEnv([]byte(tt.raw))
			if err != nil {
				t.Fatalf("mergeGraphMemoryAgentCustomEnv() error = %v", err)
			}
			var got map[string]string
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("decode merged custom env: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("merged custom env = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestResolveGraphMemoryAgentRuntimeConfig(t *testing.T) {
	profileRuntime := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	channelRuntime := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}

	t.Run("channel tuple overrides workspace tuple", func(t *testing.T) {
		got := resolveGraphMemoryAgentRuntimeConfig(
			profileRuntime, channelRuntime,
			"profile/model", " channel/model ", "profile-thinking", " high ",
		)
		if got.runtimeID != channelRuntime || got.model != "channel/model" || got.thinking != "high" || got.source != "channel" {
			t.Fatalf("resolved config = %+v, want channel tuple", got)
		}
	})

	t.Run("null channel runtime inherits the complete workspace tuple", func(t *testing.T) {
		got := resolveGraphMemoryAgentRuntimeConfig(
			profileRuntime, pgtype.UUID{},
			" profile/model ", "stale/channel/model", " medium ", "stale-thinking",
		)
		if got.runtimeID != profileRuntime || got.model != "profile/model" || got.thinking != "medium" || got.source != "workspace" {
			t.Fatalf("resolved config = %+v, want workspace tuple", got)
		}
	})
}
