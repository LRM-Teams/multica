// Package memoryorigin stops recall loops: injected prompt text is tainted so
// L1/L2 will not promote it back into durable memory, and restricted cognition
// turns (attention probe / protocol turn) do not enqueue durable candidates.
package memoryorigin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	Owner     = "owner"
	Agent     = "agent"
	Untrusted = "untrusted"
	System    = "system"

	Marker = "<!-- multica-memory injected=true"

	NoticeRel = ".multica/curation-notice.json"
)

// Normalize returns a known origin class, defaulting to agent.
func Normalize(class string) string {
	switch strings.ToLower(strings.TrimSpace(class)) {
	case Owner:
		return Owner
	case Untrusted:
		return Untrusted
	case System:
		return System
	default:
		return Agent
	}
}

// ClassifyScope maps a memory block to an origin class. Graph recall and
// unnamed extras are untrusted; system notices are system; reviewed/local
// files are agent-authored unless marked owner.
func ClassifyScope(scope, name string) string {
	scope = strings.ToLower(strings.TrimSpace(scope))
	name = strings.ToLower(strings.TrimSpace(name))
	if scope == "system" || strings.Contains(name, "curation") {
		return System
	}
	if strings.Contains(name, "graph") || scope == "workspace" {
		return Untrusted
	}
	if scope == "user" || scope == "member" {
		return Owner
	}
	return Agent
}

// Taint prefixes content with a machine-readable injected marker.
func Taint(content, originClass string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if IsInjected(content) {
		return content
	}
	return Marker + " origin_class=" + Normalize(originClass) + " -->\n" + content
}

// IsInjected reports whether text was previously injected into a prompt.
func IsInjected(text string) bool {
	return strings.Contains(text, Marker) || strings.Contains(strings.ToLower(text), "origin: `") && strings.Contains(strings.ToLower(text), "(injected")
}

// SkipLine is true when a Daily/REVIEW line must not become an L2 candidate.
func SkipLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return true
	}
	return IsInjected(line) || strings.HasPrefix(line, "<!-- multica-memory")
}

// SkipDurableCandidates is true for sidecar cognition turns that must not
// produce missed-write / friction / decision candidates.
func SkipDurableCandidates(executionProfile string) bool {
	switch strings.TrimSpace(executionProfile) {
	case "attention_probe", "protocol_turn":
		return true
	default:
		return false
	}
}

// Notice is a one-shot post-curation hint for the next live turn.
type Notice struct {
	UpdatedAt    string `json:"updated_at"`
	ChangedFiles int    `json:"changed_files"`
	Hint         string `json:"hint"`
}

// WriteNotice records that curation changed local files. Fail-open.
func WriteNotice(agentRoot string, changedFiles int) error {
	if strings.TrimSpace(agentRoot) == "" || changedFiles <= 0 {
		return nil
	}
	path := filepath.Join(agentRoot, filepath.FromSlash(NoticeRel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.Marshal(Notice{
		UpdatedAt:    time.Now().UTC().Format(time.RFC3339),
		ChangedFiles: changedFiles,
		Hint:         "Use `multica memory search` / `multica memory get <path>` to read the latest facts. Do not copy injected snapshot text into Daily.",
	})
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(payload, '\n'), 0o644)
}

// ConsumeNotice returns and deletes a pending notice, if any.
func ConsumeNotice(agentRoot string) (Notice, bool) {
	if strings.TrimSpace(agentRoot) == "" {
		return Notice{}, false
	}
	path := filepath.Join(agentRoot, filepath.FromSlash(NoticeRel))
	data, err := os.ReadFile(path)
	if err != nil {
		return Notice{}, false
	}
	_ = os.Remove(path)
	var notice Notice
	if json.Unmarshal(data, &notice) != nil || notice.ChangedFiles <= 0 {
		return Notice{}, false
	}
	return notice, true
}
