package agentworkspace

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Visibility matches Raft 1.0.16's three layers, with slock names mapped to
// Multica: default-hide dotfiles; never list node_modules; never list or
// enter .aws / .gnupg / .ssh / .multica / .multica-runtime / .multica-*;
// refuse preview of never-visible paths and Raft secret-name patterns.

const WorkspaceNodeModulesName = "node_modules"

var workspaceNeverVisibleHiddenNames = map[string]struct{}{
	".aws":             {},
	".gnupg":           {},
	".ssh":             {},
	".multica":         {},
	".multica-runtime": {},
}

// Raft 1.0.16 WORKSPACE_SECRET_FILE_PATTERNS, applied to each path segment.
var workspaceSecretFilePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^\.env(?:\.|$)`),
	regexp.MustCompile(`(?i)(?:^|[._-])secret(?:s)?(?:[._-]|$)`),
	regexp.MustCompile(`(?i)(?:^|[._-])credential(?:s)?(?:[._-]|$)`),
	regexp.MustCompile(`(?i)(?:^|[._-])token(?:s)?(?:[._-]|$)`),
}

const previewDisabledReason = "Preview is disabled for sensitive workspace files"

func workspacePathParts(relativePath string) []string {
	cleaned := filepath.ToSlash(strings.TrimSpace(relativePath))
	cleaned = strings.Trim(cleaned, "/")
	if cleaned == "" || cleaned == "." {
		return nil
	}
	parts := strings.Split(cleaned, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		out = append(out, part)
	}
	return out
}

// IsNeverVisibleHiddenEntry reports names that stay hidden even when the
// caller asks to include hidden files.
func IsNeverVisibleHiddenEntry(name string) bool {
	if _, ok := workspaceNeverVisibleHiddenNames[name]; ok {
		return true
	}
	return strings.HasPrefix(name, ".multica-")
}

// IsHiddenPath is true when any path segment starts with ".".
func IsHiddenPath(relativePath string) bool {
	for _, part := range workspacePathParts(relativePath) {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// IsNeverVisibleHiddenPath is true when any path segment is never-visible.
func IsNeverVisibleHiddenPath(relativePath string) bool {
	for _, part := range workspacePathParts(relativePath) {
		if IsNeverVisibleHiddenEntry(part) {
			return true
		}
	}
	return false
}

// IsSecretFilePath is true when any path segment matches a Raft secret pattern.
func IsSecretFilePath(filePath string) bool {
	for _, part := range workspacePathParts(filePath) {
		for _, pattern := range workspaceSecretFilePatterns {
			if pattern.MatchString(part) {
				return true
			}
		}
	}
	return false
}

// PreviewDeniedReason is empty when a file may be previewed. Never-visible
// and secret names are refused even if the caller can list hidden files.
func PreviewDeniedReason(filePath string) string {
	if IsNeverVisibleHiddenPath(filePath) || IsSecretFilePath(filePath) {
		return previewDisabledReason
	}
	return ""
}

// ListDirDenied reports whether a directory listing of relativePath should
// return no children (never-visible, or hidden while includeHidden is false).
func ListDirDenied(relativePath string, includeHidden bool) bool {
	if relativePath == "" || relativePath == "." {
		return false
	}
	if IsNeverVisibleHiddenPath(relativePath) {
		return true
	}
	return !includeHidden && IsHiddenPath(relativePath)
}
