package taskfailure

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// providerResetAtRe matches the common "YYYY-MM-DD HH:MM:SS" reset stamp in
// Chinese provider copy ("…2026-08-03 13:52:38 后可继续使用").
var providerResetAtRe = regexp.MustCompile(`(20\d{2}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2})`)

// providerResetAtEnglishRe matches Codex's account-limit reset copy, for
// example "try again at Aug 20th, 2026 3:30 AM". The provider may emit either
// abbreviated or full month names, optional ordinal suffixes, and optional
// seconds.
var providerResetAtEnglishRe = regexp.MustCompile(`(?i)\b(?:Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sep(?:tember)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\s+\d{1,2}(?:st|nd|rd|th)?,\s+20\d{2}\s+\d{1,2}:\d{2}(?::\d{2})?\s+(?:AM|PM)\b`)
var providerResetOrdinalRe = regexp.MustCompile(`(?i)(\d{1,2})(?:st|nd|rd|th),`)

// providerQuotaCode1310Re matches structured Cursor/国内网关 quota lock code.
// Prefer code over free-text (Parker #64: copy changes silently; code is stable).
var providerQuotaCode1310Re = regexp.MustCompile(`(?i)"code"\s*:\s*"?1310"?`)

// IsStickyProviderQuotaLock reports whether this failure should pin the agent
// display as provider-blocked until unlock. Capacity-only 429s stay false.
func IsStickyProviderQuotaLock(errText, failureReason string) bool {
	reason := Reason(strings.TrimSpace(failureReason))
	if reason == ReasonAgentProviderQuotaLimit {
		return true
	}
	// Prefer re-classifying the raw text: daemon/inbox may still send the
	// old capacity bucket for code-1310 429s until all callers pick up the
	// classify fix.
	return Classify(errText) == ReasonAgentProviderQuotaLimit
}

// HasProviderQuotaCode1310 reports a structured quota-lock code in errText.
func HasProviderQuotaCode1310(errText string) bool {
	return providerQuotaCode1310Re.MatchString(errText)
}

// ParseProviderBlockedUntil extracts a reset timestamp from provider error
// text. It returns recognized elapsed timestamps too: persisting the real
// reset instant makes an already-expired provider lock immediately inactive,
// instead of converting it into a permanent unknown-end lock. ok=false means
// no timestamp was present — callers must still lock without inventing a TTL
// (Parker / #815).
func ParseProviderBlockedUntil(errText string, loc *time.Location) (until time.Time, ok bool) {
	if loc == nil {
		loc = time.Local
	}
	if m := providerResetAtRe.FindStringSubmatch(errText); len(m) > 1 {
		raw := strings.Replace(m[1], "T", " ", 1)
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", raw, loc); err == nil {
			return t, true
		}
	}
	if raw := providerResetAtEnglishRe.FindString(errText); raw != "" {
		raw = providerResetOrdinalRe.ReplaceAllString(raw, "$1,")
		for _, layout := range []string{
			"Jan 2, 2006 3:04 PM",
			"Jan 2, 2006 3:04:05 PM",
			"January 2, 2006 3:04 PM",
			"January 2, 2006 3:04:05 PM",
		} {
			if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// ProviderLockActive is the read-time predicate for sticky provider lock.
// Meaningless detail (empty, whitespace, or a blank JSON placeholder such as
// "{}") ⇒ unlocked. until NULL while a real detail is set ⇒ locked, unknown
// end. until known and elapsed ⇒ unlocked.
func ProviderLockActive(detail string, until time.Time, untilValid bool, now time.Time) bool {
	if !ProviderLockDetailActive(detail) {
		return false
	}
	if !untilValid {
		return true
	}
	return until.After(now)
}

// ProviderLockDetailActive reports whether detail is a real lock reason.
// Blank JSON leftovers ("{}", "[]", null) are not quota copy — treating them
// as locks paints Online agents Offline.
func ProviderLockDetailActive(detail string) bool {
	trimmed := strings.TrimSpace(detail)
	if trimmed == "" {
		return false
	}
	switch strings.ToLower(trimmed) {
	case "null", "undefined", `""`:
		return false
	}
	var parsed any
	if json.Unmarshal([]byte(trimmed), &parsed) == nil {
		switch value := parsed.(type) {
		case map[string]any:
			return len(value) > 0
		case []any:
			return len(value) > 0
		case nil:
			return false
		}
	}
	return true
}
