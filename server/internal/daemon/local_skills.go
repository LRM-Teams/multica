package daemon

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
	agentskills "github.com/multica-ai/multica/server/internal/daemon/agent/skills"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type runtimeLocalSkillSummary struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SourcePath  string `json:"source_path"`
	Provider    string `json:"provider"`
	FileCount   int    `json:"file_count"`
}

type runtimeLocalSkillBundle struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Content     string          `json:"content"`
	SourcePath  string          `json:"source_path"`
	Provider    string          `json:"provider"`
	Files       []SkillFileData `json:"files,omitempty"`
}

func (d *Daemon) handleAgentSkillsList(req protocol.AgentSkillsListPayload, writes chan<- []byte) {
	resp := protocol.AgentSkillsListResultPayload{AgentID: req.AgentID, RequestID: req.RequestID, Global: []protocol.AgentSkillSummary{}, Workspace: []protocol.AgentSkillSummary{}}
	d.mu.Lock()
	runtime, runtimeOK := d.runtimeIndex[req.Runtime]
	d.mu.Unlock()
	if !runtimeOK {
		d.sendDaemonFrame(protocol.EventAgentSkillsListResult, resp, req.RequestID, writes)
		return
	}
	global, _, err := listRuntimeLocalSkills(runtime.Provider)
	if err != nil {
		d.sendDaemonFrame(protocol.EventAgentSkillsListResult, resp, req.RequestID, writes)
		return
	}
	for _, item := range global {
		resp.Global = append(resp.Global, protocol.AgentSkillSummary{
			Name: item.Name, Description: item.Description, Path: item.SourcePath, Source: "global",
		})
	}
	if req.AgentID != "" {
		root := agentworkspace.Root(d.cfg.WorkspacesRoot, runtime.WorkspaceID, req.AgentID)
		workspaceSkills, listErr := listWorkspaceLocalSkills(runtime.Provider, root)
		if listErr != nil {
			d.logger.Debug("workspace skill discovery failed", "agent_id", req.AgentID, "error", listErr)
		} else {
			for _, item := range workspaceSkills {
				resp.Workspace = append(resp.Workspace, protocol.AgentSkillSummary{
					Name: item.Name, Description: item.Description, Path: item.SourcePath, Source: "workspace",
				})
			}
		}
	}
	d.sendDaemonFrame(protocol.EventAgentSkillsListResult, resp, req.RequestID, writes)
}

func listWorkspaceLocalSkills(provider, root string) ([]runtimeLocalSkillSummary, error) {
	catalog, err := agentskills.WorkspaceCatalog(provider, root)
	if err != nil {
		return nil, err
	}
	items, err := catalog.List()
	return localSkillSummaries(provider, items), err
}

func runtimeLocalSkillCatalog(provider string) (agentskills.LocalCatalog, bool, error) {
	home, err := userHomeDir()
	if err != nil {
		return agentskills.LocalCatalog{}, false, fmt.Errorf("resolve user home: %w", err)
	}
	return agentskills.GlobalCatalog(provider, home)
}

func listRuntimeLocalSkills(provider string) ([]runtimeLocalSkillSummary, bool, error) {
	catalog, supported, err := runtimeLocalSkillCatalog(provider)
	if err != nil || !supported {
		return nil, supported, err
	}
	items, err := catalog.List()
	return localSkillSummaries(provider, items), true, err
}

func listLocalSkillsFromRoot(provider, root string) ([]runtimeLocalSkillSummary, bool, error) {
	items, err := agentskills.NewLocalCatalog(root).List()
	return localSkillSummaries(provider, items), true, err
}

func localSkillSummaries(provider string, items []agentskills.LocalSummary) []runtimeLocalSkillSummary {
	result := make([]runtimeLocalSkillSummary, 0, len(items))
	for _, item := range items {
		result = append(result, runtimeLocalSkillSummary{
			Key: item.Key, Name: item.Name, Description: item.Description,
			SourcePath: relativizeHomePath(item.Path), Provider: provider, FileCount: item.FileCount,
		})
	}
	return result
}

func loadRuntimeLocalSkillBundle(provider, key string) (*runtimeLocalSkillBundle, bool, error) {
	catalog, supported, err := runtimeLocalSkillCatalog(provider)
	if err != nil || !supported {
		return nil, supported, err
	}
	return loadLocalSkillBundle(provider, catalog, key)
}

func loadLocalSkillBundleFromRoot(provider, root, key string) (*runtimeLocalSkillBundle, bool, error) {
	return loadLocalSkillBundle(provider, agentskills.NewLocalCatalog(root), key)
}

func loadLocalSkillBundle(provider string, catalog agentskills.LocalCatalog, key string) (*runtimeLocalSkillBundle, bool, error) {
	bundle, err := catalog.Load(key)
	if err != nil {
		return nil, true, err
	}
	files := make([]SkillFileData, 0, len(bundle.Files))
	for _, file := range bundle.Files {
		files = append(files, SkillFileData{Path: file.Path, Content: file.Content})
	}
	return &runtimeLocalSkillBundle{
		Name: bundle.Name, Description: bundle.Description, Content: bundle.Content,
		SourcePath: relativizeHomePath(bundle.Path), Provider: provider, Files: files,
	}, true, nil
}

func relativizeHomePath(path string) string {
	home, err := userHomeDir()
	if err != nil {
		return filepath.ToSlash(path)
	}
	if path == home {
		return "~"
	}
	prefix := home + string(filepath.Separator)
	if strings.HasPrefix(path, prefix) {
		return filepath.ToSlash("~" + string(filepath.Separator) + strings.TrimPrefix(path, prefix))
	}
	if rel, err := filepath.Rel(home, path); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !strings.HasPrefix(rel, "../") {
		return "~/" + filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}
