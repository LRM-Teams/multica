package computer

import (
	"path"
	"strings"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

var workJournalDeniedDirNames = map[string]struct{}{
	"node_modules": {},
	".next":        {},
	"dist":         {},
	"build":        {},
	"target":       {},
	"vendor":       {},
	"__pycache__":  {},
	".cache":       {},
	".ssh":         {},
	".gnupg":       {},
	".git":         {},
}

// WorkJournalDeniedRepoRoot reports whether a discovered git root sits under a
// denylisted directory (noise or secrets). The repo itself is skipped; this
// does not inspect files inside an allowed root.
func WorkJournalDeniedRepoRoot(root string) bool {
	return workJournalPathHasDeniedDir(root)
}

// WorkJournalDeniedDirtyPath reports whether one dirty path (relative to a
// repo root, or absolute) is noise or a secret. Callers drop the entry and
// keep the rest of the repo.
func WorkJournalDeniedDirtyPath(relPath string) bool {
	cleaned := workJournalCleanPath(relPath)
	if cleaned == "" || cleaned == "." {
		return false
	}
	if workJournalPathHasDeniedDir(cleaned) {
		return true
	}
	return workJournalDeniedSecretBase(path.Base(cleaned))
}

// FilterWorkJournalDirtyPaths drops denied dirty entries and preserves the
// rest. An empty result means the repo had no reportable dirty paths, not
// that the repo should be omitted.
func FilterWorkJournalDirtyPaths(paths []protocol.WorkDigestDirtyPath) []protocol.WorkDigestDirtyPath {
	if len(paths) == 0 {
		return paths
	}
	out := make([]protocol.WorkDigestDirtyPath, 0, len(paths))
	for _, item := range paths {
		if WorkJournalDeniedDirtyPath(item.Path) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func workJournalCleanPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")
	return path.Clean(p)
}

func workJournalPathHasDeniedDir(p string) bool {
	cleaned := workJournalCleanPath(p)
	if cleaned == "" || cleaned == "." {
		return false
	}
	cleaned = strings.TrimPrefix(cleaned, "/")
	for _, part := range strings.Split(cleaned, "/") {
		if part == "" || part == "." {
			continue
		}
		if _, denied := workJournalDeniedDirNames[strings.ToLower(part)]; denied {
			return true
		}
	}
	return false
}

func workJournalDeniedSecretBase(base string) bool {
	base = strings.ToLower(strings.TrimSpace(base))
	if base == "" {
		return false
	}
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return true
	}
	if base == "id_rsa" || strings.HasPrefix(base, "id_rsa.") {
		return true
	}
	if strings.HasSuffix(base, ".pem") {
		return true
	}
	return base == "credentials.json"
}
