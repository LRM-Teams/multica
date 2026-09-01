// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
)

// The research graph is a named workspace scope (scope design §3, 2026-08-31
// revision): exactly one per workspace, resolved explicitly by the research
// exporter, maintenance rounds, and federated recall — never a fallback. A
// workspace that is not in graph memory mode resolves no research graph.
func TestResearchGraphDirResolvesNamedWorkspaceScope(t *testing.T) {
	t.Setenv("MULTICA_WORKSPACES_ROOT", t.TempDir())
	t.Setenv("MULTICA_MEMORY_TYPE", "graph")
	wsID := util.MustParseUUID(uuid.NewString())

	dir, err := researchGraphDir(context.Background(), nil, wsID)
	if err != nil {
		t.Fatal(err)
	}
	ws := util.UUIDToString(wsID)
	if !strings.HasSuffix(dir, filepath.Join("memory_graph", "research", ws)) {
		t.Errorf("research dir = %q, want suffix memory_graph/research/%s", dir, ws)
	}
}

func TestResearchGraphDirFailsClosedForLegacyWorkspace(t *testing.T) {
	t.Setenv("MULTICA_WORKSPACES_ROOT", t.TempDir())
	t.Setenv("MULTICA_MEMORY_TYPE", "")
	wsID := util.MustParseUUID(uuid.NewString())

	if _, err := researchGraphDir(context.Background(), nil, wsID); err == nil {
		t.Error("researchGraphDir must fail closed for a legacy workspace")
	}
}

func TestResearchGraphDirFailsClosedForInvalidWorkspace(t *testing.T) {
	t.Setenv("MULTICA_WORKSPACES_ROOT", t.TempDir())
	t.Setenv("MULTICA_MEMORY_TYPE", "graph")

	if _, err := researchGraphDir(context.Background(), nil, pgtype.UUID{}); err == nil {
		t.Error("researchGraphDir must fail closed for an invalid workspace id")
	}
}
