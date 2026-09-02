// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
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

// researchGraphDir resolves the workspace research graph directory for the
// unification spec (§4.1), creating it with its immutable identity marker on
// first use. It is a named workspace scope: exactly one per workspace, owned
// by the workspace itself, resolved explicitly by the research exporter,
// maintenance rounds, and federated recall — never a fallback. A workspace
// that is not in graph memory mode resolves no research graph (fail closed).
// Identity stamping matters because every identity-verified reader (federated
// recall, the Director background provider) fails closed on a marker-less
// directory, and the exporter is usually the graph's first creator.
func researchGraphDir(ctx context.Context, queries *db.Queries, workspaceID pgtype.UUID) (string, error) {
	if !workspaceID.Valid {
		return "", fmt.Errorf("graph_scope_unresolved: invalid workspace id")
	}
	if resolveGraphMemoryType(ctx, queries, workspaceID, graphMemoryEnvMemoryType()) != "graph" {
		return "", fmt.Errorf("graph_scope_unresolved: workspace is not in graph memory mode")
	}
	root, err := graphMemoryWorkspacesRoot()
	if err != nil {
		return "", err
	}
	ws := util.UUIDToString(workspaceID)
	return memorygraph.EnsureScopedDir(root, ws, memorygraph.GraphDirKindResearch, ws)
}
