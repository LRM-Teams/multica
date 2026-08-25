package diagnosticlog

import (
	"net/url"
	"os"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxDiagnosticBytes = 2 << 10

var (
	ansiPattern             = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))`)
	urlPattern              = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s]+`)
	secretKVPattern         = regexp.MustCompile(`(?i)"?\b(authorization|password|passwd|token|api[_-]?key|secret|cookie)"?\s*[:=]\s*"?(?:bearer\s+)?[^"\s,}]+"?`)
	secretFlagPattern       = regexp.MustCompile(`(?i)--(?:password|passwd|token|api[_-]?key|secret)(?:=|\s+)\S+`)
	environmentValuePattern = regexp.MustCompile(`\b[A-Z][A-Z0-9_]{2,}\s*=\s*[^\s]+`)
	jwtPattern              = regexp.MustCompile(`\b[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,}(?:\.[A-Za-z0-9_-]{12,})?\b`)
	credentialTokenPattern  = regexp.MustCompile(`(?i)\b(?:sk-[A-Za-z0-9_-]{12,}|gh[opsu]_[A-Za-z0-9]{20,}|xox[baprs]-[A-Za-z0-9-]{12,}|AKIA[A-Z0-9]{16})\b`)
	windowsPathPattern      = regexp.MustCompile(`(?i)\b[A-Z]:\\(?:[^\s\\]+\\)*[^\s\\]*`)
	unixPathPattern         = regexp.MustCompile(`(?:^|\s)/(?:[^\s/]+/?)+`)
)

func sanitizeEvidence(e Evidence) (diagnostic string, stderr string, truncated []string, redactionFailed bool) {
	diagnostic = sanitizeText(e.Detail)
	if bounded, cut := truncateUTF8(diagnostic, maxDiagnosticBytes); cut {
		diagnostic = bounded
		truncated = append(truncated, "diagnostic")
	}

	stderrBytes := e.StderrTail
	if len(stderrBytes) > MaxStderrBytes {
		stderrBytes = stderrBytes[len(stderrBytes)-MaxStderrBytes:]
		truncated = append(truncated, "stderr_tail")
	}
	stderr = sanitizeText(string(bytesToValidUTF8(stderrBytes)))
	if bounded, cut := truncateUTF8(stderr, MaxStderrBytes); cut {
		stderr = bounded
		truncated = appendUnique(truncated, "stderr_tail")
	}
	return diagnostic, stderr, truncated, false
}

func sanitizeText(value string) string { return SanitizeText(value) }

// SanitizeText removes credentials and unsafe diagnostics from an observability value.
// It is the exported form of the package's established redaction behavior.
func SanitizeText(value string) string {
	value = strings.ToValidUTF8(value, "�")
	value = ansiPattern.ReplaceAllString(value, "")
	value = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		default:
			return r
		}
	}, value)
	value = urlPattern.ReplaceAllStringFunc(value, sanitizeURL)
	value = secretKVPattern.ReplaceAllString(value, "$1=[REDACTED]")
	value = secretFlagPattern.ReplaceAllString(value, "--credential=[REDACTED]")
	value = environmentValuePattern.ReplaceAllStringFunc(value, func(match string) string {
		key, _, _ := strings.Cut(match, "=")
		return strings.TrimSpace(key) + "=[REDACTED]"
	})
	value = jwtPattern.ReplaceAllString(value, "[REDACTED]")
	value = credentialTokenPattern.ReplaceAllString(value, "[REDACTED]")
	value = windowsPathPattern.ReplaceAllString(value, "[path]")
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		value = strings.ReplaceAll(value, home, "~")
	}
	value = unixPathPattern.ReplaceAllStringFunc(value, func(match string) string {
		prefix := ""
		if strings.HasPrefix(match, " ") {
			prefix = " "
		}
		path := strings.TrimSpace(match)
		if strings.HasPrefix(path, "~/") {
			return prefix + path
		}
		return prefix + "[path]"
	})
	return strings.Join(strings.Fields(value), " ")
}

func sanitizeURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "[url]"
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != "http" && scheme != "https") || parsed.Host == "" {
		return "[url]"
	}
	parsed.User = nil
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return scheme + "://" + parsed.Host
}

func bytesToValidUTF8(value []byte) []byte {
	if utf8.Valid(value) {
		return value
	}
	return []byte(strings.ToValidUTF8(string(value), "�"))
}

func truncateUTF8(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value, true
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
