// Package memoryrecall implements on-demand, scope-filtered search and read
// over an agent's Multica memory files. It is the platform equivalent of
// OpenClaw memory_search / memory_get: bootstrap injects a small snapshot;
// this package answers mid-turn "what did we already write down?"
package memoryrecall

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	DefaultLimit     = 8
	MaxLimit         = 20
	DefaultGetLines  = 80
	MaxGetLines      = 200
	maxSnippetRunes  = 400
	maxFileBytes     = 64 * 1024
	minTokenLen      = 1
	recentDailyFiles = 7
)

var tokenRE = regexp.MustCompile(`[\p{Han}]|[\p{L}\p{N}_]+`)

var stopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "by": true, "for": true, "from": true,
	"in": true, "is": true, "it": true, "of": true, "on": true, "or": true, "that": true, "the": true, "to": true, "with": true,
	"了": true, "的": true, "和": true, "是": true, "在": true, "有": true, "就": true, "都": true, "也": true, "要": true,
}

// Scope is the attested execution identity used to confine reads.
// Member/project/channel IDs must come from the daemon env, not display names.
type Scope struct {
	AgentRoot string
	MemberID  string
	ProjectID string
	ChannelID string
}

// Hit is one scored memory chunk.
type Hit struct {
	Path      string  `json:"path"`
	Scope     string  `json:"scope"`
	Score     float64 `json:"score"`
	LineStart int     `json:"line_start"`
	LineEnd   int     `json:"line_end"`
	Snippet   string  `json:"snippet"`
}

// SearchResult is the stable JSON contract for `multica memory search`.
type SearchResult struct {
	Query string `json:"query"`
	Hits  []Hit  `json:"hits"`
}

