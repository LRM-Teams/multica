package daemon

import (
	"fmt"
	"os"
	"path/filepath"
)

// agentWorkspaceWriteQuotaError returns a non-empty user-facing error when a
// write that changes one file from oldSize to newSize bytes under root is
// forbidden by AgentWorkspaceQuotaBytes.
//
// Policy (tasks #94 / #111 — single source of truth for both RPC write and
// seed paths, and the "would this grow?" half of the turn-start gate):
//
//   - quota <= 0 → unlimited (allow)
//   - used < quota → allow
//   - used >= quota && newSize <= oldSize → allow (shrink / same-size recovery)
//   - used >= quota && newSize > oldSize → refuse
//
// Callers that always grow (seed append) pass oldSize=0, newSize>0 when any
// non-empty content would be written, or compute true old/new sizes per file.
func agentWorkspaceWriteQuotaError(root string, quota, oldSize, newSize int64) string {
	if quota <= 0 {
		return ""
	}
	used := dirSize(root)
	if used < quota {
		return ""
	}
	if newSize <= oldSize {
		return ""
	}
	return fmt.Sprintf(
		"agent workspace over capacity: cannot write (workspace uses %d bytes, cap is %d bytes) — a write that shrinks or keeps the same size is still allowed",
		used, quota,
	)
}

// agentWorkspaceAtOrOverCap reports whether root is at/over a positive quota.
// quota <= 0 means unlimited (never over).
func agentWorkspaceAtOrOverCap(root string, quota int64) (used int64, over bool) {
	if quota <= 0 {
		return 0, false
	}
	used = dirSize(root)
	return used, used >= quota
}

func dirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}
