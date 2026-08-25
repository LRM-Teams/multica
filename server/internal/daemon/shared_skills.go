package daemon

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
	agentskills "github.com/multica-ai/multica/server/internal/daemon/agent/skills"
	"github.com/multica-ai/multica/server/internal/memorycuration"
	"github.com/multica-ai/multica/server/pkg/agent"
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

func (d *Daemon) localMemoryCurationLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	d.runLocalMemoryCurationOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.runLocalMemoryCurationOnce(ctx)
		}
	}
}

func (d *Daemon) runLocalMemoryCurationOnce(ctx context.Context) {
	if !d.ready.Load() {
		return
	}
	for _, rt := range d.localMemoryCurationRuntimes() {
		runtime := rt
		go func() {
			if err := d.runLocalMemoryCuration(ctx, runtime); err != nil && ctx.Err() == nil {
				d.logger.Warn("local memory curation failed", "runtime_id", runtime.ID, "provider", runtime.Provider, "error", err)
			}
		}()
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

func (d *Daemon) runLocalMemoryCuration(ctx context.Context, rt Runtime) error {
	if strings.TrimSpace(rt.WorkspaceID) == "" {
		return nil
	}
	agentEntry, ok := d.cfg.Agents[rt.Provider]
	if !ok || strings.TrimSpace(agentEntry.Path) == "" {
		return nil
	}
	loc, err := time.LoadLocation(memorycuration.DefaultTimezone)
	if err != nil {
		loc = time.FixedZone("CST", 8*60*60)
	}
	now := time.Now().UTC()
	localNow := now.In(loc)
	stageHours := []struct {
		stage memorycuration.Stage
		hour  int
	}{
		{memorycuration.StageL1, 1},
		{memorycuration.StageL2, 2},
		{memorycuration.StageL3, 3},
		{memorycuration.StageL4, 4},
	}
	planDate := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	for _, scheduled := range stageHours {
		if localNow.Hour() < scheduled.hour || !d.claimLocalMemoryCurationRun(rt.WorkspaceID, scheduled.stage, localNow) {
			continue
		}
		reviewerCfg := memorycuration.AgentL3ReviewerConfig{
			Provider: rt.Provider, Path: agentEntry.Path, Model: agentEntry.Model, Timeout: d.cfg.MemoryCurationL3ReviewTimeout,
		}
		reviewer := memorycuration.NewConfiguredL3Reviewer(d.cfg.MemoryCurationL3ReviewEnabled, reviewerCfg)
		var stageAgent memorycuration.StageAgent
		if d.cfg.MemoryCurationL3ReviewEnabled {
			stageAgent, _ = memorycuration.NewAgentStageRunner(reviewerCfg)
		}
		res, runErr := memorycuration.NewEngine(reviewer).Run(memorycuration.Options{
			Context: ctx, WorkspacesRoot: d.cfg.WorkspacesRoot, WorkspaceID: rt.WorkspaceID, StageAgent: stageAgent,
			AllAgents: true, Stage: scheduled.stage, Since: planDate, Until: planDate,
			Now: now, Timezone: memorycuration.DefaultTimezone,
		})
		if runErr != nil {
			d.releaseLocalMemoryCurationRun(rt.WorkspaceID, scheduled.stage)
			return runErr
		}
		if len(res.Errors) > 0 || (scheduled.stage == memorycuration.StageL3 && shouldRetryLocalL3(res)) {
			d.releaseLocalMemoryCurationRun(rt.WorkspaceID, scheduled.stage)
			return fmt.Errorf("local memory curation deferred or failed: errors=%d deferred=%d", len(res.Errors), res.ReviewDeferred)
		}
	}
	return nil
}

func shouldRetryLocalL3(res memorycuration.Result) bool {
	nonRetryable := 0
	for _, trace := range res.ReviewTraces {
		if trace.ReasonCode == "low_confidence" || trace.ReasonCode == "invalid_decision" {
			nonRetryable++
		}
	}
	return res.ReviewDeferred > nonRetryable
}

func (d *Daemon) claimLocalMemoryCurationRun(workspaceID string, stage memorycuration.Stage, localNow time.Time) bool {
	key := workspaceID + "\x00" + string(stage)
	planDate := localNow.Format("2006-01-02")
	d.memoryCurationMu.Lock()
	defer d.memoryCurationMu.Unlock()
	if d.memoryCurationRuns[key] == planDate {
		return false
	}
	d.memoryCurationRuns[key] = planDate
	return true
}

func (d *Daemon) releaseLocalMemoryCurationRun(workspaceID string, stage memorycuration.Stage) {
	d.memoryCurationMu.Lock()
	delete(d.memoryCurationRuns, workspaceID+"\x00"+string(stage))
	d.memoryCurationMu.Unlock()
}

func (d *Daemon) localMemoryCurationRuntimes() []Runtime {
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
			agentEntry, configured := d.cfg.Agents[rt.Provider]
			if ok && rt.Status == "online" && configured && strings.TrimSpace(agentEntry.Path) != "" {
				runtimes = append(runtimes, rt)
				break
			}
		}
	}
	return runtimes
}

