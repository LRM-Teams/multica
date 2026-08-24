package daemon

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

func TestEffectiveMemoryTypePrecedence(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		taskScoped string
		want       string
	}{
		{"task graph overrides env legacy", MemoryTypeLegacy, MemoryTypeGraph, MemoryTypeGraph},
		{"task legacy overrides env graph", MemoryTypeGraph, MemoryTypeLegacy, MemoryTypeLegacy},
		{"empty task falls back to env graph", MemoryTypeGraph, "", MemoryTypeGraph},
		{"empty task falls back to env legacy", MemoryTypeLegacy, "", MemoryTypeLegacy},
		{"unrecognized task value falls back to env", MemoryTypeGraph, "bogus", MemoryTypeGraph},
		{"task value is case/space tolerant", MemoryTypeLegacy, " Graph ", MemoryTypeGraph},
		{"unrecognized env and no task value defaults legacy", "bogus", "", MemoryTypeLegacy},
		{"unrecognized both defaults legacy", "bogus", "bogus", MemoryTypeLegacy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveMemoryType(tc.configured, tc.taskScoped); got != tc.want {
				t.Fatalf("effectiveMemoryType(%q, %q) = %q, want %q", tc.configured, tc.taskScoped, got, tc.want)
			}
		})
	}
}

func TestEffectiveGraphProfilePrecedence(t *testing.T) {
	cfg := Config{MemoryType: MemoryTypeLegacy, GraphExploreAgents: 4, GraphExploreMaxRounds: 3}
	task := Task{MemoryType: MemoryTypeGraph, ExploreAgents: 8, ExploreMaxRounds: 6}
	p := effectiveGraphProfile(cfg, task)
	if p.memoryType != MemoryTypeGraph || p.exploreAgents != 8 || p.exploreMaxRounds != 6 {
		t.Fatalf("task-scoped profile must win: %+v", p)
	}
	p = effectiveGraphProfile(cfg, Task{})
	if p.memoryType != MemoryTypeLegacy || p.exploreAgents != 4 || p.exploreMaxRounds != 3 {
		t.Fatalf("env defaults must apply: %+v", p)
	}
}

func TestWorkspaceGraphProfileCacheRoundTrip(t *testing.T) {
	d := &WorkspaceDaemonCore{}
	d.rememberGraphProfile(testWSID, MemoryTypeGraph, 9, 5)
	p, ok := d.graphProfileForWorkspace(testWSID)
	if !ok || p.memoryType != MemoryTypeGraph || p.exploreAgents != 9 || p.exploreMaxRounds != 5 {
		t.Fatalf("cached profile = %+v, ok=%v", p, ok)
	}
	d.rememberGraphProfile(testWSID, "", 0, 0)
	p, ok = d.graphProfileForWorkspace(testWSID)
	if !ok || p.memoryType != MemoryTypeGraph {
		t.Fatalf("empty delivery must not clobber cache: %+v, ok=%v", p, ok)
	}
	if _, ok := d.graphProfileForWorkspace("00000000-0000-0000-0000-000000000000"); ok {
		t.Fatal("unknown workspace must miss the cache")
	}
}

// A task-scoped legacy profile skips the graph recall endpoint even when the
// daemon environment selects graph memory.
func TestGraphExecutionMemoriesTaskOverrideBeatsEnv(t *testing.T) {
	d := &WorkspaceDaemonCore{
		cfg:    Config{MemoryType: MemoryTypeGraph},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if out := d.graphExecutionMemories(context.Background(), Task{MemoryType: MemoryTypeLegacy, ChatMessage: "hello"}, d.logger); out != nil {
		t.Fatalf("graphExecutionMemories = %v, want nil under task-scoped legacy override", out)
	}
}

func TestGraphExecutionMemoriesEnvLegacySkipsRecall(t *testing.T) {
	d := &WorkspaceDaemonCore{
		cfg:    Config{MemoryType: MemoryTypeLegacy},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if out := d.graphExecutionMemories(context.Background(), Task{ChatMessage: "hello"}, d.logger); out != nil {
		t.Fatalf("graphExecutionMemories = %v, want nil under env legacy", out)
	}
}
