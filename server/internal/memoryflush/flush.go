// Package memoryflush records a fail-open missed-write signal immediately
// before provider context compaction. Compaction must never be blocked by
// this package: every filesystem error is swallowed into Result.Error.
package memoryflush

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ActionCompactionFlush = "compaction_flush"
	snapshotRel           = ".multica/memory-write-hashes.json"
	signalRel             = "sync_queue/memory-signal.jsonl"
)

// Result is the observational outcome of a pre-compaction flush.
type Result struct {
	AgentRoot     string `json:"agent_root"`
	WroteSignal   bool   `json:"wrote_signal"`
	DurableWrites int    `json:"durable_writes"`
	Error         string `json:"error,omitempty"`
}

type memoryWriteSnapshot struct {
	Files map[string]string `json:"files"`
}

type signalLine struct {
	Action    string `json:"action"`
	Kind      string `json:"kind"`
	Summary   string `json:"summary"`
	CreatedAt string `json:"created_at"`
}

// BeforeCompaction compares the durable memory snapshot to current files.
// If nothing durable changed since the last snapshot, it appends one
// compaction_flush signal so L2 can recover facts that only lived in
// conversation. Failures never surface as an error return.
func BeforeCompaction(agentRoot string) (result Result) {
	result.AgentRoot = strings.TrimSpace(agentRoot)
	if result.AgentRoot == "" {
		return result
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			result.Error = "panic during memory flush"
		}
	}()
	if err := run(agentRoot, &result); err != nil && result.Error == "" {
		result.Error = err.Error()
	}
	return result
}

func run(agentRoot string, result *Result) error {
	prior, err := loadSnapshot(agentRoot)
	if err != nil {
		return err
	}
	changes, err := diffDurableWrites(agentRoot, prior)
	if err != nil {
		return err
	}
	result.DurableWrites = len(changes)
	if result.DurableWrites > 0 {
		return nil
	}
	if alreadySignaled(agentRoot) {
		return nil
	}
	return appendSignal(agentRoot, result)
}

func loadSnapshot(agentRoot string) (memoryWriteSnapshot, error) {
	snap := memoryWriteSnapshot{Files: map[string]string{}}
	data, err := os.ReadFile(filepath.Join(agentRoot, filepath.FromSlash(snapshotRel)))
	if err != nil {
		if os.IsNotExist(err) {
			return snap, nil
		}
		return snap, err
	}
	if json.Unmarshal(data, &snap) != nil || snap.Files == nil {
		snap.Files = map[string]string{}
	}
	return snap, nil
}

func diffDurableWrites(agentRoot string, prior memoryWriteSnapshot) ([]string, error) {
	var changed []string
	err := filepath.WalkDir(agentRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != agentRoot {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(agentRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !isDurableMemoryPath(rel) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		if prior.Files[rel] != hash {
			changed = append(changed, rel)
		}
		return nil
	})
	return changed, err
}

func isDurableMemoryPath(rel string) bool {
	rel = filepath.ToSlash(rel)
	base := filepath.Base(rel)
	parts := strings.Split(rel, "/")
	switch {
	case rel == "memory/MEMORY.md", rel == "memory/STATE.md":
		return true
	case strings.HasPrefix(rel, "memory/daily/") && strings.HasSuffix(base, ".md"):
		return true
	case len(parts) == 3 && parts[0] == "users" && (base == "USER.md" || base == "RELATIONSHIP.md"):
		return true
	case len(parts) == 3 && parts[0] == "projects" && (base == "MEMORY.md" || base == "STATE.md" || base == "DECISIONS.md"):
		return true
	case len(parts) == 3 && parts[0] == "channels" && base == "CONTEXT.md":
		return true
	case rel == "notes/agents.md", rel == "notes/relationship-map.md":
		return true
	default:
		return false
	}
}

func alreadySignaled(agentRoot string) bool {
	data, err := os.ReadFile(filepath.Join(agentRoot, filepath.FromSlash(signalRel)))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item map[string]any
		if json.Unmarshal([]byte(line), &item) != nil {
			continue
		}
		action, _ := item["action"].(string)
		if action == ActionCompactionFlush {
			return true
		}
	}
	return false
}

func appendSignal(agentRoot string, result *Result) error {
	path := filepath.Join(agentRoot, filepath.FromSlash(signalRel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.Marshal(signalLine{
		Action:    ActionCompactionFlush,
		Kind:      "missed",
		Summary:   "context compaction ran without a durable memory write; recover facts from the session checkpoint",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(payload, '\n')); err != nil {
		return err
	}
	result.WroteSignal = true
	return nil
}