// sharedSkillSyncRuntimes returns one stable online runtime per workspace so
// workspace-level scans are synced exactly once per poll.
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
	home, err := userHomeDir()
	if err != nil {
		return "", false
	}
	switch provider {
	case agent.ProviderPi:
		return filepath.Join(home, ".pi", "share", "skills"), true
	default:
		return "", false
	}
}

// The authoritative on-disk root for one Multica agent is now resolved through
// execenv (PredictAgentRootDir / ResolveAgentWorkspaceLayout), keyed only by
// workspace + agent ID (LRM-955): switching coding harness / provider / runtime
// on the same machine must keep reading and writing this same tree. The tree is
// also the Agent's durable execution cwd. The agentRoot helpers below operate
// on an already-resolved root.
func agentSyncQueueDir(agentRoot string) string { return filepath.Join(agentRoot, "sync_queue") }
func agentSkillDraftsDir(agentRoot string) string {
	return filepath.Join(agentRoot, "skills", "drafts")
}
func agentMemoryCandidatesPath(agentRoot string) string {
	return filepath.Join(agentSyncQueueDir(agentRoot), "memory-candidates.jsonl")
}
func agentSkillCandidatesPath(agentRoot string) string {
	return filepath.Join(agentSyncQueueDir(agentRoot), "skill-candidates.jsonl")
}

func ensureMulticaAgentRoot(root string) error {
	// Writers create their own memory, skill, cache, and runtime paths. Keeping
	// initialization lazy avoids a forest of empty folders and template files.
	return os.MkdirAll(root, 0o755)
}

func (d *Daemon) syncSharedSkillsForRuntime(ctx context.Context, rt Runtime) error {
	scanRoot, ok := sharedSkillScanRoot(d.cfg, rt.Provider)
	if ok {
		if err := d.syncWorkspaceSharedSkillsForRuntime(ctx, rt, scanRoot); err != nil {
			return err
		}
	}
	return d.syncEvolutionSubmissionsForRuntime(ctx, rt)
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
		fingerprint, err := agentskills.LocalFingerprint(skillDir)
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
	d.logSharedSkillSyncResult(rt, "shared skills synced", result)
	return nil
}

func (d *Daemon) syncEvolutionSubmissionsForRuntime(ctx context.Context, rt Runtime) error {
	if strings.TrimSpace(rt.WorkspaceID) == "" {
		return nil
	}

	acknowledged, err := loadEvolutionAcknowledgements(d.cfg, rt.WorkspaceID)
	if err != nil {
		return fmt.Errorf("load evolution acknowledgements: %w", err)
	}
	payload := EvolutionSubmissionSyncPayload{}
	pendingFingerprints := map[string]string{}
	base := agentworkspace.AgentsDir(d.cfg.WorkspacesRoot, rt.WorkspaceID)
	submissions, err := d.scanEvolutionSubmissionsRoot(rt, base)
	if err != nil {
		d.logger.Warn("agent evolution root unavailable", "path", base, "provider", rt.Provider, "error", err)
	} else {
		for _, submission := range submissions {
			ackKey := evolutionSubmissionAckKey(submission.AgentID, submission.LocalUnitID)
			fingerprint := evolutionSubmissionFingerprint(submission)
			if acknowledged[ackKey] == fingerprint {
				continue
			}
			if _, exists := pendingFingerprints[ackKey]; exists {
				continue
			}
			pendingFingerprints[ackKey] = fingerprint
			payload.Submissions = append(payload.Submissions, submission)
		}
	}
	if len(payload.Submissions) == 0 {
		return nil
	}

	result, err := d.client.SyncEvolutionSubmissions(ctx, rt.ID, payload)
	if err != nil {
		return err
	}
	acknowledgementsChanged := false
	for _, ackKey := range result.Acknowledged {
		if fingerprint, ok := pendingFingerprints[ackKey]; ok {
			if acknowledged[ackKey] != fingerprint {
				acknowledged[ackKey] = fingerprint
				acknowledgementsChanged = true
			}
		}
	}
	if acknowledgementsChanged {
		if err := saveEvolutionAcknowledgements(d.cfg, rt.WorkspaceID, acknowledged); err != nil {
			return fmt.Errorf("save evolution acknowledgements: %w", err)
		}
	}
	d.logSharedSkillSyncResult(rt, "evolution submissions synced", result)
	return nil
}

