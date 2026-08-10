// Package memorysync implements center-memory identity, comparison, and
// conflict policy A for Multica agent durable memory:
//
//   - same identity + same content -> noop
//   - same identity + more specific -> update active
//   - same identity + opposed -> keep existing active, enqueue conflict
//   - machines/runtimes are provenance only
package memorysync

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"regexp"
	"strings"
	"unicode"
)

const (
	StatusActive     = "active"
	StatusConflict   = "conflict"
	StatusSuperseded = "superseded"

	KindPreference   = "preference"
	KindRelationship = "relationship"
	KindFact         = "fact"
	KindDecision     = "decision"
	KindState        = "state"
	KindContext      = "context"

	DecisionSame         = "same"
	DecisionMoreSpecific = "more_specific"
	DecisionOpposed      = "opposed"
	DecisionNew          = "new"
)

// Entry is one durable memory atom synced to the center store.
type Entry struct {
	Scope     string
	SubjectID string
	Kind      string
	Topic     string
	RelPath   string
	Content   string
}

// CompareResult is the deterministic upsert decision for strategy A.
type CompareResult struct {
	Decision string
	Reason   string
}

var (
	bulletRE        = regexp.MustCompile(`(?m)^\s*[-*]\s+(.+)$`)
	negationRE      = regexp.MustCompile(`(?i)(不要|别再|别|禁止|从不|勿|never|don't|do\s+not|no\s+longer|避免)`)
	positiveRE      = regexp.MustCompile(`(?i)(必须|一定要|总是|先|务必|always|must|should|require)`)
	unixLocalPathRE = regexp.MustCompile(`(?i)(^|[\s\x60'"(])/(home|users|tmp|private|opt|mnt|volumes|var/(tmp|run))(/|\b)`)
	windowsPathRE   = regexp.MustCompile(`(?i)(^|[\s\x60'"(])[a-z]:[\\/]`)
	loopbackRE      = regexp.MustCompile(`(?i)(^|[^a-z0-9])(localhost|127\.0\.0\.1|0\.0\.0\.0|::1)([^a-z0-9]|$)`)
	secretRE        = regexp.MustCompile(`(?i)(-----BEGIN [A-Z ]*PRIVATE KEY-----|\bAKIA[0-9A-Z]{16}\b|\b(ghp|github_pat|xox[baprs]|sk)-[a-z0-9_-]{12,}\b|\b(password|passwd|api[_-]?key|access[_-]?token|secret)\s*[:=]\s*\S+)`)
)

