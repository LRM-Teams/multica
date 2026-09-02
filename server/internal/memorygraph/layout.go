package memorygraph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// GraphDirKind is the physical graph kind under one workspace (spec §3).
type GraphDirKind string

const (
	GraphDirKindProject  GraphDirKind = "project"
	GraphDirKindChannel  GraphDirKind = "channel"
	GraphDirKindResearch GraphDirKind = "research"
)

// graphIdentityFile is the immutable identity marker inside every graph
// root. A store must verify it before reading or writing; mismatch fails
// closed (spec §3, §12 graph_identity_mismatch).
const graphIdentityFile = ".graph_identity.json"

// GraphIdentity is the immutable identity of one physical graph.
type GraphIdentity struct {
	WorkspaceID string `json:"workspace_id"`
	Kind        string `json:"kind"`     // "project" | "channel" | "research"
	OwnerID     string `json:"owner_id"` // project-id | channel-id | workspace-id (research)
}

func (k GraphDirKind) subdir() (string, error) {
	switch k {
	case GraphDirKindProject:
		return "projects", nil
	case GraphDirKindChannel:
		return "channels", nil
	case GraphDirKindResearch:
		return "research", nil
	default:
		return "", fmt.Errorf("graph_identity_mismatch: unknown graph dir kind %q", string(k))
	}
}

// DirForScope returns the canonical graph directory for one scope without
// creating it: <root>/<ws>/memory_graph/projects/<pid>,
// <root>/<ws>/memory_graph/channels/<cid>, or
// <root>/<ws>/memory_graph/research/<ws>. All IDs must be UUIDs; anything
// else fails closed. The research graph is the one sanctioned workspace-level
// scope (scope design §3, 2026-08-31 revision): a named scope whose owner is
// the workspace itself, never a fallback for unresolved project/channel
// targets.
func DirForScope(workspacesRoot, workspaceID string, kind GraphDirKind, ownerID string) (string, error) {
	if _, err := uuid.Parse(strings.TrimSpace(workspaceID)); err != nil {
		return "", fmt.Errorf("graph_scope_unresolved: invalid workspace id %q", workspaceID)
	}
	if _, err := uuid.Parse(strings.TrimSpace(ownerID)); err != nil {
		return "", fmt.Errorf("graph_scope_unresolved: invalid graph owner id %q", ownerID)
	}
	if kind == GraphDirKindResearch && ownerID != workspaceID {
		return "", fmt.Errorf("graph_scope_unresolved: research graph owner must be the workspace itself, got owner %q for workspace %q", ownerID, workspaceID)
	}
	sub, err := kind.subdir()
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(workspacesRoot)
	if err != nil {
		return "", fmt.Errorf("graph_store_unavailable: %w", err)
	}
	dir := filepath.Join(root, workspaceID, "memory_graph", sub, ownerID)
	if rel, err := filepath.Rel(root, dir); err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("graph_scope_unresolved: graph dir escapes workspaces root")
	}
	return dir, nil
}

// EnsureScopedDir creates the canonical graph directory on first write and
// stamps its immutable identity. An existing directory must carry the same
// identity; mismatch fails closed.
func EnsureScopedDir(workspacesRoot, workspaceID string, kind GraphDirKind, ownerID string) (string, error) {
	dir, err := DirForScope(workspacesRoot, workspaceID, kind, ownerID)
	if err != nil {
		return "", err
	}
	want := GraphIdentity{WorkspaceID: workspaceID, Kind: string(kind), OwnerID: ownerID}
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		if err := VerifyGraphIdentity(dir, want); err != nil {
			return "", err
		}
		return dir, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("graph_store_unavailable: mkdir %s: %w", dir, err)
	}
	body, err := json.Marshal(want)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, graphIdentityFile), body, 0o644); err != nil {
		return "", fmt.Errorf("graph_store_unavailable: write identity: %w", err)
	}
	return dir, nil
}

// ReadGraphIdentity loads the identity marker of one graph directory.
func ReadGraphIdentity(dir string) (GraphIdentity, error) {
	body, err := os.ReadFile(filepath.Join(dir, graphIdentityFile))
	if err != nil {
		return GraphIdentity{}, fmt.Errorf("graph_identity_mismatch: read identity: %w", err)
	}
	var id GraphIdentity
	if err := json.Unmarshal(body, &id); err != nil {
		return GraphIdentity{}, fmt.Errorf("graph_identity_mismatch: parse identity: %w", err)
	}
	if _, err := uuid.Parse(id.WorkspaceID); err != nil {
		return GraphIdentity{}, fmt.Errorf("graph_identity_mismatch: invalid workspace id in identity")
	}
	if _, err := uuid.Parse(id.OwnerID); err != nil {
		return GraphIdentity{}, fmt.Errorf("graph_identity_mismatch: invalid owner id in identity")
	}
	if id.Kind != string(GraphDirKindProject) && id.Kind != string(GraphDirKindChannel) && id.Kind != string(GraphDirKindResearch) {
		return GraphIdentity{}, fmt.Errorf("graph_identity_mismatch: invalid kind %q", id.Kind)
	}
	if id.Kind == string(GraphDirKindResearch) && id.OwnerID != id.WorkspaceID {
		return GraphIdentity{}, fmt.Errorf("graph_identity_mismatch: research graph owner %q must equal workspace %q", id.OwnerID, id.WorkspaceID)
	}
	return id, nil
}

// VerifyGraphIdentity fails closed unless the directory's identity marker
// exactly matches want.
func VerifyGraphIdentity(dir string, want GraphIdentity) error {
	got, err := ReadGraphIdentity(dir)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("graph_identity_mismatch: dir %s identity %+v, want %+v", dir, got, want)
	}
	return nil
}
