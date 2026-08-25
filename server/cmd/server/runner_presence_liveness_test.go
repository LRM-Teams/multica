package main

import (
	"testing"
	"time"
)

func TestRunnerIDCacheGetSet(t *testing.T) {
	c := newRunnerIDCache()
	now := time.Now()
	if _, ok := c.get("a|w"); ok {
		t.Fatal("cache should miss on empty")
	}
	c.set("a|w", "rt-1", now)
	got, ok := c.get("a|w")
	if !ok || got != "rt-1" {
		t.Fatalf("cache get after set = %q, %v; want rt-1, true", got, ok)
	}
	if _, ok := c.get("b|w"); ok {
		t.Fatal("cache should not leak keys")
	}
}

func TestRunnerIDCachePruneExpiresUnrefreshedEntries(t *testing.T) {
	c := newRunnerIDCache()
	now := time.Now()
	c.set("a|w", "rt-1", now)
	c.set("b|w", "rt-2", now.Add(-10*time.Minute)) // not refreshed for a long time

	c.prune(now, runnerIDCacheTTL)

	if _, ok := c.get("a|w"); !ok {
		t.Fatal("recently-refreshed entry must survive prune")
	}
	if _, ok := c.get("b|w"); ok {
		t.Fatal("stale entry must be pruned (disconnected runner)")
	}
}
