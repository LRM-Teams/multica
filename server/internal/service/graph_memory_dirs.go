// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// graphMemoryWorkspacesRoot resolves the workspaces root on the server from
// MULTICA_WORKSPACES_ROOT, defaulting to ~/.multica/workspaces. It mirrors
// the daemon config and the scheduler job (same env contract).
func graphMemoryWorkspacesRoot() (string, error) {
	root := strings.TrimSpace(os.Getenv("MULTICA_WORKSPACES_ROOT"))
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w (set MULTICA_WORKSPACES_ROOT to override)", err)
		}
		root = filepath.Join(home, ".multica", "workspaces")
	}
	return filepath.Abs(root)
}

// graphMemoryDirForWorkspace returns the memory_graph directory serving one
// workspace: the per-workspace <root>/<workspace>/memory_graph when it
// exists, else the root-level default layout <root>/memory_graph (the
// daemon's default GraphMemoryDir). ok is false when neither exists — the
// workspace has no graph memory store and callers skip silently. This
// follows the scheduler job's per-dir discovery (findMemoryGraphDirs).
func graphMemoryDirForWorkspace(root, workspaceID string) (dir string, ok bool) {
	if root == "" {
		return "", false
	}
	if workspaceID != "" {
		perWS := filepath.Join(root, workspaceID, "memory_graph")
		if info, err := os.Stat(perWS); err == nil && info.IsDir() {
			return perWS, true
		}
	}
	def := filepath.Join(root, "memory_graph")
	if info, err := os.Stat(def); err == nil && info.IsDir() {
		return def, true
	}
	return "", false
}

// resolveGraphMemoryType resolves memory_type for one workspace (design
// §1/A4): a valid graph_memory_profile row wins over the process env default
// (MULTICA_MEMORY_TYPE); anything unrecognized falls back to the env
// default, then "legacy". Lookup errors (including a missing row) fail open
// to the env default so a transient DB hiccup never flips a workspace's
// memory pipeline.
func resolveGraphMemoryType(ctx context.Context, queries *db.Queries, workspaceID pgtype.UUID, envType string) string {
	if queries != nil && workspaceID.Valid {
		if profile, err := queries.GetGraphMemoryProfile(ctx, workspaceID); err == nil {
			if profile.MemoryType == "graph" || profile.MemoryType == "legacy" {
				return profile.MemoryType
			}
		}
	}
	if envType == "graph" || envType == "legacy" {
		return envType
	}
	return "legacy"
}

// graphMemoryEnvMemoryType reads the process-level memory_type default.
func graphMemoryEnvMemoryType() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv("MULTICA_MEMORY_TYPE")))
}
