package main

import "testing"

func TestRunnerIDCacheGetSet(t *testing.T) {
	c := newRunnerIDCache()
	if _, ok := c.get("a|w"); ok {
		t.Fatal("cache should miss on empty")
	}
	c.set("a|w", "rt-1")
	got, ok := c.get("a|w")
	if !ok || got != "rt-1" {
		t.Fatalf("cache get after set = %q, %v; want rt-1, true", got, ok)
	}
	if _, ok := c.get("b|w"); ok {
		t.Fatal("cache should not leak keys")
	}
}
