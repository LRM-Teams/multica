package service

import "testing"

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
