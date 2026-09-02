package computer

import (
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	periodBriefCollectRootsFileName = "period-brief-collect-roots.json"
	maxPeriodBriefCollectRoots      = 16
)

type periodBriefCollectRootsSetting struct {
	Roots []string `json:"roots"`
}

// PeriodBriefCollectRootsPath is the Computer-local Period Work collect-root
// file under the resident root (~/.multica/computer/).
func PeriodBriefCollectRootsPath(residentRoot string) string {
	residentRoot = strings.TrimSpace(residentRoot)
	if residentRoot == "" {
		return ""
	}
	return filepath.Join(residentRoot, periodBriefCollectRootsFileName)
}

// NormalizePeriodBriefCollectRoots trims, dedupes, and rejects filesystem
// root, the user's home, and denylisted secret/noise paths. An empty result
// is unset (heuristic SCAN_ROOTS). Paths are stored as typed; ~ / $HOME are
// expanded on the collecting OS, not here.
func NormalizePeriodBriefCollectRoots(roots []string, home string) ([]string, error) {
	home = normalizeCollectRootCompare(home)
	seen := make(map[string]struct{}, len(roots))
	out := make([]string, 0, len(roots))
	for _, raw := range roots {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if strings.ContainsAny(trimmed, "\n\r") {
			return nil, errors.New("collect root must be a single path")
		}
		if err := rejectPeriodBriefCollectRoot(trimmed, home); err != nil {
			return nil, err
		}
		key := expandCollectRootCompare(trimmed, home)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
		if len(out) > maxPeriodBriefCollectRoots {
			return nil, errors.New("at most 16 collect roots")
		}
	}
	return out, nil
}

func rejectPeriodBriefCollectRoot(root, home string) error {
	cleaned := strings.ReplaceAll(strings.TrimSpace(root), "\\", "/")
	cleaned = path.Clean(cleaned)
	compare := expandCollectRootCompare(root, home)
	switch normalizeCollectRootCompare(root) {
	case "/", "~", "$home", "${home}", "%userprofile%":
		return errors.New("collect root cannot be the filesystem or home directory")
	}
	if home != "" && compare == normalizeCollectRootCompare(home) {
		return errors.New("collect root cannot be the filesystem or home directory")
	}
	base := strings.ToLower(path.Base(cleaned))
	switch base {
	case ".ssh", ".gnupg", ".aws", ".multica", ".cache", "appdata", "library", "downloads", "下载":
		return errors.New("collect root is denylisted")
	}
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return errors.New("collect root is denylisted")
	}
	for _, part := range strings.Split(strings.TrimPrefix(cleaned, "/"), "/") {
		switch strings.ToLower(part) {
		case ".ssh", ".gnupg", ".aws", ".multica", "appdata":
			return errors.New("collect root is denylisted")
		}
	}
	if len(cleaned) == 2 && cleaned[1] == ':' {
		return errors.New("collect root cannot be the filesystem or home directory")
	}
	return nil
}

func normalizeCollectRootCompare(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\\", "/")
	switch {
	case value == "~" || strings.EqualFold(value, "$HOME") || strings.EqualFold(value, "${HOME}") || strings.EqualFold(value, "%USERPROFILE%"):
		return "~"
	case strings.HasPrefix(value, "~/"):
		value = "$home/" + strings.TrimPrefix(value, "~/")
	case strings.HasPrefix(strings.ToLower(value), "$home/"):
		value = "$home/" + value[len("$home/"):]
	case strings.HasPrefix(strings.ToLower(value), "${home}/"):
		value = "$home/" + value[len("${home}/"):]
	}
	if value != "/" {
		value = strings.TrimRight(value, "/")
	}
	return strings.ToLower(path.Clean(value))
}

