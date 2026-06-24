package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

func agentSharedSkillScanBase(cfg Config, provider, workspaceID string) (string, bool) {
	if strings.TrimSpace(workspaceID) == "" {
		return "", false
	}
	switch provider {
	case "pi":
		return filepath.Join(cfg.WorkspacesRoot, workspaceID, ".pi", "agents"), true
	default:
		return "", false
	}
}

func agentSharedSkillScanRoot(agentBase, agentID string) string {
	return filepath.Join(agentBase, agentID, "sync_queue", "skill-candidates")
}

func (d *Daemon) syncSharedSkillsForRuntime(ctx context.Context, rt Runtime) error {
	scanRoot, ok := sharedSkillScanRoot(d.cfg, rt.Provider)
	if ok {
		if err := d.syncWorkspaceSharedSkillsForRuntime(ctx, rt, scanRoot); err != nil {
			return err
		}
	}
	if err := d.syncAgentSharedSkillsForRuntime(ctx, rt); err != nil {
		return err
	}
	return d.syncAgentMemoriesForRuntime(ctx, rt)
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
	base, ok := agentSharedSkillScanBase(d.cfg, rt.Provider, rt.WorkspaceID)
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
		scanRoot := agentSharedSkillScanRoot(base, agentID)
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

func (d *Daemon) syncAgentMemoriesForRuntime(ctx context.Context, rt Runtime) error {
	base, ok := agentSharedSkillScanBase(d.cfg, rt.Provider, rt.WorkspaceID)
	if !ok {
		return nil
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		if !os.IsNotExist(err) {
			d.logger.Warn("agent memory root unavailable", "path", base, "provider", rt.Provider, "error", err)
		}
		return nil
	}

	payload := AgentMemorySyncPayload{Agents: make([]AgentMemoryBundleSet, 0, len(entries))}
	for _, entry := range entries {
		if !entry.IsDir() || isIgnoredLocalSkillEntry(entry.Name()) {
			continue
		}
		agentID := entry.Name()
		scanRoot := filepath.Join(base, agentID, "shared-cache", "memory")
		set, err := d.buildAgentMemoryBundleSet(rt, agentID, scanRoot)
		if err != nil {
			d.logger.Warn("agent memory scan failed", "runtime_id", rt.ID, "agent_id", agentID, "path", scanRoot, "error", err)
			continue
		}
		payload.Agents = append(payload.Agents, set)
	}
	if len(payload.Agents) == 0 {
		return nil
	}

	result, err := d.client.SyncAgentMemories(ctx, rt.ID, payload)
	if err != nil {
		return err
	}
	d.logSharedSkillSyncResult(rt, "agent memories synced", result)
	return nil
}

func (d *Daemon) buildAgentMemoryBundleSet(rt Runtime, agentID, scanRoot string) (AgentMemoryBundleSet, error) {
	entries, err := os.ReadDir(scanRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return AgentMemoryBundleSet{AgentID: agentID}, nil
		}
		return AgentMemoryBundleSet{}, err
	}

	presentKeys := make([]string, 0, len(entries))
	memories := make([]AgentMemoryBundle, 0, len(entries))
	activeCacheKeys := make(map[string]struct{}, len(entries))

	d.sharedSkillScanMu.Lock()
	defer d.sharedSkillScanMu.Unlock()

	for _, entry := range entries {
		if entry.IsDir() || isIgnoredLocalSkillEntry(entry.Name()) || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			continue
		}
		key, err := normalizeLocalSkillKey(entry.Name())
		if err != nil {
			continue
		}
		path := filepath.Join(scanRoot, entry.Name())
		info, err := os.Stat(path)
		if err != nil || info.Size() > maxLocalSkillFileSize {
			continue
		}
		presentKeys = append(presentKeys, key)
		fingerprint := info.ModTime().UTC().Format(time.RFC3339Nano) + "\x00" + info.Name() + "\x00" + strconv.FormatInt(info.Size(), 10)
		cacheKey := scanRoot + "\x00" + key
		activeCacheKeys[cacheKey] = struct{}{}
		if d.sharedSkillScanCache[cacheKey] == fingerprint {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		d.sharedSkillScanCache[cacheKey] = fingerprint
		memories = append(memories, AgentMemoryBundle{
			Key:         key,
			Name:        strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())),
			Content:     string(content),
			SourcePath:  relativizeHomePath(path),
			Provider:    rt.Provider,
			ContentHash: sharedSkillHash(string(content), nil),
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
	sort.Strings(presentKeys)
	sort.Slice(memories, func(i, j int) bool { return memories[i].Key < memories[j].Key })
	return AgentMemoryBundleSet{AgentID: agentID, Memories: memories, PresentKeys: presentKeys}, nil
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
