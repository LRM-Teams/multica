package memorycuration

import (
	"fmt"
	"strings"
	"time"
)

type reviewEntry struct {
	ID                  string
	Type                string
	Status              string
	Confidence          string
	Sensitivity         string
	Scope               string
	Topic               string
	SourceDate          string
	ReviewExpiresAt     string
	Evidence            []string
	ProposedDestination string
	Title               string
	Body                string
}

func (e reviewEntry) HashKey() string {
	return hashShort(e.Type, e.ProposedDestination, e.Body, strings.Join(e.Evidence, ","))
}

func (e reviewEntry) Expired(now time.Time) bool {
	if e.ReviewExpiresAt == "" {
		return false
	}
	d, err := time.Parse("2006-01-02", e.ReviewExpiresAt)
	if err != nil {
		return false
	}
	return dateOnly(d).Before(dateOnly(now))
}

func parseReview(content string) ([]reviewEntry, error) {
	content = strings.TrimSpace(content)
	if content == "" || content == strings.TrimSpace(reviewHeader) {
		return nil, nil
	}
	parts := strings.Split(content, "\n---\n")
	var entries []reviewEntry
	for i := 1; i+1 < len(parts); i += 2 {
		meta := parseMeta(parts[i])
		bodyPart := strings.TrimSpace(parts[i+1])
		lines := strings.Split(bodyPart, "\n")
		title := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[0]), "#"))
		body := strings.TrimSpace(strings.Join(lines[1:], "\n"))
		entry := reviewEntry{
			ID:                  meta["id"],
			Type:                meta["type"],
			Status:              defaultString(meta["status"], "candidate"),
			Confidence:          defaultString(meta["confidence"], "medium"),
			Sensitivity:         defaultString(meta["sensitivity"], "unknown"),
			Scope:               defaultString(meta["scope"], "agent"),
			SourceDate:          meta["source_date"],
			ReviewExpiresAt:     meta["review_expires_at"],
			Evidence:            splitCSV(meta["evidence"]),
			ProposedDestination: meta["proposed_destination"],
			Title:               title,
			Body:                body,
		}
		if entry.ID == "" {
			entry.ID = "mem_unknown_" + entry.HashKey()
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func parseMeta(block string) map[string]string {
	out := map[string]string{}
	var current string
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "-") && current != "" {
			out[current] = strings.TrimSpace(out[current] + "," + strings.TrimSpace(strings.TrimPrefix(trimmed, "-")))
			continue
		}
		idx := strings.Index(trimmed, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:idx])
		val := strings.TrimSpace(trimmed[idx+1:])
		current = key
		out[key] = val
	}
	return out
}

func renderReview(entries []reviewEntry) string {
	if len(entries) == 0 {
		return reviewHeader
	}
	sortEntries(entries)
	var b strings.Builder
	b.WriteString(strings.TrimRight(reviewHeader, "\n"))
	b.WriteString("\n")
	for _, entry := range entries {
		if entry.Status == "" {
			entry.Status = "candidate"
		}
		if entry.Confidence == "" {
			entry.Confidence = "medium"
		}
		if entry.Sensitivity == "" {
			entry.Sensitivity = "unknown"
		}
		if entry.Scope == "" {
			entry.Scope = "agent"
		}
		fmt.Fprintf(&b, "\n---\nid: %s\ntype: %s\nstatus: %s\nconfidence: %s\nsensitivity: %s\nscope: %s\n", entry.ID, entry.Type, entry.Status, entry.Confidence, entry.Sensitivity, entry.Scope)
		if entry.SourceDate != "" {
			fmt.Fprintf(&b, "source_date: %s\n", entry.SourceDate)
		}
		if entry.ReviewExpiresAt != "" {
			fmt.Fprintf(&b, "review_expires_at: %s\n", entry.ReviewExpiresAt)
		}
		if len(entry.Evidence) > 0 {
			fmt.Fprintf(&b, "evidence: %s\n", strings.Join(entry.Evidence, ","))
		}
		if entry.ProposedDestination != "" {
			fmt.Fprintf(&b, "proposed_destination: %s\n", entry.ProposedDestination)
		}
		fmt.Fprintf(&b, "---\n# %s\n\n%s\n", entry.Title, strings.TrimSpace(entry.Body))
	}
	return b.String()
}

func defaultString(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func splitCSV(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
