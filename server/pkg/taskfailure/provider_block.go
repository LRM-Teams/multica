package taskfailure

import (
	"regexp"
	"strings"
	"time"
)

// DefaultProviderQuotaBlockTTL is used when a quota/usage-limit failure has
// no parseable reset timestamp. Long enough to stop the claim thrash that
// made #77 flap online↔error; short enough that a misclassified transient
// 429 does not strand an agent for days.
const DefaultProviderQuotaBlockTTL = time.Hour

// providerResetAtRe matches the common "YYYY-MM-DD HH:MM:SS" reset stamp in
// Chinese provider copy ("…2026-08-03 13:52:38 后可继续使用").
var providerResetAtRe = regexp.MustCompile(`(20\d{2}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2})`)

// IsStickyProviderQuotaLock reports whether this failure should pin the agent
// unavailable until a reset time (no auto-retry / no claim). Capacity-only
// 429s stay false — those are still backoff-retryable.
func IsStickyProviderQuotaLock(errText, failureReason string) bool {
	reason := Reason(strings.TrimSpace(failureReason))
	if reason == ReasonAgentProviderQuotaLimit {
		return true
	}
	// Prefer re-classifying the raw text: daemon/inbox may still send the
	// old capacity bucket for Chinese usage-limit 429s until all callers
	// pick up the classify fix.
	return Classify(errText) == ReasonAgentProviderQuotaLimit
}

// ParseProviderBlockedUntil extracts a reset timestamp from provider error
// text. Falls back to now+DefaultProviderQuotaBlockTTL when none is found.
// Timestamps without a zone are treated as local wall-clock in loc (pass
// time.Local in production; tests inject a fixed loc).
func ParseProviderBlockedUntil(errText string, now time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	if m := providerResetAtRe.FindStringSubmatch(errText); len(m) > 1 {
		raw := strings.Replace(m[1], "T", " ", 1)
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", raw, loc); err == nil {
			if t.After(now) {
				return t
			}
		}
	}
	return now.Add(DefaultProviderQuotaBlockTTL)
}
