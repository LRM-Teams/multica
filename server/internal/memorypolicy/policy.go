// Package memorypolicy defines compact, deterministic limits for canonical
// Multica memory files. The limits intentionally follow the amount of each
// file that can be recalled for one execution: content that cannot fit in the
// next prompt should be compacted instead of silently accumulating forever.
package memorypolicy

import (
	"bufio"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

const (
	DailyMaxNewEntriesPerTurn = 3
	DailyEntryMaxRunes        = 240
	DurableEntryMaxRunes      = 180
	ReviewEntryMaxRunes       = 360
)

// SoftFileLimit returns the compact target size for a canonical memory file.
// Zero means the path is not governed by this policy.
func SoftFileLimit(relPath string) int64 {
	rel := path.Clean(strings.ReplaceAll(strings.TrimSpace(relPath), "\\", "/"))
	parts := strings.Split(rel, "/")
	base := path.Base(rel)
	switch {
	case rel == "memory/MEMORY.md", rel == "memory/STATE.md":
		return 2 * 1024
	case rel == "memory/REVIEW.md":
		return 8 * 1024
	case len(parts) == 3 && parts[0] == "memory" && parts[1] == "daily" && strings.HasSuffix(base, ".md"):
		return 2 * 1024
	case len(parts) == 3 && parts[0] == "users" && parts[1] != "" && base == "USER.md":
		return 2 * 1024
	case len(parts) == 3 && parts[0] == "users" && parts[1] != "" && base == "RELATIONSHIP.md":
		return 1024
	case len(parts) == 3 && parts[0] == "projects" && parts[1] != "" && base == "MEMORY.md":
		return 4 * 1024
	case len(parts) == 3 && parts[0] == "projects" && parts[1] != "" && base == "STATE.md":
		return 2 * 1024
	case len(parts) == 3 && parts[0] == "projects" && parts[1] != "" && base == "DECISIONS.md":
		return 3 * 1024
	case len(parts) == 3 && parts[0] == "channels" && parts[1] != "" && base == "CONTEXT.md":
		return 1536
	default:
		return 0
	}
}

// ValidateFile rejects memory that remains too large or contains an
// overlong entry. It does not truncate: a self-review must merge, dedupe, or
// move detail back to source evidence without silently losing facts.
func ValidateFile(relPath string, content []byte) error {
	limit := SoftFileLimit(relPath)
	if limit == 0 {
		return nil
	}
	if int64(len(content)) > limit {
		return fmt.Errorf("%s is %d bytes; compact target is %d bytes", relPath, len(content), limit)
	}
	entryLimit := DurableEntryMaxRunes
	if strings.HasPrefix(path.Clean(relPath), "memory/daily/") {
		entryLimit = DailyEntryMaxRunes
	} else if path.Clean(relPath) == "memory/REVIEW.md" {
		entryLimit = ReviewEntryMaxRunes
	}

	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") || line == "§" {
			continue
		}
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			line = strings.TrimSpace(line[2:])
		}
		if utf8.RuneCountInString(line) > entryLimit {
			return fmt.Errorf("%s line %d has %d characters; entry limit is %d", relPath, lineNumber, utf8.RuneCountInString(line), entryLimit)
		}
	}
	return scanner.Err()
}