func (d *Daemon) scanEvolutionSubmissionsRoot(rt Runtime, base string) ([]EvolutionSubmissionBundle, error) {
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var submissions []EvolutionSubmissionBundle
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		agentID := entry.Name()
		agentRoot := filepath.Join(base, agentID)
		releaseLock, lockErr := memorycuration.AcquireAgentRootFileLock(agentRoot, false, time.Now().UTC())
		if lockErr != nil {
			continue
		}
		if err := ensureMulticaAgentRoot(agentRoot); err != nil {
			releaseLock()
			d.logger.Warn("agent root creation failed", "runtime_id", rt.ID, "agent_id", agentID, "path", agentRoot, "error", err)
			continue
		}
		memorySubmissions, err := d.loadMemoryCandidateSubmissions(rt, agentID, agentRoot)
		if err != nil {
			releaseLock()
			d.logger.Warn("memory candidate scan failed", "runtime_id", rt.ID, "agent_id", agentID, "error", err)
			continue
		}
		skillSubmissions, err := d.loadSkillCandidateSubmissions(rt, agentID, agentRoot)
		releaseLock()
		if err != nil {
			d.logger.Warn("skill candidate scan failed", "runtime_id", rt.ID, "agent_id", agentID, "error", err)
			continue
		}
		submissions = append(submissions, memorySubmissions...)
		submissions = append(submissions, skillSubmissions...)
	}
	return submissions, nil
}

func (d *Daemon) loadMemoryCandidateSubmissions(rt Runtime, agentID, agentRoot string) ([]EvolutionSubmissionBundle, error) {
	path := agentMemoryCandidatesPath(agentRoot)
	items, issues, err := readEvolutionCandidateJSONL(path)
	if err != nil {
		return nil, err
	}
	if err := quarantineEvolutionCandidateIssues(path, issues); err != nil {
		return nil, err
	}
	submissions := make([]EvolutionSubmissionBundle, 0, len(items))
	for _, item := range items {
		submission := evolutionCandidateToBundle(rt.WorkspaceID, agentID, item)
		if submission.UnitType == "" {
			submission.UnitType = "memory"
		}
		if submission.LocalUnitID == "" || submission.UnitType == "" {
			continue
		}
		if submission.ContentHash == "" {
			submission.ContentHash = sharedSkillHash(submission.Content, nil)
		}
		submissions = append(submissions, submission)
	}
	return submissions, nil
}

