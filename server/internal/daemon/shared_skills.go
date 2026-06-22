package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (d *Daemon) sharedSkillsSyncLoop(ctx context.Context) {
	interval := d.cfg.SharedSkillsSyncInterval
	if interval <= 0 {
		return
	}
	d.syncSharedSkillsOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.syncSharedSkillsOnce(ctx)
		}
	}
}

func (d *Daemon) syncSharedSkillsOnce(ctx context.Context) {
	if !d.ready.Load() {
		return
	}
	for _, rt := range d.sharedSkillSyncRuntimes() {
		if err := d.syncSharedSkillsForRuntime(ctx, rt); err != nil && ctx.Err() == nil {
			d.logger.Warn("shared skills sync failed", "runtime_id", rt.ID, "provider", rt.Provider, "error", err)
		}
	}
}

// sharedSkillSyncRuntimes returns one stable online runtime per workspace so
// workspace-level skills are synced exactly once per poll.
func (d *Daemon) sharedSkillSyncRuntimes() []Runtime {
	d.mu.Lock()
	defer d.mu.Unlock()

	workspaceIDs := make([]string, 0, len(d.workspaces))
	for id := range d.workspaces {
		workspaceIDs = append(workspaceIDs, id)
	}
	sort.Strings(workspaceIDs)

	runtimes := make([]Runtime, 0, len(workspaceIDs))
	for _, wsID := range workspaceIDs {
		ws := d.workspaces[wsID]
		runtimeIDs := append([]string(nil), ws.runtimeIDs...)
		sort.Strings(runtimeIDs)
		for _, id := range runtimeIDs {
			rt, ok := d.runtimeIndex[id]
			if !ok || rt.Status != "online" {
				continue
			}
			runtimes = append(runtimes, rt)
			break
		}
	}
	return runtimes
}