// GetResult is the stable JSON contract for `multica memory get`.
type GetResult struct {
	Path      string `json:"path"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Content   string `json:"content"`
}

// ScopeFromEnv reads the daemon-injected identity. AgentRoot is required for
// any recall; missing member/project/channel simply omits those trees.
func ScopeFromEnv() Scope {
	return Scope{
		AgentRoot: strings.TrimSpace(os.Getenv("MULTICA_AGENT_ROOT")),
		MemberID:  strings.TrimSpace(os.Getenv("MULTICA_MEMBER_ID")),
		ProjectID: strings.TrimSpace(os.Getenv("MULTICA_PROJECT_ID")),
		ChannelID: strings.TrimSpace(os.Getenv("MULTICA_CHANNEL_ID")),
	}
}

// Search ranks allowlisted memory chunks against query. Empty query or missing
// root returns an error; zero hits is a successful empty list.
func Search(scope Scope, query string, limit int) (SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return SearchResult{}, fmt.Errorf("search query is required")
	}
	root, err := resolveAgentRoot(scope.AgentRoot)
	if err != nil {
		return SearchResult{}, err
	}
	scope.AgentRoot = root
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	files, err := listSearchFiles(scope)
	if err != nil {
		return SearchResult{}, err
	}
	var hits []Hit
	for _, file := range files {
		chunks, err := chunkFile(scope.AgentRoot, file.rel)
		if err != nil {
			continue
		}
		for _, chunk := range chunks {
			score := lexicalSimilarity(query, chunk.text)
			if score < 0.08 && !containsAllQueryTokens(query, chunk.text) {
				continue
			}
			if score < 0.08 {
				score = 0.08
			}
			hits = append(hits, Hit{
				Path:      file.rel,
				Scope:     file.scope,
				Score:     roundScore(score),
				LineStart: chunk.lineStart,
				LineEnd:   chunk.lineEnd,
				Snippet:   clipSnippet(chunk.text),
			})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Path != hits[j].Path {
			return hits[i].Path < hits[j].Path
		}
		return hits[i].LineStart < hits[j].LineStart
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	if hits == nil {
		hits = []Hit{}
	}
	return SearchResult{Query: query, Hits: hits}, nil
}

// Get reads an allowlisted relative path. fromLine is 1-based; lines 0 means default.
func Get(scope Scope, relPath string, fromLine, lines int) (GetResult, error) {
	root, err := resolveAgentRoot(scope.AgentRoot)
	if err != nil {
		return GetResult{}, err
	}
	scope.AgentRoot = root
	rel, err := normalizeRelPath(relPath)
	if err != nil {
		return GetResult{}, err
	}
	if !pathAllowed(scope, rel) {
		return GetResult{}, fmt.Errorf("path %q is outside the current memory scope", rel)
	}
	abs, err := confinedAbs(scope.AgentRoot, rel)
	if err != nil {
		return GetResult{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return GetResult{}, fmt.Errorf("memory file %q not found", rel)
		}
		return GetResult{}, err
	}
	if len(data) > maxFileBytes {
		data = data[:maxFileBytes]
	}
	all := strings.Split(string(data), "\n")
	if fromLine <= 0 {
		fromLine = 1
	}
	if fromLine > len(all) {
		return GetResult{Path: rel, LineStart: fromLine, LineEnd: fromLine - 1, Content: ""}, nil
	}
	if lines <= 0 {
		lines = DefaultGetLines
	}
	if lines > MaxGetLines {
		lines = MaxGetLines
	}
	start := fromLine - 1
	end := start + lines
	if end > len(all) {
		end = len(all)
	}
	return GetResult{
		Path:      rel,
		LineStart: fromLine,
		LineEnd:   end,
		Content:   strings.Join(all[start:end], "\n"),
	}, nil
}

type searchFile struct {
	rel   string
	scope string
}

func listSearchFiles(scope Scope) ([]searchFile, error) {
	var files []searchFile
	add := func(rel, kind string) {
		rel = filepath.ToSlash(rel)
		if !pathAllowed(scope, rel) {
			return
		}
		abs := filepath.Join(scope.AgentRoot, filepath.FromSlash(rel))
		if st, err := os.Stat(abs); err != nil || st.IsDir() {
			return
		}
		files = append(files, searchFile{rel: rel, scope: kind})
	}
	add("memory/MEMORY.md", "agent")
	add("memory/STATE.md", "agent")
	add("memory/REVIEW.md", "agent")
	add("notes/agents.md", "agent")
	add("notes/relationship-map.md", "agent")
	dailies, _ := filepath.Glob(filepath.Join(scope.AgentRoot, "memory", "daily", "*.md"))
	sort.Strings(dailies)
	if len(dailies) > recentDailyFiles {
		dailies = dailies[len(dailies)-recentDailyFiles:]
	}
	for _, abs := range dailies {
		rel, err := filepath.Rel(scope.AgentRoot, abs)
		if err != nil {
			continue
		}
		add(filepath.ToSlash(rel), "daily")
	}
	if scope.MemberID != "" && safeID(scope.MemberID) {
		add(filepath.ToSlash(filepath.Join("users", scope.MemberID, "USER.md")), "user")
		add(filepath.ToSlash(filepath.Join("users", scope.MemberID, "RELATIONSHIP.md")), "user")
	}
	if scope.ProjectID != "" && safeID(scope.ProjectID) {
		add(filepath.ToSlash(filepath.Join("projects", scope.ProjectID, "MEMORY.md")), "project")
		add(filepath.ToSlash(filepath.Join("projects", scope.ProjectID, "STATE.md")), "project")
		add(filepath.ToSlash(filepath.Join("projects", scope.ProjectID, "DECISIONS.md")), "project")
	}
	if scope.ChannelID != "" && safeID(scope.ChannelID) {
		add(filepath.ToSlash(filepath.Join("channels", scope.ChannelID, "CONTEXT.md")), "channel")
	}
	return files, nil
}

type textChunk struct {
	text      string
	lineStart int
	lineEnd   int
}

func chunkFile(root, rel string) ([]textChunk, error) {
	abs, err := confinedAbs(root, rel)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	if len(data) > maxFileBytes {
		data = data[:maxFileBytes]
	}
	lines := strings.Split(string(data), "\n")
	var chunks []textChunk
	var buf []string
	start := 1
	flush := func(end int) {
		text := strings.TrimSpace(strings.Join(buf, "\n"))
		if text != "" {
			chunks = append(chunks, textChunk{text: text, lineStart: start, lineEnd: end})
		}
		buf = buf[:0]
	}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		isBullet := strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ")
		if isBullet && len(buf) > 0 {
			flush(i)
			start = i + 1
		}
		if trimmed == "" && len(buf) > 0 {
			flush(i)
			start = i + 2
			continue
		}
		if trimmed == "" {
			start = i + 2
			continue
		}
		if len(buf) == 0 {
			start = i + 1
		}
		buf = append(buf, line)
	}
	flush(len(lines))
	return chunks, nil
}

func resolveAgentRoot(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("MULTICA_AGENT_ROOT is required")
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve agent root: %w", err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("agent root %q is not readable", abs)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("agent root %q is not a directory", abs)
	}
	return abs, nil
}

func normalizeRelPath(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", fmt.Errorf("path is required")
	}
	rel = filepath.ToSlash(rel)
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("absolute paths are not allowed")
	}
	clean := filepath.ToSlash(pathClean(rel))
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", fmt.Errorf("path traversal is not allowed")
	}
	return clean, nil
}

func pathClean(rel string) string {
	return filepath.Clean(filepath.FromSlash(rel))
}

func confinedAbs(root, rel string) (string, error) {
	abs := filepath.Join(root, filepath.FromSlash(rel))
	relToRoot, err := filepath.Rel(root, abs)
	if err != nil {
		return "", err
	}
	relToRoot = filepath.ToSlash(relToRoot)
	if strings.HasPrefix(relToRoot, "../") || relToRoot == ".." {
		return "", fmt.Errorf("path %q escapes agent root", rel)
	}
	return abs, nil
}

func pathAllowed(scope Scope, rel string) bool {
	rel = filepath.ToSlash(rel)
	switch rel {
	case "memory/MEMORY.md", "memory/STATE.md", "memory/REVIEW.md",
		"notes/agents.md", "notes/relationship-map.md":
		return true
	}
	if strings.HasPrefix(rel, "memory/daily/") && strings.HasSuffix(rel, ".md") && !strings.Contains(rel[len("memory/daily/"):], "/") {
		return true
	}
	parts := strings.Split(rel, "/")
	if len(parts) == 3 && parts[1] != "" && safeID(parts[1]) {
		switch parts[0] {
		case "users":
			return scope.MemberID != "" && parts[1] == scope.MemberID && (parts[2] == "USER.md" || parts[2] == "RELATIONSHIP.md")
		case "projects":
			return scope.ProjectID != "" && parts[1] == scope.ProjectID && (parts[2] == "MEMORY.md" || parts[2] == "STATE.md" || parts[2] == "DECISIONS.md")
		case "channels":
			return scope.ChannelID != "" && parts[1] == scope.ChannelID && parts[2] == "CONTEXT.md"
		}
	}
	return false
}

func safeID(id string) bool {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return false
	}
	return !strings.Contains(id, "..")
}

func containsAllQueryTokens(query, text string) bool {
	q := tokens(query)
	if len(q) == 0 {
		return false
	}
	hay := strings.ToLower(text)
	for _, tok := range q {
		if !strings.Contains(hay, tok) {
			return false
		}
	}
	return true
}

func lexicalSimilarity(a, b string) float64 {
	wa := weightedTokens(a)
	wb := weightedTokens(b)
	if len(wa) == 0 || len(wb) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for token, av := range wa {
		dot += av * wb[token]
		normA += av * av
	}
	for _, bv := range wb {
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func weightedTokens(s string) map[string]float64 {
	weights := map[string]float64{}
	for _, token := range tokens(s) {
		weights[token]++
	}
	return weights
}

func tokens(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	matches := tokenRE.FindAllString(s, -1)
	out := make([]string, 0, len(matches))
	for _, token := range matches {
		token = strings.TrimFunc(token, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
		if token == "" || len(token) < minTokenLen || stopWords[token] {
			continue
		}
		out = append(out, token)
	}
	return out
}

func clipSnippet(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= maxSnippetRunes {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:maxSnippetRunes])) + "…"
}

func roundScore(score float64) float64 {
	return math.Round(score*1000) / 1000
}