func (d *Daemon) loadSkillCandidateSubmissions(rt Runtime, agentID, agentRoot string) ([]EvolutionSubmissionBundle, error) {
	path := agentSkillCandidatesPath(agentRoot)
	items, issues, err := readEvolutionCandidateJSONL(path)
	if err != nil {
		return nil, err
	}
	if err := quarantineEvolutionCandidateIssues(path, issues); err != nil {
		return nil, err
	}
	submissions := make([]EvolutionSubmissionBundle, 0, len(items))
	for _, item := range items {
		submission := evolutionCandidateToBundle(rt.WorkspaceID, agentID, item)
		if submission.UnitType == "" {
			submission.UnitType = "skill"
		}
		if submission.LocalUnitID == "" || submission.UnitType == "" {
			continue
		}
		bundlePath := bundlePathFromCandidate(item)
		if bundlePath != "" {
			bundleDir, err := secureSkillDraftBundleDir(agentRoot, bundlePath)
			if err != nil {
				d.logger.Warn("skill candidate bundle rejected", "agent_id", agentID, "local_unit_id", submission.LocalUnitID, "bundle_path", bundlePath, "error", err)
				continue
			}
			files, content, err := loadSkillDraftBundle(bundleDir)
			if err != nil {
				d.logger.Warn("skill candidate bundle skipped", "agent_id", agentID, "local_unit_id", submission.LocalUnitID, "bundle_path", bundlePath, "error", err)
				continue
			}
			submission.Files = files
			if submission.Content == "" {
				submission.Content = content
			}
			if submission.BundleRef == "" {
				submission.BundleRef = relativizeHomePath(bundleDir)
			}
			if submission.BundleHash == "" {
				submission.BundleHash = sharedSkillHash(content, files)
			}
		}
		submissions = append(submissions, submission)
	}
	return submissions, nil
}

type evolutionCandidateParseIssue struct {
	Line  int
	Raw   string
	Error string
}

