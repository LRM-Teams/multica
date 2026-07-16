// Package mention provides utilities for expanding issue identifier references
// (e.g. MUL-117) into clickable mention links in markdown content.
package mention

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// IssueResolver looks up an issue by workspace and number.
// Implemented by db.Queries.
type IssueResolver interface {
	GetIssueByNumber(ctx context.Context, arg db.GetIssueByNumberParams) (db.Issue, error)
}

// PrefixResolver looks up a workspace to get its issue prefix.
type PrefixResolver interface {
	GetWorkspace(ctx context.Context, id pgtype.UUID) (db.Workspace, error)
}

// Resolver combines both interfaces needed for mention expansion.
type Resolver interface {
	IssueResolver
	PrefixResolver
}

// ExpandIssueIdentifiers scans markdown content for bare issue identifier
// patterns (e.g. MUL-117) and replaces them with mention links:
// [MUL-117](mention://issue/<uuid>)
//
// It skips identifiers that are:
//   - Already inside a markdown link: [MUL-117](...)
//   - Inside inline code: `MUL-117`
//   - Inside fenced code blocks: ```...```
func ExpandIssueIdentifiers(ctx context.Context, resolver Resolver, workspaceID pgtype.UUID, content string) string {
	// Get the workspace prefix.
	ws, err := resolver.GetWorkspace(ctx, workspaceID)
	if err != nil || ws.IssuePrefix == "" {
		return content
	}
	identifiers := FindBareIssueIdentifiers(ws.IssuePrefix, content)
	if len(identifiers) == 0 {
		return content
	}

	// Build a set of replacements (offset → replacement string).
	type replacement struct {
		start, end int
		text       string
	}
	var replacements []replacement

	for _, identifier := range identifiers {
		// Look up the issue.
		issue, err := resolver.GetIssueByNumber(ctx, db.GetIssueByNumberParams{
			WorkspaceID: workspaceID,
			Number:      identifier.Number,
		})
		if err != nil {
			continue // Issue doesn't exist — leave as-is.
		}

		issueID := uuidToString(issue.ID)
		mentionLink := fmt.Sprintf("[%s](mention://issue/%s)", identifier.Label, issueID)

		replacements = append(replacements, replacement{
			start: identifier.Start,
			end:   identifier.End,
			text:  mentionLink,
		})
	}

	if len(replacements) == 0 {
		return content
	}

	// Apply replacements from right to left to preserve offsets.
	result := content
	for i := len(replacements) - 1; i >= 0; i-- {
		r := replacements[i]
		result = result[:r.start] + r.text + result[r.end:]
	}

	return result
}

// BareIssueIdentifier is a workspace-scoped human issue key in visible text.
// Its byte offsets address the original content so callers can either replace
// legacy markdown or attach a structured reference without changing text.
type BareIssueIdentifier struct {
	Start  int
	End    int
	Label  string
	Number int32
}

// FindBareIssueIdentifiers finds plain workspace issue keys such as MUL-117.
// It intentionally leaves markdown untouched: callers that persist structured
// message parts use the result to attach typed issue-ref metadata, while the
// legacy comment path may still choose to replace text with a markdown link.
// Inline code, fenced code, and existing markdown links are never references.
func FindBareIssueIdentifiers(prefix, content string) []BareIssueIdentifier {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || content == "" {
		return nil
	}
	pattern := regexp.MustCompile(regexp.QuoteMeta(prefix) + `-(\d+)`)
	skipRegions := findSkipRegions(content)
	matches := pattern.FindAllStringSubmatchIndex(content, -1)
	out := make([]BareIssueIdentifier, 0, len(matches))
	for _, match := range matches {
		start, end := match[0], match[1]
		if !hasBareIssueIdentifierBoundaries(content, start, end) ||
			inSkipRegion(start, skipRegions) || isInsideMarkdownLink(content, start, end) {
			continue
		}
		number, err := strconv.ParseInt(content[match[2]:match[3]], 10, 32)
		if err != nil || number <= 0 {
			continue
		}
		out = append(out, BareIssueIdentifier{
			Start:  start,
			End:    end,
			Label:  content[start:end],
			Number: int32(number),
		})
	}
	return out
}

// hasBareIssueIdentifierBoundaries keeps identifier discovery from matching a
// word fragment without consuming punctuation around a match. Consuming the
// trailing boundary makes RE2's non-overlapping matcher skip a second
// identifier immediately after that punctuation (for example, LRM-126。LRM-126).
func hasBareIssueIdentifierBoundaries(content string, start, end int) bool {
	return (start == 0 || !isASCIIWordByte(content[start-1])) &&
		(end == len(content) || !isASCIIWordByte(content[end]))
}

func isASCIIWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' ||
		b >= 'A' && b <= 'Z' ||
		b >= '0' && b <= '9' ||
		b == '_'
}

// skipRegion represents a region of text that should not be modified.
type skipRegion struct {
	start, end int
}

// findSkipRegions identifies fenced code blocks (```) and inline code (`)
// regions in the content.
func findSkipRegions(content string) []skipRegion {
	var regions []skipRegion

	// Fenced code blocks: ```...```
	fenceRe := regexp.MustCompile("(?m)^```[^`]*\n[\\s\\S]*?\n```")
	for _, loc := range fenceRe.FindAllStringIndex(content, -1) {
		regions = append(regions, skipRegion{loc[0], loc[1]})
	}

	// Inline code: `...` (but not inside fenced blocks — already handled).
	inlineRe := regexp.MustCompile("`[^`\n]+`")
	for _, loc := range inlineRe.FindAllStringIndex(content, -1) {
		regions = append(regions, skipRegion{loc[0], loc[1]})
	}

	return regions
}

// inSkipRegion checks if a position falls within any skip region.
func inSkipRegion(pos int, regions []skipRegion) bool {
	for _, r := range regions {
		if pos >= r.start && pos < r.end {
			return true
		}
	}
	return false
}

// isInsideMarkdownLink checks if the text at [start:end] is already part of
// a markdown link like [MUL-117](mention://...) or [text](url).
func isInsideMarkdownLink(content string, start, end int) bool {
	// Check if preceded by '[' (part of link text).
	if start > 0 {
		before := strings.TrimRight(content[:start], " ")
		if len(before) > 0 && before[len(before)-1] == '[' {
			return true
		}
	}
	// Check if followed by '](', indicating it's the link text of a markdown link.
	after := content[end:]
	if strings.HasPrefix(after, "](") {
		return true
	}
	// Check if we're inside the URL part of a link: ...](mention://issue/...).
	// Look backwards for ]( pattern.
	idx := strings.LastIndex(content[:start], "](")
	if idx >= 0 {
		// Check that we haven't passed a closing ) yet.
		between := content[idx:start]
		if !strings.Contains(between, ")") {
			return true
		}
	}
	return false
}

func uuidToString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
