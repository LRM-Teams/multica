package taskfailure

import (
	"testing"
	"time"
)

func TestIsStickyProviderQuotaLock(t *testing.T) {
	t.Parallel()
	err := `429: {"code":"1310","message":"已达到 7 天使用上限，2026-08-03 13:52:38 后可继续使用。"}`
	if !IsStickyProviderQuotaLock(err, string(ReasonAgentProviderCapacityOrRateLimit)) {
		t.Fatal("chinese usage-limit 429 must sticky-lock even when failure_reason still says capacity")
	}
	if !IsStickyProviderQuotaLock("x", string(ReasonAgentProviderQuotaLimit)) {
		t.Fatal("explicit quota reason must sticky-lock")
	}
	if IsStickyProviderQuotaLock("API Error: 429 Too Many Requests", string(ReasonAgentProviderCapacityOrRateLimit)) {
		t.Fatal("bare transient 429 must not sticky-lock")
	}
}

func TestParseProviderBlockedUntil(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, loc)
	err := `429: {"code":"1310","message":"已达到 7 天使用上限，2026-08-03 13:52:38 后可继续使用。"}`
	got := ParseProviderBlockedUntil(err, now, loc)
	want := time.Date(2026, 8, 3, 13, 52, 38, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("ParseProviderBlockedUntil = %v, want %v", got, want)
	}

	fallback := ParseProviderBlockedUntil("quota exceeded", now, loc)
	if !fallback.Equal(now.Add(DefaultProviderQuotaBlockTTL)) {
		t.Fatalf("fallback = %v, want now+1h", fallback)
	}
}
