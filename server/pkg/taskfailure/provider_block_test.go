package taskfailure

import (
	"testing"
	"time"
)

func TestIsStickyProviderQuotaLock(t *testing.T) {
	t.Parallel()
	err := `429: {"code":"1310","message":"已达到 7 天使用上限，2026-08-03 13:52:38 后可继续使用。"}`
	if !IsStickyProviderQuotaLock(err, string(ReasonAgentProviderCapacityOrRateLimit)) {
		t.Fatal("code-1310 usage-limit 429 must sticky-lock even when failure_reason still says capacity")
	}
	if !IsStickyProviderQuotaLock("x", string(ReasonAgentProviderQuotaLimit)) {
		t.Fatal("explicit quota reason must sticky-lock")
	}
	if IsStickyProviderQuotaLock("API Error: 429 Too Many Requests", string(ReasonAgentProviderCapacityOrRateLimit)) {
		t.Fatal("bare transient 429 must not sticky-lock")
	}
	if IsStickyProviderQuotaLock(
		"2026-08-04T09:34:14.402226Z model catalog refresh timed out",
		"codex_semantic_inactivity",
	) {
		t.Fatal("fractional timestamp beginning with 402 must not sticky-lock")
	}
}

func TestParseProviderBlockedUntil(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, loc)
	err := `429: {"code":"1310","message":"已达到 7 天使用上限，2026-08-03 13:52:38 后可继续使用。"}`
	got, ok := ParseProviderBlockedUntil(err, now, loc)
	if !ok {
		t.Fatal("expected parse ok")
	}
	want := time.Date(2026, 8, 3, 13, 52, 38, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("ParseProviderBlockedUntil = %v, want %v", got, want)
	}

	_, ok = ParseProviderBlockedUntil("quota exceeded with no stamp", now, loc)
	if ok {
		t.Fatal("must not invent a reset time when none is present")
	}
}

func TestProviderLockActive(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	if ProviderLockActive("", time.Time{}, false, now) {
		t.Fatal("empty detail must be unlocked")
	}
	if !ProviderLockActive("quota", time.Time{}, false, now) {
		t.Fatal("detail set + until unknown must stay locked")
	}
	if !ProviderLockActive("quota", now.Add(time.Hour), true, now) {
		t.Fatal("future until must be locked")
	}
	if ProviderLockActive("quota", now.Add(-time.Minute), true, now) {
		t.Fatal("elapsed until must unlock")
	}
}

func TestProviderLockActiveRejectsBlankJSONPlaceholders(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	for _, detail := range []string{"{}", "{ }", "[]", "null", "\"\"", " \n\t "} {
		if ProviderLockActive(detail, time.Time{}, false, now) {
			t.Fatalf("placeholder detail %q must be unlocked", detail)
		}
		if ProviderLockDetailActive(detail) {
			t.Fatalf("ProviderLockDetailActive(%q) = true, want false", detail)
		}
	}
	if !ProviderLockDetailActive("429 quota exceeded") {
		t.Fatal("real quota copy must stay a lock detail")
	}
}