// NormalizeTopic keeps topic keys short and stable.
func NormalizeTopic(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			b.WriteRune(r)
			lastUnderscore = false
		case r == '_' || r == '-' || unicode.IsSpace(r):
			if b.Len() > 0 && !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

// InferTopic picks a coarse topic from durable preference text.
func InferTopic(content string) string {
	text := strings.ToLower(content)
	switch {
	case strings.Contains(text, "进度") || strings.Contains(text, "汇报") || strings.Contains(text, "feedback") || strings.Contains(text, "progress"):
		return "progress_feedback"
	case strings.Contains(text, "指出") || strings.Contains(text, "赞同") || strings.Contains(text, "critique") || strings.Contains(text, "disagree"):
		return "direct_critique"
	case strings.Contains(text, "隐私") || strings.Contains(text, "个人信息") || strings.Contains(text, "privacy") || strings.Contains(text, "pii"):
		return "privacy_redaction"
	case strings.Contains(text, "中文") || strings.Contains(text, "english") || strings.Contains(text, "语言") || strings.Contains(text, "language"):
		return "language_preference"
	default:
		return "durable_" + ContentHash(NormalizeContent(content))[:12]
	}
}

// NormalizeContent collapses whitespace for comparison/hashing.
func NormalizeContent(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

// ContentHash returns a stable hex sha256 of normalized content.
func ContentHash(content string) string {
	sum := sha256.Sum256([]byte(NormalizeContent(content)))
	return hex.EncodeToString(sum[:])
}

// IdentityKey builds the center identity for strategy A.
func IdentityKey(scope, subjectID, kind, topic, content string) string {
	scope = strings.ToLower(strings.TrimSpace(scope))
	kind = strings.ToLower(strings.TrimSpace(kind))
	topic = NormalizeTopic(topic)
	subjectID = strings.TrimSpace(subjectID)
	if scope == "" {
		scope = "user"
	}
	if kind == "" {
		kind = KindPreference
	}
	if topic == "" {
		topic = InferTopic(content)
	}
	if subjectID == "" || scope == "agent" {
		return scope + "+" + kind + "+" + topic
	}
	return scope + ":" + subjectID + "+" + kind + "+" + topic
}

// Compare implements strategy A between an existing active entry and an incoming one.
func Compare(existingContent, incomingContent string) CompareResult {
	a := NormalizeContent(existingContent)
	b := NormalizeContent(incomingContent)
	if a == "" && b == "" {
		return CompareResult{Decision: DecisionSame, Reason: "both empty"}
	}
	if a == b || strings.EqualFold(a, b) {
		return CompareResult{Decision: DecisionSame, Reason: "identical"}
	}
	if opposed(a, b) {
		return CompareResult{Decision: DecisionOpposed, Reason: "semantic opposition"}
	}
	if moreSpecific(b, a) {
		return CompareResult{Decision: DecisionMoreSpecific, Reason: "incoming more specific"}
	}
	if moreSpecific(a, b) {
		// Incoming is weaker/less specific — keep existing active (treat as same-ish noop).
		return CompareResult{Decision: DecisionSame, Reason: "existing already more specific"}
	}
	// Same identity but neither clearly more specific nor opposed → conflict (strategy A keep active).
	return CompareResult{Decision: DecisionOpposed, Reason: "same identity divergent wording"}
}

func moreSpecific(candidate, baseline string) bool {
	c := strings.ToLower(candidate)
	b := strings.ToLower(baseline)
	if c == "" || b == "" || c == b {
		return false
	}
	if strings.Contains(c, b) && len([]rune(c)) > len([]rune(b))+2 {
		return true
	}
	// Candidate adds concrete constraints while sharing most tokens.
	ct, bt := tokenSet(c), tokenSet(b)
	if len(bt) == 0 {
		return false
	}
	overlap := 0
	for t := range bt {
		if ct[t] {
			overlap++
		}
	}
	if overlap*100/len(bt) >= 70 && len(ct) > len(bt) {
		return true
	}
	return false
}

func opposed(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	aNeg, bNeg := negationRE.MatchString(la), negationRE.MatchString(lb)
	aPos, bPos := positiveRE.MatchString(la), positiveRE.MatchString(lb)
	if (aNeg && bPos && !bNeg) || (bNeg && aPos && !aNeg) {
		// Share topical tokens so "never use emoji" vs "must report progress" isn't opposed.
		if topicalOverlap(la, lb) {
			return true
		}
	}
	pairs := [][2]string{
		{"先报", "直接干"},
		{"先反馈", "直接干"},
		{"报进度", "别报"},
		{"report progress", "do not report"},
		{"先确认", "直接"},
		{"指出", "赞同"},
	}
	for _, p := range pairs {
		if (strings.Contains(la, p[0]) && strings.Contains(lb, p[1])) ||
			(strings.Contains(la, p[1]) && strings.Contains(lb, p[0])) {
			return true
		}
	}
	return false
}

func topicalOverlap(a, b string) bool {
	ta, tb := tokenSet(a), tokenSet(b)
	if len(ta) == 0 || len(tb) == 0 {
		return false
	}
	overlap := 0
	for t := range ta {
		if tb[t] {
			overlap++
		}
	}
	return overlap >= 1
}

func tokenSet(s string) map[string]bool {
	out := map[string]bool{}
	var b strings.Builder
	flush := func() {
		tok := strings.TrimSpace(b.String())
		b.Reset()
		if tok == "" || len([]rune(tok)) < 2 {
			return
		}
		out[tok] = true
	}
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Han, r) {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// PortabilityReason returns a stable reason when content is bound to one
// machine or appears to contain a credential. Empty means the content is safe
// to replicate as portable center memory. The detector is intentionally
// conservative: rejected content remains available in the local agent tree.
func PortabilityReason(content string) string {
	content = NormalizeContent(content)
	if content == "" {
		return "empty"
	}
	switch {
	case secretRE.MatchString(content):
		return "credential_like"
	case unixLocalPathRE.MatchString(content):
		return "absolute_local_path"
	case windowsPathRE.MatchString(content):
		return "absolute_local_path"
	case loopbackRE.MatchString(content):
		return "loopback_endpoint"
	default:
		return ""
	}
}

// IsPortableContent reports whether a memory atom may leave the source
// device. Device-local content is retained on disk but excluded from center
// sync and cross-device hydration.
func IsPortableContent(content string) bool {
	return PortabilityReason(content) == ""
}

// ScopeFromRelPath derives scope/subject/kind from a Multica agent-root relative path.
func ScopeFromRelPath(relPath string) (scope, subjectID, kind string) {
	rel := path.Clean(strings.ReplaceAll(strings.TrimSpace(relPath), "\\", "/"))
	parts := strings.Split(rel, "/")
	base := path.Base(rel)
	switch {
	case len(parts) == 3 && parts[0] == "users" && parts[1] != "" && base == "USER.md":
		return "user", parts[1], KindPreference
	case len(parts) == 3 && parts[0] == "users" && parts[1] != "" && base == "RELATIONSHIP.md":
		return "user", parts[1], KindRelationship
	case len(parts) == 3 && parts[0] == "projects" && parts[1] != "" && base == "MEMORY.md":
		return "project", parts[1], KindFact
	case len(parts) == 3 && parts[0] == "projects" && parts[1] != "" && base == "DECISIONS.md":
		return "project", parts[1], KindDecision
	case len(parts) == 3 && parts[0] == "projects" && parts[1] != "" && base == "STATE.md":
		return "project", parts[1], KindState
	case len(parts) == 3 && parts[0] == "channels" && parts[1] != "" && base == "CONTEXT.md":
		return "channel", parts[1], KindContext
	case rel == "memory/MEMORY.md":
		return "agent", "", KindFact
	default:
		return "", "", ""
	}
}

// IsDurableRelPath reports whether a path should participate in center sync.
func IsDurableRelPath(relPath string) bool {
	scope, _, _ := ScopeFromRelPath(relPath)
	return scope != ""
}

// ExtractBullets pulls markdown list items; if none, returns the trimmed body as one item.
func ExtractBullets(content string) []string {
	matches := bulletRE.FindAllStringSubmatch(content, -1)
	out := make([]string, 0, len(matches))
	seen := map[string]bool{}
	for _, m := range matches {
		line := NormalizeContent(m[1])
		if line == "" || seen[line] {
			continue
		}
		// Skip scaffold headings leftovers that look empty of preference.
		if strings.HasPrefix(line, "#") {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	if len(out) > 0 {
		return out
	}
	body := NormalizeContent(stripMarkdownHeadings(content))
	if body == "" {
		return nil
	}
	return []string{body}
}

func stripMarkdownHeadings(content string) string {
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		kept = append(kept, trim)
	}
	return strings.Join(kept, " ")
}

// EntriesFromFile builds sync atoms from one durable memory file.
func EntriesFromFile(relPath, fileContent string) []Entry {
	scope, subjectID, kind := ScopeFromRelPath(relPath)
	if scope == "" {
		return nil
	}
	bullets := ExtractBullets(fileContent)
	out := make([]Entry, 0, len(bullets))
	for _, bullet := range bullets {
		topic := InferTopic(bullet)
		out = append(out, Entry{
			Scope:     scope,
			SubjectID: subjectID,
			Kind:      kind,
			Topic:     topic,
			RelPath:   path.Clean(strings.ReplaceAll(relPath, "\\", "/")),
			Content:   bullet,
		})
	}
	return out
}

// BuildIdentity returns identity key for an entry.
func (e Entry) BuildIdentity() string {
	return IdentityKey(e.Scope, e.SubjectID, e.Kind, e.Topic, e.Content)
}