func expandCollectRootCompare(value, home string) string {
	compare := normalizeCollectRootCompare(value)
	home = normalizeCollectRootCompare(home)
	if home == "" || home == "." || home == "/" {
		return compare
	}
	if compare == "~" {
		return home
	}
	if strings.HasPrefix(compare, "$home/") {
		return path.Clean(home + "/" + strings.TrimPrefix(compare, "$home/"))
	}
	return compare
}

// ReadPeriodBriefCollectRoots returns the declared collect roots. A missing
// or empty file is unset (heuristic SCAN_ROOTS), not an error.
func ReadPeriodBriefCollectRoots(residentRoot string) ([]string, error) {
	pathName := PeriodBriefCollectRootsPath(residentRoot)
	if pathName == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(pathName)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var setting periodBriefCollectRootsSetting
	if err := json.Unmarshal(raw, &setting); err != nil {
		return nil, err
	}
	return NormalizePeriodBriefCollectRoots(setting.Roots, "")
}

// WritePeriodBriefCollectRoots persists declared collect roots. An empty
// list clears the override so collectors fall back to heuristic SCAN_ROOTS.
func WritePeriodBriefCollectRoots(residentRoot string, roots []string, home string) error {
	normalized, err := NormalizePeriodBriefCollectRoots(roots, home)
	if err != nil {
		return err
	}
	pathName := PeriodBriefCollectRootsPath(residentRoot)
	if pathName == "" {
		return errors.New("Computer resident root is required")
	}
	if err := os.MkdirAll(filepath.Dir(pathName), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(periodBriefCollectRootsSetting{Roots: normalized})
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(pathName), ".period-brief-collect-roots-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, pathName)
}

// PeriodBriefCollectRoots reports the in-memory declared collect roots.
func (computerCore *ComputerCore) PeriodBriefCollectRoots() []string {
	if computerCore == nil {
		return nil
	}
	computerCore.workJournalMu.Lock()
	defer computerCore.workJournalMu.Unlock()
	out := make([]string, len(computerCore.periodBriefCollectRoots))
	copy(out, computerCore.periodBriefCollectRoots)
	return out
}

// SetPeriodBriefCollectRoots writes the Computer-local collect-root file.
func (computerCore *ComputerCore) SetPeriodBriefCollectRoots(roots []string) error {
	if computerCore == nil {
		return errors.New("ComputerCore is unavailable")
	}
	if err := WritePeriodBriefCollectRoots(computerCore.workJournalRoot, roots, computerCore.workJournalHome); err != nil {
		return err
	}
	normalized, err := NormalizePeriodBriefCollectRoots(roots, computerCore.workJournalHome)
	if err != nil {
		return err
	}
	computerCore.workJournalMu.Lock()
	computerCore.periodBriefCollectRoots = normalized
	computerCore.workJournalMu.Unlock()
	return nil
}

// ApplyPeriodBriefCollectRoots is the Computer-local get/set used by the
// WorkspaceDaemon control callback. Set=false returns the current file.
func (computerCore *ComputerCore) ApplyPeriodBriefCollectRoots(command protocol.ComputerCollectRootsPayload) ([]string, error) {
	if computerCore == nil {
		return nil, errors.New("ComputerCore is unavailable")
	}
	if command.Set {
		if err := computerCore.SetPeriodBriefCollectRoots(command.Roots); err != nil {
			return nil, err
		}
	}
	return computerCore.PeriodBriefCollectRoots(), nil
}

func (computerCore *ComputerCore) loadPeriodBriefCollectRoots() {
	if computerCore == nil {
		return
	}
	roots, err := ReadPeriodBriefCollectRoots(computerCore.workJournalRoot)
	if err != nil {
		return
	}
	computerCore.workJournalMu.Lock()
	computerCore.periodBriefCollectRoots = roots
	computerCore.workJournalMu.Unlock()
}

// FormatPeriodBriefCollectRootsPrint is the collector-recipe wire form:
// space-separated paths, or empty when unset.
func FormatPeriodBriefCollectRootsPrint(roots []string) string {
	return strings.Join(roots, " ")
}