func sharedSkillScanRoot(cfg Config, provider string) (string, bool) {
	if dir := strings.TrimSpace(cfg.SharedSkillsDir); dir != "" {
		return dir, true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	switch provider {
	case "pi":
		return filepath.Join(home, ".pi", "share", "skills"), true
	default:
		return "", false
	}
}

func agentSharedSkillScanBase(cfg Config, provider string) (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	switch provider {
	case "pi":
		return filepath.Join(home, "multica_workspaces", "pi_demo_workspace", ".pi", "agents"), true
	default:
		return "", false
	}
}

func (d *Daemon) syncSharedSkillsForRuntime(ctx context.Context, rt Runtime) error {
	scanRoot, ok := sharedSkillScanRoot(d.cfg, rt.Provider)
	if ok {
		if err := d.syncWorkspaceSharedSkillsForRuntime(ctx, rt, scanRoot); err != nil {
			return err
		}
	}
	return d.syncAgentSharedSkillsForRuntime(ctx, rt)
}

func (d *Daemon) syncWorkspaceSharedSkillsForRuntime(ctx context.Context, rt Runtime, scanRoot string) error {
	if _, err := os.Stat(scanRoot); err != nil {
		if !os.IsNotExist(err) {
			d.logger.Warn("shared skills root unavailable", "path", scanRoot, "provider", rt.Provider, "error", err)
		}
		return nil
	}

	summaries, _, err := listLocalSkillsFromRoot(rt.Provider, scanRoot)
	if err != nil {
		return err
	}

	presentKeys := make([]string, 0, len(summaries))
	bundles := make([]SharedSkillBundle, 0, len(summaries))
	activeCacheKeys := make(map[string]struct{}, len(summaries))

	d.sharedSkillScanMu.Lock()
	defer d.sharedSkillScanMu.Unlock()

	for _, summary := range summaries {
		presentKeys = append(presentKeys, summary.Key)
		skillDir := filepath.Join(scanRoot, filepath.FromSlash(summary.Key))
		fingerprint, err := localSkillScanFingerprint(skillDir)
		if err != nil {
			d.logger.Warn("shared skill fingerprint skipped", "key", summary.Key, "error", err)
			continue
		}
		cacheKey := scanRoot + "\x00" + summary.Key
		activeCacheKeys[cacheKey] = struct{}{}
		if d.sharedSkillScanCache[cacheKey] == fingerprint {
			continue
		}

		bundle, _, err := loadLocalSkillBundleFromRoot(rt.Provider, scanRoot, summary.Key)
		if err != nil {
			d.logger.Warn("shared skill bundle skipped", "key", summary.Key, "error", err)
			continue
		}
		files := make([]SkillFileData, len(bundle.Files))
		copy(files, bundle.Files)
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		d.sharedSkillScanCache[cacheKey] = fingerprint
		bundles = append(bundles, SharedSkillBundle{
			Key:         summary.Key,
			Name:        bundle.Name,
			Description: bundle.Description,
			Content:     bundle.Content,
			SourcePath:  bundle.SourcePath,
			Provider:    rt.Provider,
			ContentHash: sharedSkillHash(bundle.Content, files),
			Files:       files,
		})
	}

	for cacheKey := range d.sharedSkillScanCache {
		if !strings.HasPrefix(cacheKey, scanRoot+"\x00") {
			continue
		}
		if _, active := activeCacheKeys[cacheKey]; !active {
			delete(d.sharedSkillScanCache, cacheKey)
		}
	}

	result, err := d.client.SyncSharedSkills(ctx, rt.ID, SharedSkillSyncPayload{
		Skills:      bundles,
		PresentKeys: presentKeys,
	})
	if err != nil {
		return err
	}
	if len(result.Conflicts) > 0 {
		for _, conflict := range result.Conflicts {
			d.logger.Warn("shared skill sync conflict",
				"runtime_id", rt.ID,
				"key", conflict.Key,
				"name", conflict.Name,
				"skill_id", conflict.Skill,
				"reason", conflict.Reason,
			)
		}
	}
	if len(result.Errors) > 0 {
		for _, item := range result.Errors {
			d.logger.Warn("shared skill sync item failed",
				"runtime_id", rt.ID,
				"key", item.Key,
				"name", item.Name,
				"error", item.Error,
			)
		}
	}
	d.logger.Debug("shared skills synced",
		"runtime_id", rt.ID,
		"scan_root", scanRoot,
		"created", result.Created,
		"updated", result.Updated,
		"unchanged", result.Unchanged,
		"deleted", result.Deleted,
		"conflicts", len(result.Conflicts),
		"errors", len(result.Errors),
	)
	return nil
}

func (d *Daemon) syncAgentSharedSkillsForRuntime(ctx context.Context, rt Runtime) error {
	base, ok := agentSharedSkillScanBase(d.cfg, rt.Provider)
	if !ok {
		return nil
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		if !os.IsNotExist(err) {
			d.logger.Warn("agent shared skills root unavailable", "path", base, "provider", rt.Provider, "error", err)
		}
		return nil
	}

	payload := AgentSharedSkillSyncPayload{Agents: make([]AgentSharedSkillBundleSet, 0, len(entries))}
	for _, entry := range entries {
		if !entry.IsDir() || isIgnoredLocalSkillEntry(entry.Name()) {
			continue
		}
		agentID := entry.Name()
		scanRoot := filepath.Join(base, agentID, "shared-cache", "skills")
		set, err := d.buildAgentSharedSkillBundleSet(rt, agentID, scanRoot)
		if err != nil {
			d.logger.Warn("agent shared skill scan failed", "runtime_id", rt.ID, "agent_id", agentID, "path", scanRoot, "error", err)
			continue
		}
		payload.Agents = append(payload.Agents, set)
	}
	if len(payload.Agents) == 0 {
		return nil
	}

	result, err := d.client.SyncAgentSharedSkills(ctx, rt.ID, payload)
	if err != nil {
		return err
	}
	d.logSharedSkillSyncResult(rt, "agent shared skills synced", result)
	return nil
}

func (d *Daemon) buildAgentSharedSkillBundleSet(rt Runtime, agentID, scanRoot string) (AgentSharedSkillBundleSet, error) {
	summaries, _, err := listLocalSkillsFromRoot(rt.Provider, scanRoot)
	if err != nil {
		return AgentSharedSkillBundleSet{}, err
	}

	presentKeys := make([]string, 0, len(summaries))
	bundles := make([]SharedSkillBundle, 0, len(summaries))
	activeCacheKeys := make(map[string]struct{}, len(summaries))

	d.sharedSkillScanMu.Lock()
	defer d.sharedSkillScanMu.Unlock()

	for _, summary := range summaries {
		presentKeys = append(presentKeys, summary.Key)
		skillDir := filepath.Join(scanRoot, filepath.FromSlash(summary.Key))
		fingerprint, err := localSkillScanFingerprint(skillDir)
		if err != nil {
			d.logger.Warn("agent shared skill fingerprint skipped", "agent_id", agentID, "key", summary.Key, "error", err)
			continue
		}
		cacheKey := scanRoot + "\x00" + summary.Key
		activeCacheKeys[cacheKey] = struct{}{}
		if d.sharedSkillScanCache[cacheKey] == fingerprint {
			continue
		}

		bundle, _, err := loadLocalSkillBundleFromRoot(rt.Provider, scanRoot, summary.Key)
		if err != nil {
			d.logger.Warn("agent shared skill bundle skipped", "agent_id", agentID, "key", summary.Key, "error", err)
			continue
		}
		files := make([]SkillFileData, len(bundle.Files))
		copy(files, bundle.Files)
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		d.sharedSkillScanCache[cacheKey] = fingerprint
		bundles = append(bundles, SharedSkillBundle{
			Key:         summary.Key,
			Name:        bundle.Name,
			Description: bundle.Description,
			Content:     bundle.Content,
			SourcePath:  bundle.SourcePath,
			Provider:    rt.Provider,
			ContentHash: sharedSkillHash(bundle.Content, files),
			Files:       files,
		})
	}

	for cacheKey := range d.sharedSkillScanCache {
		if !strings.HasPrefix(cacheKey, scanRoot+"\x00") {
			continue
		}
		if _, active := activeCacheKeys[cacheKey]; !active {
			delete(d.sharedSkillScanCache, cacheKey)
		}
	}

	return AgentSharedSkillBundleSet{AgentID: agentID, Skills: bundles, PresentKeys: presentKeys}, nil
}

func (d *Daemon) logSharedSkillSyncResult(rt Runtime, msg string, result *SharedSkillSyncResult) {
	if result == nil {
		return
	}
	for _, conflict := range result.Conflicts {
		d.logger.Warn("shared skill sync conflict", "runtime_id", rt.ID, "key", conflict.Key, "name", conflict.Name, "skill_id", conflict.Skill, "reason", conflict.Reason)
	}
	for _, item := range result.Errors {
		d.logger.Warn("shared skill sync item failed", "runtime_id", rt.ID, "key", item.Key, "name", item.Name, "error", item.Error)
	}
	d.logger.Debug(msg, "runtime_id", rt.ID, "created", result.Created, "updated", result.Updated, "unchanged", result.Unchanged, "deleted", result.Deleted, "conflicts", len(result.Conflicts), "errors", len(result.Errors))
}

func sharedSkillHash(content string, files []SkillFileData) string {
	h := sha256.New()
	_, _ = h.Write([]byte(content))
	for _, f := range files {
		_, _ = h.Write([]byte("\x00" + f.Path + "\x00" + f.Content))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