func readEvolutionCandidateJSONL(path string) ([]map[string]json.RawMessage, []evolutionCandidateParseIssue, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	items := []map[string]json.RawMessage{}
	issues := []evolutionCandidateParseIssue{}
	lineNumber := 0
	for {
		line, err := reader.ReadBytes('\n')
		lineNumber++
		if len(strings.TrimSpace(string(line))) > 0 {
			var item map[string]json.RawMessage
			if unmarshalErr := json.Unmarshal(line, &item); unmarshalErr == nil {
				items = append(items, item)
			} else {
				issues = append(issues, evolutionCandidateParseIssue{Line: lineNumber, Raw: strings.TrimRight(string(line), "\r\n"), Error: unmarshalErr.Error()})
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, nil, err
		}
	}
	return items, issues, nil
}

func quarantineEvolutionCandidateIssues(sourcePath string, issues []evolutionCandidateParseIssue) error {
	if len(issues) == 0 {
		return nil
	}
	quarantinePath := sourcePath + ".invalid.jsonl"
	existing, err := os.ReadFile(quarantinePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read candidate quarantine: %w", err)
	}
	known := string(existing)
	f, err := os.OpenFile(quarantinePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open candidate quarantine: %w", err)
	}
	defer f.Close()
	encoder := json.NewEncoder(f)
	for _, issue := range issues {
		hash := sha256.Sum256([]byte(issue.Raw))
		rawHash := hex.EncodeToString(hash[:])
		if strings.Contains(known, rawHash) {
			continue
		}
		record := struct {
			SourceLine int    `json:"source_line"`
			Error      string `json:"error"`
			Raw        string `json:"raw"`
			RawHash    string `json:"raw_hash"`
		}{SourceLine: issue.Line, Error: issue.Error, Raw: issue.Raw, RawHash: rawHash}
		if err := encoder.Encode(record); err != nil {
			return fmt.Errorf("write candidate quarantine: %w", err)
		}
		known += rawHash
	}
	return nil
}

func secureSkillDraftBundleDir(agentRoot, bundlePath string) (string, error) {
	bundlePath = strings.TrimSpace(bundlePath)
	if bundlePath == "" || filepath.IsAbs(filepath.FromSlash(bundlePath)) {
		return "", fmt.Errorf("bundle path must be relative")
	}
	draftsRoot := filepath.Clean(agentSkillDraftsDir(agentRoot))
	bundleDir := filepath.Clean(filepath.Join(agentSyncQueueDir(agentRoot), filepath.FromSlash(bundlePath)))
	if !pathContainedBy(draftsRoot, bundleDir) {
		return "", fmt.Errorf("bundle path escapes skills/drafts")
	}
	resolvedRoot, err := filepath.EvalSymlinks(draftsRoot)
	if err != nil {
		return "", fmt.Errorf("resolve drafts root: %w", err)
	}
	resolvedBundle, err := filepath.EvalSymlinks(bundleDir)
	if err != nil {
		return "", fmt.Errorf("resolve bundle path: %w", err)
	}
	if !pathContainedBy(resolvedRoot, resolvedBundle) {
		return "", fmt.Errorf("bundle symlink escapes skills/drafts")
	}
	return resolvedBundle, nil
}

func pathContainedBy(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || filepath.IsAbs(rel) {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func evolutionSubmissionAckKey(agentID, localUnitID string) string {
	return strings.TrimSpace(agentID) + "/" + strings.TrimSpace(localUnitID)
}

func evolutionSubmissionFingerprint(submission EvolutionSubmissionBundle) string {
	payload, _ := json.Marshal(submission)
	hash := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(hash[:])
}

func evolutionAcknowledgementsPath(cfg Config, workspaceID string) string {
	return filepath.Join(agentworkspace.WorkspaceDir(cfg.WorkspacesRoot, workspaceID), ".multica", "evolution-sync-acks.json")
}

func loadEvolutionAcknowledgements(cfg Config, workspaceID string) (map[string]string, error) {
	path := evolutionAcknowledgementsPath(cfg, workspaceID)
	payload, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	acknowledged := map[string]string{}
	if err := json.Unmarshal(payload, &acknowledged); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return acknowledged, nil
}

func saveEvolutionAcknowledgements(cfg Config, workspaceID string, acknowledged map[string]string) error {
	path := evolutionAcknowledgementsPath(cfg, workspaceID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(acknowledged, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".evolution-sync-acks-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func evolutionCandidateToBundle(workspaceID, agentID string, item map[string]json.RawMessage) EvolutionSubmissionBundle {
	payload, _ := json.Marshal(item)
	return EvolutionSubmissionBundle{
		WorkspaceID:    workspaceID,
		AgentID:        agentID,
		UnitType:       jsonString(item, "unit_type"),
		LocalUnitID:    jsonString(item, "local_unit_id"),
		Title:          jsonString(item, "title"),
		Summary:        jsonString(item, "summary"),
		Content:        jsonString(item, "content"),
		Payload:        payload,
		ContentHash:    jsonString(item, "content_hash"),
		BundleHash:     jsonString(item, "bundle_hash"),
		BundleRef:      jsonString(item, "bundle_ref"),
		Sensitivity:    jsonString(item, "sensitivity"),
		Confidence:     jsonString(item, "confidence"),
		SuggestedScope: jsonString(item, "suggested_scope"),
		SourceUserID:   jsonString(item, "source_user_id"),
		SubjectType:    jsonString(item, "subject_type"),
		SubjectID:      jsonString(item, "subject_id"),
		Evidence:       jsonRawOrEmptyObject(item, "evidence"),
		Applies:        jsonRawOrEmptyObject(item, "applies"),
		Tags:           jsonStringSlice(item, "tags"),
		Tools:          jsonStringSlice(item, "tools"),
		TaskTypes:      jsonStringSlice(item, "task_types"),
		ProjectTypes:   jsonStringSlice(item, "project_types"),
		Languages:      jsonStringSlice(item, "languages"),
		Frameworks:     jsonStringSlice(item, "frameworks"),
		SourceCreated:  jsonString(item, "created_at"),
	}
}

func bundlePathFromCandidate(item map[string]json.RawMessage) string {
	return strings.TrimSpace(jsonString(item, "bundle_path"))
}

func loadSkillDraftBundle(bundleDir string) ([]SkillFileData, string, error) {
	bundle, err := agentskills.NewLocalCatalog(filepath.Dir(bundleDir)).Load(filepath.Base(bundleDir))
	if err != nil {
		return nil, "", err
	}
	files := make([]SkillFileData, 0, len(bundle.Files)+1)
	files = append(files, SkillFileData{Path: agentskills.ContentFilename, Content: bundle.Content})
	for _, file := range bundle.Files {
		files = append(files, SkillFileData{Path: file.Path, Content: file.Content})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, bundle.Content, nil
}

func jsonString(item map[string]json.RawMessage, key string) string {
	raw, ok := item[key]
	if !ok || len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	return ""
}

func jsonStringSlice(item map[string]json.RawMessage, key string) []string {
	raw, ok := item[key]
	if !ok || len(raw) == 0 {
		return nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func jsonRawOrEmptyObject(item map[string]json.RawMessage, key string) json.RawMessage {
	raw, ok := item[key]
	if !ok || len(raw) == 0 || !json.Valid(raw) {
		return json.RawMessage(`{}`)
	}
	return raw
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
