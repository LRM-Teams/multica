package memorycuration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/memorypolicy"
)

type Engine struct {
	l3Reviewer L3Reviewer
}

var agentRootLocks sync.Map

func lockAgentRoot(root string) func() {
	value, _ := agentRootLocks.LoadOrStore(root, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func NewEngine(reviewers ...L3Reviewer) *Engine {
	var reviewer L3Reviewer
	if len(reviewers) > 0 {
		reviewer = reviewers[0]
	}
	return &Engine{l3Reviewer: reviewer}
}

func (e *Engine) Run(opts Options) (Result, error) {
	stage := opts.Stage
	if stage == "" {
		stage = StageAll
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	if opts.Context == nil {
		opts.Context = context.Background()
	}
	if opts.Timezone == "" {
		opts.Timezone = DefaultTimezone
	}
	if opts.Mode == "" {
		opts.Mode = "auto"
	}
	if opts.ConfidenceThreshold <= 0 {
		opts.ConfidenceThreshold = minL3ActionConfidence
	}
	if opts.Until.IsZero() {
		opts.Until = opts.Now.AddDate(0, 0, -1)
	}
	if opts.Since.IsZero() {
		opts.Since = opts.Until
	}
	opts.Since = dateOnly(opts.Since)
	opts.Until = dateOnly(opts.Until)

	res := Result{
		Stage:          stage,
		WorkspacesRoot: opts.WorkspacesRoot,
		WorkspaceID:    opts.WorkspaceID,
		DateFrom:       formatDate(opts.Since),
		DateTo:         formatDate(opts.Until),
		DryRun:         opts.DryRun,
		Force:          opts.Force,
		Timezone:       opts.Timezone,
	}
	res.Events = append(res.Events, newRunEvent("validated_profile", "", "done", fmt.Sprintf("mode=%s dry_run=%t", opts.Mode, opts.DryRun), opts.Now))
	roots, err := discoverAgentRoots(opts.WorkspacesRoot, opts.WorkspaceID, opts.AgentIDs, opts.AllAgents)
	if err != nil {
		res.Events = append(res.Events, newRunEvent("resolved_targets", "", "failed", err.Error(), opts.Now))
		return res, err
	}
	res.Events = append(res.Events, newRunEvent("resolved_targets", "", "done", fmt.Sprintf("%d target agent(s)", len(roots)), opts.Now))
	if stage == StageTeamCuration {
		res.Events = append(res.Events, newRunEvent("invoked_curator", "team", "started", "team curation", time.Now().UTC()))
		ar, runErr := e.runTeamCuration(roots, opts)
		res.AgentsScanned = len(roots)
		res.EvidenceCollected = ar.EvidenceCollected
		res.SharedCandidatesAdded = ar.SharedCandidatesAdded
		res.ConflictsFound = ar.ConflictsFound
		res.AgentResults = append(res.AgentResults, ar)
		if runErr != nil {
			res.Events = append(res.Events, newRunEvent("invoked_curator", "team", "failed", runErr.Error(), time.Now().UTC()))
			res.Errors = append(res.Errors, AgentError{WorkspaceID: opts.WorkspaceID, Stage: StageTeamCuration, Error: runErr.Error()})
		} else {
			res.Events = append(res.Events, newRunEvent("invoked_curator", "team", "done", "team curation", time.Now().UTC()))
			res.Events = append(res.Events, newRunEvent("persisted_candidates", "team", "done", fmt.Sprintf("%d team item(s)", ar.SharedCandidatesAdded), time.Now().UTC()))
		}
		return res, nil
	}
	for _, root := range roots {
		ar := AgentRunResult{WorkspaceID: root.WorkspaceID, AgentID: root.AgentID, Root: root.Root}
		res.Events = append(res.Events, newRunEvent("read_local_files", root.AgentID, "started", root.Root, time.Now().UTC()))
		res.AgentsScanned++
		if !opts.DryRun {
			if err := ensureMemoryRoot(root.Root); err != nil {
				res.Events = append(res.Events, newRunEvent("read_local_files", root.AgentID, "failed", err.Error(), time.Now().UTC()))
				res.Errors = append(res.Errors, AgentError{WorkspaceID: root.WorkspaceID, AgentID: root.AgentID, Stage: stage, Error: err.Error()})
				res.AgentResults = append(res.AgentResults, ar)
				continue
			}
		}
		unlock := lockAgentRoot(root.Root)
		releaseFileLock, lockErr := AcquireAgentRootFileLock(root.Root, opts.DryRun, opts.Now)
		if lockErr != nil {
			unlock()
			res.Events = append(res.Events, newRunEvent("read_local_files", root.AgentID, "failed", lockErr.Error(), time.Now().UTC()))
			res.Errors = append(res.Errors, AgentError{WorkspaceID: root.WorkspaceID, AgentID: root.AgentID, Stage: stage, Error: lockErr.Error()})
			res.AgentResults = append(res.AgentResults, ar)
			continue
		}
		res.Events = append(res.Events, newRunEvent("read_local_files", root.AgentID, "done", root.Root, time.Now().UTC()))
		stages := []Stage{stage}
		if stage == StageAll {
			stages = []Stage{StageAgentSelfReview}
		}
		for _, st := range stages {
			res.Events = append(res.Events, newRunEvent("invoked_curator", root.AgentID, "started", string(st), time.Now().UTC()))
			var sr AgentRunResult
			var err error
			switch st {
			case StageAgentSelfReview:
				sr, err = e.runAgentSelfReview(root, opts)
			case StageL1:
				sr, err = e.runL1(root, opts)
			case StageL2:
				sr, err = e.runL2(root, opts)
			case StageL3:
				sr, err = e.runL3(root, opts)
			case StageL4:
				sr, err = e.runL4(root, opts)
			default:
				err = fmt.Errorf("unsupported stage %q", st)
			}
			mergeAgentRunResult(&ar, sr)
			if err != nil {
				res.Events = append(res.Events, newRunEvent("invoked_curator", root.AgentID, "failed", err.Error(), time.Now().UTC()))
				res.Errors = append(res.Errors, AgentError{WorkspaceID: root.WorkspaceID, AgentID: root.AgentID, Stage: st, Error: err.Error()})
				continue
			}
			res.Events = append(res.Events, newRunEvent("invoked_curator", root.AgentID, "done", string(st), time.Now().UTC()))
			res.Events = append(res.Events, newRunEvent("parsed_output", root.AgentID, "done", string(st), time.Now().UTC()))
		}
		releaseFileLock()
		unlock()
		if ar.Changed {
			res.AgentsChanged++
		}
		res.DailyFilesWritten += ar.DailyFilesWritten
		res.ReviewCandidatesAdded += ar.ReviewCandidatesAdded
		res.EntriesReviewed += ar.EntriesReviewed
		res.MemoryRoutes += ar.MemoryRoutes
		res.SkillRoutes += ar.SkillRoutes
		res.SplitRoutes += ar.SplitRoutes
		res.DiscardRoutes += ar.DiscardRoutes
		res.ReviewDeferred += ar.ReviewDeferred
		res.EntriesPromoted += ar.EntriesPromoted
		res.SkillCandidatesAdded += ar.SkillCandidatesAdded
		res.SharedCandidatesAdded += ar.SharedCandidatesAdded
		res.SharedCandidatesSynced += ar.SharedCandidatesSynced
		res.EntriesArchived += ar.EntriesArchived
		res.DuplicatesMerged += ar.DuplicatesMerged
		res.ConflictsFound += ar.ConflictsFound
		res.EvidenceCollected += ar.EvidenceCollected
		res.ReviewTraces = appendBoundedReviewTraces(res.ReviewTraces, ar.ReviewTraces, defaultL3ReviewMaxEntries)
		res.Events = append(res.Events, newRunEvent("persisted_candidates", root.AgentID, "done", fmt.Sprintf("memory=%d skill=%d", ar.ReviewCandidatesAdded, ar.SkillCandidatesAdded), time.Now().UTC()))
		res.AgentResults = append(res.AgentResults, ar)
	}
	if stage == StageAll {
		res.Events = append(res.Events, newRunEvent("invoked_curator", "team", "started", "team curation", time.Now().UTC()))
		teamResult, teamErr := e.runTeamCuration(roots, opts)
		res.EvidenceCollected += teamResult.EvidenceCollected
		res.SharedCandidatesAdded += teamResult.SharedCandidatesAdded
		res.ConflictsFound += teamResult.ConflictsFound
		res.AgentResults = append(res.AgentResults, teamResult)
		if teamErr != nil {
			res.Events = append(res.Events, newRunEvent("invoked_curator", "team", "failed", teamErr.Error(), time.Now().UTC()))
			res.Errors = append(res.Errors, AgentError{WorkspaceID: opts.WorkspaceID, Stage: StageTeamCuration, Error: teamErr.Error()})
		} else {
			res.Events = append(res.Events, newRunEvent("invoked_curator", "team", "done", "team curation", time.Now().UTC()))
			res.Events = append(res.Events, newRunEvent("persisted_candidates", "team", "done", fmt.Sprintf("%d team item(s)", teamResult.SharedCandidatesAdded), time.Now().UTC()))
		}
	}
	return res, nil
}

func newRunEvent(key, agentID, status, message string, at time.Time) RunEvent {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return RunEvent{Key: key, AgentID: agentID, Status: status, Message: message, CreatedAt: at.UTC().Format(time.RFC3339)}
}

func appendBoundedReviewTraces(dst, src []L3ReviewTrace, limit int) []L3ReviewTrace {
	if limit <= 0 || len(dst) >= limit {
		return dst
	}
	remaining := limit - len(dst)
	if len(src) > remaining {
		src = src[:remaining]
	}
	return append(dst, src...)
}

func mergeAgentRunResult(dst *AgentRunResult, src AgentRunResult) {
	dst.Changed = dst.Changed || src.Changed
	dst.DailyFilesWritten += src.DailyFilesWritten
	dst.ReviewCandidatesAdded += src.ReviewCandidatesAdded
	dst.EntriesReviewed += src.EntriesReviewed
	dst.MemoryRoutes += src.MemoryRoutes
	dst.SkillRoutes += src.SkillRoutes
	dst.SplitRoutes += src.SplitRoutes
	dst.DiscardRoutes += src.DiscardRoutes
	dst.ReviewDeferred += src.ReviewDeferred
	dst.EntriesPromoted += src.EntriesPromoted
	dst.SkillCandidatesAdded += src.SkillCandidatesAdded
	dst.ReviewTraces = append(dst.ReviewTraces, src.ReviewTraces...)
	dst.SharedCandidatesAdded += src.SharedCandidatesAdded
	dst.SharedCandidatesSynced += src.SharedCandidatesSynced
	dst.EntriesArchived += src.EntriesArchived
	dst.DuplicatesMerged += src.DuplicatesMerged
	dst.ConflictsFound += src.ConflictsFound
	dst.EvidenceCollected += src.EvidenceCollected
	if strings.TrimSpace(src.CuratorOutput) != "" {
		dst.CuratorOutput = truncateUTF8(strings.TrimSpace(src.CuratorOutput), maxL3ReviewOutputBytes)
	}
}

func stageLocalFiles(root string, names ...string) map[string]string {
	out := make(map[string]string, len(names))
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err == nil {
			out[name] = truncateUTF8(string(b), maxL3ReviewInputBodyBytes)
		}
	}
	return out
}

const (
	maxScopedCurationFiles = 12
	maxScopedCurationBytes = 16 * 1024
	maxScopedCurationFile  = 2 * 1024
)

// stageFilesWithScoped adds only canonical, one-level scoped memory files.
// It deliberately ignores daily history and notes indexes, and bounds the
// additional payload so a workspace with many projects cannot flood context.
func stageFilesWithScoped(root string, names ...string) map[string]string {
	out := stageLocalFiles(root, names...)
	type scopedGroup struct {
		dir      string
		files    []string
		maxBytes int
		maxFiles int
	}
	groups := []scopedGroup{
		{dir: "users", files: []string{"USER.md", "RELATIONSHIP.md"}, maxBytes: 4 * 1024, maxFiles: 4},
		{dir: "projects", files: []string{"STATE.md", "DECISIONS.md", "MEMORY.md"}, maxBytes: 8 * 1024, maxFiles: 6},
		{dir: "channels", files: []string{"CONTEXT.md"}, maxBytes: 4 * 1024, maxFiles: 2},
	}
	remaining := maxScopedCurationBytes
	added := 0
	for _, group := range groups {
		groupRemaining := group.maxBytes
		groupAdded := 0
		entries, err := os.ReadDir(filepath.Join(root, group.dir))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if added >= maxScopedCurationFiles || remaining <= 0 {
				return out
			}
			if groupRemaining <= 0 || groupAdded >= group.maxFiles {
				break
			}
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			for _, filename := range group.files {
				if added >= maxScopedCurationFiles || remaining <= 0 || groupRemaining <= 0 || groupAdded >= group.maxFiles {
					break
				}
				rel := filepath.ToSlash(filepath.Join(group.dir, entry.Name(), filename))
				path := filepath.Join(root, filepath.FromSlash(rel))
				info, err := os.Lstat(path)
				if err != nil || !info.Mode().IsRegular() {
					continue
				}
				payload, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				content := truncateUTF8(strings.TrimSpace(string(payload)), min(maxScopedCurationFile, remaining, groupRemaining))
				if content == "" {
					continue
				}
				out[rel] = content
				remaining -= len(content)
				groupRemaining -= len(content)
				added++
				groupAdded++
			}
		}
	}
	return out
}

func evidenceForAgent(opts Options, workspaceID, agentID string, since, until time.Time) ([]EvidenceItem, error) {
	if opts.DBEvidence != nil {
		return opts.DBEvidence[agentID], nil
	}
	return CollectDBEvidence(opts.Context, opts.DB, workspaceID, agentID, since, until)
}

func runStageAgent(opts Options, root agentRoot, stage Stage, localFiles map[string]string, evidence []EvidenceItem, reviewEntries []L3ReviewEntry) (StageAgentOutput, error) {
	if opts.StageAgent == nil {
		return StageAgentOutput{}, nil
	}
	return opts.StageAgent.RunStage(opts.Context, StageAgentInput{
		Stage: stage, WorkspaceID: root.WorkspaceID, AgentID: root.AgentID, AgentRoot: root.Root,
		DateFrom: formatDate(opts.Since), DateTo: formatDate(opts.Until), Timezone: opts.Timezone,
		Mode: opts.Mode, DryRun: opts.DryRun, LocalFiles: localFiles, DBEvidence: evidence, ReviewEntries: reviewEntries,
		OversizedFiles: detectOversizedMemoryFiles(root.Root),
	})
}

func detectOversizedMemoryFiles(root string) []OversizedMemoryFile {
	var oversized []OversizedMemoryFile
	for _, scopeDir := range []string{"memory", "users", "projects", "channels"} {
		scanRoot := filepath.Join(root, scopeDir)
		_ = filepath.WalkDir(scanRoot, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			limit := memorypolicy.SoftFileLimit(rel)
			if limit == 0 {
				return nil
			}
			info, infoErr := entry.Info()
			if infoErr == nil && info.Size() > limit {
				oversized = append(oversized, OversizedMemoryFile{Path: rel, SizeBytes: info.Size(), SoftLimit: limit})
			}
			return nil
		})
	}
	sort.Slice(oversized, func(i, j int) bool { return oversized[i].Path < oversized[j].Path })
	return oversized
}

func (e *Engine) runAgentSelfReview(root agentRoot, opts Options) (AgentRunResult, error) {
	ar := AgentRunResult{WorkspaceID: root.WorkspaceID, AgentID: root.AgentID, Root: root.Root}
	if opts.StageAgent == nil {
		return ar, fmt.Errorf("agent self-review requires a selected curator runtime")
	}
	evidence, err := evidenceForAgent(opts, root.WorkspaceID, root.AgentID, opts.Since, opts.Until)
	if err != nil {
		return ar, err
	}
	ar.EvidenceCollected += len(evidence)
	before, err := snapshotSelfReviewFiles(root.Root)
	if err != nil {
		return ar, err
	}
	files := stageFilesWithScoped(root.Root,
		"memory/daily/"+formatDate(opts.Until)+".md",
		"memory/REVIEW.md",
		"memory/MEMORY.md",
		"memory/STATE.md",
		"memory/SCRATCHPAD.md",
		"sync_queue/memory-candidates.jsonl",
		"sync_queue/skill-candidates.jsonl",
	)
	out, stageErr := runStageAgent(opts, root, StageAgentSelfReview, files, evidence, nil)
	after, err := snapshotSelfReviewFiles(root.Root)
	if err != nil {
		return ar, err
	}
	changed := changedSelfReviewFiles(before, after)
	reject := func(cause error) (AgentRunResult, error) {
		if rollbackErr := restoreSelfReviewFiles(root.Root, before, changed); rollbackErr != nil {
			return ar, fmt.Errorf("%w; restore self-review files: %v", cause, rollbackErr)
		}
		return ar, cause
	}
	if stageErr != nil {
		return reject(stageErr)
	}
	var report selfReviewStageOutput
	if !extractJSONObject(out.Content, &report) {
		return reject(fmt.Errorf("agent self-review returned invalid JSON contract"))
	}
	declared := make(map[string]struct{}, len(report.LocalWrites))
	for _, write := range report.LocalWrites {
		rel, ok := normalizeSelfReviewPath(write.File)
		if !ok {
			return reject(fmt.Errorf("agent self-review declared disallowed local write %q", write.File))
		}
		if _, ok := changed[rel]; !ok {
			return reject(fmt.Errorf("agent self-review declared local write %q but the file did not change", rel))
		}
		declared[rel] = struct{}{}
	}
	for rel := range changed {
		if _, ok := declared[rel]; !ok {
			return reject(fmt.Errorf("agent self-review changed %q without reporting it in local_writes", rel))
		}
		if snap, exists := after[rel]; exists {
			if err := memorypolicy.ValidateFile(rel, snap.Content); err != nil {
				return reject(fmt.Errorf("agent self-review wrote non-concise memory: %w", err))
			}
		}
		if strings.HasPrefix(rel, "memory/daily/") {
			ar.DailyFilesWritten++
		}
	}
	for _, candidate := range report.Candidates {
		if strings.EqualFold(strings.TrimSpace(candidate.Type), "skill") {
			ar.SkillCandidatesAdded++
		} else {
			ar.ReviewCandidatesAdded++
		}
	}
	ar.Changed = len(changed) > 0 || len(report.Candidates) > 0
	ar.CuratorOutput = out.Content
	return ar, nil
}

type selfReviewStageOutput struct {
	LocalWrites []struct {
		File string `json:"file"`
	} `json:"local_writes"`
	Candidates []struct {
		Type string `json:"type"`
	} `json:"candidates"`
}

type selfReviewFileSnapshot struct {
	Hash    [sha256.Size]byte
	Content []byte
	Mode    os.FileMode
}

func snapshotSelfReviewFiles(root string) (map[string]selfReviewFileSnapshot, error) {
	out := make(map[string]selfReviewFileSnapshot)
	for _, top := range []string{"memory", "users", "projects", "channels", "notes", "sync_queue", filepath.Join("skills", "drafts")} {
		scanRoot := filepath.Join(root, top)
		err := filepath.WalkDir(scanRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				if os.IsNotExist(walkErr) {
					return nil
				}
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			rel, ok := normalizeSelfReviewPath(rel)
			if !ok {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			info, infoErr := entry.Info()
			if infoErr != nil {
				return infoErr
			}
			out[rel] = selfReviewFileSnapshot{Hash: sha256.Sum256(data), Content: append([]byte(nil), data...), Mode: info.Mode().Perm()}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return out, nil
}

func normalizeSelfReviewPath(path string) (string, bool) {
	path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if path == "." || filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, "../") {
		return "", false
	}
	parts := strings.Split(path, "/")
	base := filepath.Base(path)
	switch {
	case path == "memory/MEMORY.md", path == "memory/STATE.md", path == "memory/REVIEW.md", path == "memory/SCRATCHPAD.md":
		return path, true
	case len(parts) == 3 && parts[0] == "memory" && parts[1] == "daily" && strings.HasSuffix(base, ".md"):
		return path, true
	case len(parts) == 3 && parts[0] == "users" && parts[1] != "" && (base == "USER.md" || base == "RELATIONSHIP.md"):
		return path, true
	case len(parts) == 3 && parts[0] == "projects" && parts[1] != "" && (base == "MEMORY.md" || base == "STATE.md" || base == "DECISIONS.md"):
		return path, true
	case len(parts) == 3 && parts[0] == "channels" && parts[1] != "" && base == "CONTEXT.md":
		return path, true
	case len(parts) == 2 && parts[0] == "notes" && strings.HasSuffix(base, ".md"):
		return path, true
	case len(parts) == 2 && parts[0] == "sync_queue" && (strings.HasSuffix(base, ".json") || strings.HasSuffix(base, ".jsonl")):
		return path, true
	case len(parts) == 4 && parts[0] == "skills" && parts[1] == "drafts" && parts[2] != "" && base == "SKILL.md":
		return path, true
	default:
		return "", false
	}
}

func changedSelfReviewFiles(before, after map[string]selfReviewFileSnapshot) map[string]struct{} {
	changed := make(map[string]struct{})
	for rel, snap := range after {
		if old, ok := before[rel]; !ok || old.Hash != snap.Hash {
			changed[rel] = struct{}{}
		}
	}
	for rel := range before {
		if _, ok := after[rel]; !ok {
			changed[rel] = struct{}{}
		}
	}
	return changed
}

func restoreSelfReviewFiles(root string, before map[string]selfReviewFileSnapshot, changed map[string]struct{}) error {
	paths := make([]string, 0, len(changed))
	for rel := range changed {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		path := filepath.Join(root, filepath.FromSlash(rel))
		snap, existed := before[rel]
		if !existed {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, snap.Content, snap.Mode); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) runTeamCuration(roots []agentRoot, opts Options) (AgentRunResult, error) {
	ar := AgentRunResult{WorkspaceID: opts.WorkspaceID, AgentID: "team"}
	if opts.StageAgent == nil {
		return ar, fmt.Errorf("team curation requires a selected curator runtime and agent")
	}
	scratchRoot, err := os.MkdirTemp("", "multica-team-curation-")
	if err != nil {
		return ar, fmt.Errorf("create isolated team curation root: %w", err)
	}
	defer os.RemoveAll(scratchRoot)
	ar.Root = scratchRoot
	// Team curation consumes only server-filtered, explicitly shareable DB
	// candidates. Agent-local MEMORY/REVIEW/notes/sync_queue/skill drafts may
	// contain user-private or sensitive material and have no enforceable
	// shareability marker, so they must not cross this trust boundary.
	localFiles := make(map[string]string)
	var evidence []EvidenceItem
	if opts.DBEvidence != nil {
		// Server may attach pending curation_candidate rows only. Ignore any raw
		// chat/session kinds that leak through (e.g. StageAll reuse of the map).
		appendPending := func(items []EvidenceItem) {
			for _, item := range items {
				if teamShareableEvidence(item) {
					evidence = append(evidence, item)
				}
			}
		}
		for _, root := range roots {
			appendPending(opts.DBEvidence[root.AgentID])
		}
		appendPending(opts.DBEvidence[""])
	}
	ar.EvidenceCollected = len(evidence)
	out, err := opts.StageAgent.RunStage(opts.Context, StageAgentInput{
		Stage: StageTeamCuration, WorkspaceID: opts.WorkspaceID, AgentID: "team", AgentRoot: ar.Root,
		DateFrom: formatDate(opts.Since), DateTo: formatDate(opts.Until), Timezone: opts.Timezone,
		Mode: opts.Mode, DryRun: opts.DryRun, LocalFiles: localFiles, DBEvidence: evidence,
	})
	if err != nil {
		return ar, err
	}
	ar.CuratorOutput = out.Content
	ar.SharedCandidatesAdded, ar.ConflictsFound = CountTeamCurationOutput(out.Content)
	return ar, nil
}

func teamShareableEvidence(item EvidenceItem) bool {
	if item.Kind != "curation_candidate" || strings.EqualFold(strings.TrimSpace(item.Scope), "user") {
		return false
	}
	var metadata struct {
		Shareable bool `json:"shareable"`
	}
	return len(item.Metadata) > 0 && json.Unmarshal(item.Metadata, &metadata) == nil && metadata.Shareable
}

type l2AgentEnvelope struct {
	Candidates []struct {
		Type                string   `json:"type"`
		Title               string   `json:"title"`
		Body                string   `json:"body"`
		ProposedDestination string   `json:"proposed_destination"`
		Sensitivity         string   `json:"sensitivity"`
		Confidence          string   `json:"confidence"`
		Evidence            []string `json:"evidence"`
	} `json:"candidates"`
}

func parseAgentL2Candidates(content string, sourceDate time.Time) []reviewEntry {
	trimmed := strings.TrimSpace(content)
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end < start {
		return nil
	}
	var env l2AgentEnvelope
	if err := json.Unmarshal([]byte(trimmed[start:end+1]), &env); err != nil {
		return nil
	}
	out := make([]reviewEntry, 0, len(env.Candidates))
	for _, c := range env.Candidates {
		body := strings.TrimSpace(c.Body)
		if body == "" {
			continue
		}
		kind := strings.TrimSpace(c.Type)
		if kind == "" {
			kind = "stable_fact"
		}
		dest := strings.TrimSpace(c.ProposedDestination)
		if dest == "" {
			dest = "MEMORY.md"
		}
		confidence := strings.TrimSpace(c.Confidence)
		if confidence == "" {
			confidence = "high"
		}
		sensitivity := normalizeL3Sensitivity(c.Sensitivity)
		if sensitivity == "" {
			sensitivity = "unknown"
		}
		evidence := c.Evidence
		if len(evidence) == 0 {
			evidence = []string{"daily:" + formatDate(sourceDate)}
		}
		out = append(out, reviewEntry{ID: "mem_" + strings.ReplaceAll(formatDate(sourceDate), "-", "") + "_" + hashShort(kind, dest, body, strings.Join(evidence, ",")), Type: kind, Status: "candidate", Confidence: confidence, Sensitivity: sensitivity, Scope: "agent", SourceDate: formatDate(sourceDate), Evidence: evidence, ProposedDestination: dest, Title: sentenceTitle(firstNonEmpty(c.Title, body)), Body: body})
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (e *Engine) runL1(root agentRoot, opts Options) (AgentRunResult, error) {
	ar := AgentRunResult{WorkspaceID: root.WorkspaceID, AgentID: root.AgentID, Root: root.Root}
	workLog, err := fileContentWithoutTemplate(filepath.Join(root.Root, "notes", "work-log.md"))
	if err != nil {
		return ar, err
	}
	scratch, err := fileContentWithoutTemplate(filepath.Join(root.Root, "memory", "SCRATCHPAD.md"))
	if err != nil {
		return ar, err
	}
	for _, d := range dateRange(opts.Since, opts.Until) {
		path := filepath.Join(root.Root, "memory", "daily", formatDate(d)+".md")
		if !opts.Force {
			if _, err := os.Stat(path); err == nil {
				continue
			}
		}
		evidence, err := evidenceForAgent(opts, root.WorkspaceID, root.AgentID, d, d)
		if err != nil {
			return ar, err
		}
		ar.EvidenceCollected += len(evidence)
		content := dailyContent(root, d, workLog, scratch, evidence, opts.Now, opts.Timezone)
		if out, agentErr := runStageAgent(opts, root, StageL1, stageFilesWithScoped(root.Root, "notes/work-log.md", "memory/SCRATCHPAD.md", "memory/MEMORY.md", "memory/STATE.md"), evidence, nil); agentErr != nil {
			return ar, agentErr
		} else if strings.HasPrefix(strings.TrimSpace(out.Content), "#") {
			content = strings.TrimSpace(out.Content) + "\n"
		}
		changed, err := writeIfChanged(path, content, opts.DryRun)
		if err != nil {
			return ar, err
		}
		if changed {
			ar.Changed = true
			ar.DailyFilesWritten++
		}
		if err := appendAudit(root.Root, "l1", d, map[string]any{"outcome": "daily_written", "path": filepath.ToSlash(filepath.Join("memory", "daily", formatDate(d)+".md")), "evidence_collected": len(evidence), "timezone": opts.Timezone}, opts.DryRun); err != nil {
			return ar, err
		}
	}
	return ar, nil
}

func dailyContent(root agentRoot, d time.Time, workLog, scratch string, evidence []EvidenceItem, now time.Time, timezone string) string {
	var activity []string
	var temporary []string
	var evidenceLines []string
	if workLog != "" {
		activity = append(activity, bulletize(workLog)...)
		evidenceLines = append(evidenceLines, "local_notes:work-log.md - Agent-local work log summary.")
	}
	if scratch != "" {
		temporary = append(temporary, bulletize(scratch)...)
		evidenceLines = append(evidenceLines, "local_memory:SCRATCHPAD.md - Agent-local transient notes.")
	}
	for _, item := range evidence {
		detail := item.Title
		if detail == "" {
			detail = item.Snippet
		}
		activity = append(activity, fmt.Sprintf("%s - %s", item.Reference(), detail))
		evidenceLines = append(evidenceLines, fmt.Sprintf("%s - %s", item.Reference(), detail))
	}
	if len(activity) == 0 {
		activity = []string{"No relevant DB or local activity was found for this agent."}
	}
	if len(temporary) == 0 {
		temporary = []string{"No temporary follow-ups extracted by the deterministic recorder."}
	}
	if len(evidenceLines) == 0 {
		evidenceLines = []string{"no_evidence - No platform or local evidence collected."}
	}
	return fmt.Sprintf(`# Daily Memory - %s

## Activity Summary
%s

## Decisions And Stable Facts
- No durable decisions extracted by the deterministic recorder.

## User / Teammate Preferences Observed
- No user preference extracted by the deterministic recorder.

## Temporary State And Follow-ups
%s

## Evidence Index
%s

## Curation Status
- timezone: %s
- l1_recorded_at: %s
- l2_extracted_at:
- l3_promoted_at:
- l4_curated_at:
`, formatDate(d), joinBullets(activity), joinBullets(temporary), joinBullets(evidenceLines), timezone, now.UTC().Format(time.RFC3339))
}

func bulletize(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		if line != "" {
			out = append(out, line)
		}
	}
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

func joinBullets(lines []string) string {
	var b strings.Builder
	for _, line := range lines {
		fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(line))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (e *Engine) runL2(root agentRoot, opts Options) (AgentRunResult, error) {
	ar := AgentRunResult{WorkspaceID: root.WorkspaceID, AgentID: root.AgentID, Root: root.Root}
	reviewPath := filepath.Join(root.Root, "memory", "REVIEW.md")
	existing, err := os.ReadFile(reviewPath)
	if err != nil && !os.IsNotExist(err) {
		return ar, err
	}
	review := string(existing)
	entries, err := parseReview(review)
	if err != nil {
		return ar, err
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		seen[entry.HashKey()] = true
	}
	var newEntries []reviewEntry
	for _, d := range dateRange(opts.Since, opts.Until) {
		path := filepath.Join(root.Root, "memory", "daily", formatDate(d)+".md")
		b, err := os.ReadFile(path)
		dailyExists := true
		if err != nil {
			if os.IsNotExist(err) {
				dailyExists = false
			} else {
				return ar, err
			}
		}
		content := string(b)
		if dailyExists && !opts.Force && statusHasValue(content, "l2_extracted_at") {
			continue
		}
		candidates := candidatesFromDaily(content, d)
		if out, agentErr := runStageAgent(opts, root, StageL2, stageFilesWithScoped(root.Root, "memory/daily/"+formatDate(d)+".md", "notes/work-log.md", "memory/SCRATCHPAD.md", "memory/REVIEW.md", "memory/MEMORY.md", "memory/STATE.md"), nil, nil); agentErr != nil {
			return ar, agentErr
		} else if agentCandidates := parseAgentL2Candidates(out.Content, d); len(agentCandidates) > 0 {
			candidates = agentCandidates
		}
		for _, c := range candidates {
			if seen[c.HashKey()] || hasSemanticDuplicate(entries, c) || hasSemanticDuplicate(newEntries, c) {
				ar.DuplicatesMerged++
				continue
			}
			newEntries = append(newEntries, c)
			seen[c.HashKey()] = true
		}
		if dailyExists {
			updated := setStatusTime(content, "l2_extracted_at", opts.Now)
			if _, err := writeIfChanged(path, updated, opts.DryRun); err != nil {
				return ar, err
			}
		}
	}
	if len(newEntries) > 0 {
		entries = append(entries, newEntries...)
		changed, err := writeIfChanged(reviewPath, renderReview(entries), opts.DryRun)
		if err != nil {
			return ar, err
		}
		if changed {
			ar.Changed = true
			ar.ReviewCandidatesAdded = len(newEntries)
		}
	}
	if err := appendAudit(root.Root, "l2", opts.Until, map[string]any{"review_candidates_added": len(newEntries)}, opts.DryRun); err != nil {
		return ar, err
	}
	return ar, nil
}

func candidatesFromDaily(content string, sourceDate time.Time) []reviewEntry {
	var out []reviewEntry
	add := func(kind, dest, title, body string) {
		body = strings.TrimSpace(body)
		if body == "" || strings.HasPrefix(strings.ToLower(body), "no ") {
			return
		}
		h := hashShort(kind, dest, body, formatDate(sourceDate))
		out = append(out, reviewEntry{
			ID:                  "mem_" + strings.ReplaceAll(formatDate(sourceDate), "-", "") + "_" + h,
			Type:                kind,
			Status:              "candidate",
			Confidence:          "high",
			Sensitivity:         "unknown",
			Scope:               "agent",
			SourceDate:          formatDate(sourceDate),
			Evidence:            []string{"daily:" + formatDate(sourceDate)},
			ProposedDestination: dest,
			Title:               title,
			Body:                body,
		})
	}
	for _, line := range sectionLines(content, "User / Teammate Preferences Observed") {
		add("preference", "USER.md", sentenceTitle(line), line)
	}
	for _, line := range sectionLines(content, "Decisions And Stable Facts") {
		add("stable_fact", "MEMORY.md", sentenceTitle(line), line)
	}
	for _, line := range sectionLines(content, "Temporary State And Follow-ups") {
		add("temporary", "STATE.md", sentenceTitle(line), line)
	}
	return out
}

func hasSemanticDuplicate(entries []reviewEntry, candidate reviewEntry) bool {
	for _, entry := range entries {
		if entry.Type != candidate.Type || entry.ProposedDestination != candidate.ProposedDestination || entry.Scope != candidate.Scope || entry.Sensitivity != candidate.Sensitivity {
			continue
		}
		if sameTopicDuplicate(entry.Topic, candidate.Topic, entry.Type, candidate.Type, entry.Scope, candidate.Scope) {
			return true
		}
		if semanticDuplicate(entry.Body, candidate.Body) {
			return true
		}
	}
	return false
}

func sentenceTitle(s string) string {
	s = strings.TrimSpace(strings.TrimSuffix(s, "."))
	if len(s) > 72 {
		s = strings.TrimSpace(s[:72]) + "..."
	}
	if s == "" {
		return "Memory candidate"
	}
	return s
}

func (e *Engine) runL3(root agentRoot, opts Options) (AgentRunResult, error) {
	ar := AgentRunResult{WorkspaceID: root.WorkspaceID, AgentID: root.AgentID, Root: root.Root}
	reviewPath := filepath.Join(root.Root, "memory", "REVIEW.md")
	reviewBytes, err := os.ReadFile(reviewPath)
	if err != nil {
		if os.IsNotExist(err) {
			evidence, _ := evidenceForAgent(opts, root.WorkspaceID, root.AgentID, opts.Since, opts.Until)
			if _, agentErr := runStageAgent(opts, root, StageL3, stageFilesWithScoped(root.Root, "memory/MEMORY.md", "memory/STATE.md", "notes/decisions.md"), evidence, nil); agentErr != nil {
				return ar, agentErr
			}
			return ar, nil
		}
		return ar, err
	}
	entries, err := parseReview(string(reviewBytes))
	if err != nil {
		return ar, err
	}

	reviewable := make([]reviewEntry, 0, len(entries))
	remaining := make([]reviewEntry, 0, len(entries))
	reviewLimitDeferred := 0
	for _, entry := range entries {
		if entry.Expired(opts.Now) {
			ar.EntriesArchived++
			continue
		}
		if !entryEligibleForL3Review(entry) {
			remaining = append(remaining, entry)
			continue
		}
		if len(reviewable) >= defaultL3ReviewMaxEntries {
			ar.ReviewDeferred++
			reviewLimitDeferred++
			remaining = append(remaining, entry)
			continue
		}
		reviewable = append(reviewable, entry)
	}
	if reviewLimitDeferred > 0 {
		if err := appendAudit(root.Root, "l3", opts.Until, map[string]any{
			"outcome": "deferred", "reason_code": "review_limit", "deferred_count": reviewLimitDeferred,
		}, opts.DryRun); err != nil {
			return ar, err
		}
	}
	if len(reviewable) == 0 {
		return e.finishL3(root, opts, reviewPath, remaining, ar)
	}
	if e.l3Reviewer == nil && opts.StageAgent == nil {
		for _, entry := range reviewable {
			ar.ReviewDeferred++
			trace := deferredL3Trace(entry, "reviewer_unavailable")
			ar.ReviewTraces = appendBoundedReviewTraces(ar.ReviewTraces, []L3ReviewTrace{trace}, defaultL3ReviewMaxEntries)
			if err := appendL3TraceAudit(root.Root, opts.Until, trace, opts.DryRun); err != nil {
				return ar, err
			}
			remaining = append(remaining, entry)
		}
		return e.finishL3(root, opts, reviewPath, remaining, ar)
	}

	inputs := make([]L3ReviewEntry, 0, len(reviewable))
	for _, entry := range reviewable {
		inputs = append(inputs, l3InputFromEntry(entry))
	}
	var output L3ReviewOutput
	var reviewErr error
	if opts.StageAgent != nil {
		evidence, _ := evidenceForAgent(opts, root.WorkspaceID, root.AgentID, opts.Since, opts.Until)
		stageOut, err := runStageAgent(opts, root, StageL3, stageFilesWithScoped(root.Root, "memory/REVIEW.md", "memory/MEMORY.md", "memory/STATE.md", "notes/decisions.md"), evidence, inputs)
		if err != nil {
			reviewErr = err
		} else {
			decisions, err := parseL3ReviewDecisions(stageOut.Content, inputs)
			if err != nil {
				reviewErr = err
			} else {
				output = L3ReviewOutput{Decisions: decisions, Provider: stageOut.Provider, Model: stageOut.Model, Duration: stageOut.Duration}
			}
		}
	} else {
		output, reviewErr = e.l3Reviewer.Review(opts.Context, L3ReviewInput{WorkspaceID: root.WorkspaceID, AgentID: root.AgentID, Entries: inputs})
	}
	if reviewErr != nil {
		for _, entry := range reviewable {
			ar.ReviewDeferred++
			trace := deferredL3Trace(entry, "reviewer_error")
			ar.ReviewTraces = appendBoundedReviewTraces(ar.ReviewTraces, []L3ReviewTrace{trace}, defaultL3ReviewMaxEntries)
			if err := appendL3TraceAudit(root.Root, opts.Until, trace, opts.DryRun); err != nil {
				return ar, err
			}
			remaining = append(remaining, entry)
		}
		return e.finishL3(root, opts, reviewPath, remaining, ar)
	}
	decisions := make(map[string]L3ReviewDecision, len(output.Decisions))
	for _, decision := range output.Decisions {
		decisions[decision.EntryID] = decision
	}
	for entryIndex, entry := range reviewable {
		decision, ok := decisions[entry.ID]
		if !ok {
			ar.ReviewDeferred++
			trace := deferredL3TraceWithOutput(entry, output, "reviewer_omitted")
			ar.ReviewTraces = appendBoundedReviewTraces(ar.ReviewTraces, []L3ReviewTrace{trace}, defaultL3ReviewMaxEntries)
			if err := appendL3TraceAudit(root.Root, opts.Until, trace, opts.DryRun); err != nil {
				return ar, err
			}
			remaining = append(remaining, entry)
			continue
		}
		trace := L3ReviewTrace{EntryID: entry.ID, EntryHash: entry.HashKey(), Route: decision.Route, Confidence: decision.Confidence, Provider: output.Provider, Model: output.Model, PromptVersion: L3ReviewPromptVersion, DurationMS: output.Duration.Milliseconds()}
		sensitivity := normalizeL3Sensitivity(decision.Sensitivity)
		if sensitivity == "" {
			sensitivity = normalizeL3Sensitivity(entry.Sensitivity)
		}
		trace.Sensitivity = sensitivity
		if sensitivity != "none" {
			trace.Outcome = "deferred"
			if sensitivity == "sensitive" {
				trace.ReasonCode = "sensitive_candidate"
			} else {
				trace.ReasonCode = "sensitivity_unknown"
			}
			ar.ReviewDeferred++
			ar.ReviewTraces = appendBoundedReviewTraces(ar.ReviewTraces, []L3ReviewTrace{trace}, defaultL3ReviewMaxEntries)
			if err := appendL3TraceAudit(root.Root, opts.Until, trace, opts.DryRun); err != nil {
				return ar, err
			}
			remaining = append(remaining, entry)
			continue
		}
		decision.Sensitivity = sensitivity
		threshold := opts.ConfidenceThreshold
		if decision.Route == L3RouteDiscard && threshold < minL3DiscardConfidence {
			threshold = minL3DiscardConfidence
		}
		if decision.Error != "" || decision.Confidence < threshold {
			trace.Outcome = "deferred"
			if decision.Error != "" {
				trace.ReasonCode = "invalid_decision"
			} else {
				trace.ReasonCode = "low_confidence"
			}
			ar.ReviewDeferred++
			ar.ReviewTraces = appendBoundedReviewTraces(ar.ReviewTraces, []L3ReviewTrace{trace}, defaultL3ReviewMaxEntries)
			if err := appendL3TraceAudit(root.Root, opts.Until, trace, opts.DryRun); err != nil {
				return ar, err
			}
			remaining = append(remaining, entry)
			continue
		}

		if !curatorModeAllowsDecision(opts.Mode, decision) {
			trace.Outcome = "deferred"
			trace.ReasonCode = "profile_review_required"
			ar.ReviewDeferred++
			ar.ReviewTraces = appendBoundedReviewTraces(ar.ReviewTraces, []L3ReviewTrace{trace}, defaultL3ReviewMaxEntries)
			if err := appendL3TraceAudit(root.Root, opts.Until, trace, opts.DryRun); err != nil {
				return ar, err
			}
			remaining = append(remaining, entry)
			continue
		}

		beforeApply := ar
		beforeApply.ReviewTraces = append([]L3ReviewTrace(nil), ar.ReviewTraces...)
		tx := newFileMutationTransaction(opts.DryRun)
		processed, syncPending, err := e.applyL3Decision(root, opts, entry, decision, &ar, tx)
		if err != nil {
			tx.rollback()
			ar = beforeApply
			trace.Outcome = "deferred"
			trace.ReasonCode = "application_error"
			ar.ReviewDeferred++
			ar.ReviewTraces = appendBoundedReviewTraces(ar.ReviewTraces, []L3ReviewTrace{trace}, defaultL3ReviewMaxEntries)
			if auditErr := appendL3TraceAudit(root.Root, opts.Until, trace, opts.DryRun); auditErr != nil {
				return ar, auditErr
			}
			remaining = append(remaining, entry)
			continue
		}
		if !processed {
			trace.Outcome = "deferred"
			ar.ReviewDeferred++
			remaining = append(remaining, entry)
		} else {
			trace.Outcome = "applied"
			if syncPending {
				trace.Outcome = "applied_sync_pending"
				trace.ReasonCode = "shared_sync_pending"
			}
			ar.EntriesReviewed++
			switch decision.Route {
			case L3RouteMemory:
				ar.MemoryRoutes++
			case L3RouteSkill:
				ar.SkillRoutes++
			case L3RouteSplit:
				ar.SplitRoutes++
			case L3RouteDiscard:
				ar.DiscardRoutes++
			}
		}
		ar.ReviewTraces = appendBoundedReviewTraces(ar.ReviewTraces, []L3ReviewTrace{trace}, defaultL3ReviewMaxEntries)
		if processed {
			nextReview := make([]reviewEntry, 0, len(remaining)+len(reviewable)-entryIndex-1)
			nextReview = append(nextReview, remaining...)
			nextReview = append(nextReview, reviewable[entryIndex+1:]...)
			auditMutation, err := prepareL3TraceAuditMutation(root.Root, opts.Until, trace)
			if err != nil {
				tx.rollback()
				return ar, err
			}
			finalMutations := []fileMutation{{path: reviewPath, content: renderReview(nextReview)}}
			if auditMutation != nil {
				finalMutations = append(finalMutations, *auditMutation)
			}
			if _, err := tx.commit(finalMutations); err != nil {
				tx.rollback()
				ar = beforeApply
				failureTrace := trace
				failureTrace.Outcome = "rolled_back"
				failureTrace.ReasonCode = "review_queue_commit_error"
				_ = appendL3TraceAudit(root.Root, opts.Until, failureTrace, opts.DryRun)
				return ar, err
			}
		} else if err := appendL3TraceAudit(root.Root, opts.Until, trace, opts.DryRun); err != nil {
			tx.rollback()
			return ar, err
		}
	}
	return e.finishL3(root, opts, reviewPath, remaining, ar)
}

func curatorModeAllowsDecision(mode string, decision L3ReviewDecision) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "auto":
		return true
	case "auto_safe":
		return decision.Route == L3RouteMemory && decision.Confidence >= minL3DiscardConfidence
	case "observe", "review":
		return false
	default:
		return false
	}
}

func entryEligibleForL3Review(entry reviewEntry) bool {
	sensitivity := normalizeL3Sensitivity(entry.Sensitivity)
	return strings.EqualFold(strings.TrimSpace(entry.Status), "candidate") &&
		strings.EqualFold(strings.TrimSpace(entry.Confidence), "high") &&
		(sensitivity == "none" || sensitivity == "unknown" || sensitivity == "") &&
		strings.TrimSpace(entry.Body) != ""
}

func l3InputFromEntry(entry reviewEntry) L3ReviewEntry {
	return L3ReviewEntry{ID: entry.ID, Type: entry.Type, Title: entry.Title, Body: entry.Body, Confidence: entry.Confidence, Sensitivity: entry.Sensitivity, Scope: entry.Scope, Evidence: entry.Evidence}
}

func deferredL3Trace(entry reviewEntry, reasonCode string) L3ReviewTrace {
	return L3ReviewTrace{EntryID: entry.ID, EntryHash: entry.HashKey(), Outcome: "deferred", PromptVersion: L3ReviewPromptVersion, ReasonCode: reasonCode}
}

func deferredL3TraceWithOutput(entry reviewEntry, output L3ReviewOutput, reasonCode string) L3ReviewTrace {
	trace := deferredL3Trace(entry, reasonCode)
	trace.Provider = output.Provider
	trace.Model = output.Model
	trace.DurationMS = output.Duration.Milliseconds()
	return trace
}

func l3TraceAuditPayload(trace L3ReviewTrace) map[string]any {
	return map[string]any{
		"entry_id": trace.EntryID, "entry_hash": trace.EntryHash, "route": trace.Route,
		"outcome": trace.Outcome, "confidence": trace.Confidence, "sensitivity": trace.Sensitivity, "provider": trace.Provider,
		"model": trace.Model, "prompt_version": trace.PromptVersion, "duration_ms": trace.DurationMS,
		"reason_code": trace.ReasonCode,
	}
}

func appendL3TraceAudit(root string, planDate time.Time, trace L3ReviewTrace, dryRun bool) error {
	return appendAudit(root, "l3", planDate, l3TraceAuditPayload(trace), dryRun)
}

func prepareL3TraceAuditMutation(root string, planDate time.Time, trace L3ReviewTrace) (*fileMutation, error) {
	payload := l3TraceAuditPayload(trace)
	payload["stage"] = "l3"
	payload["plan_date"] = formatDate(planDate)
	payload["recorded_at"] = time.Now().UTC().Format(time.RFC3339)
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, "memory", "audit", fmt.Sprintf("l3-%s.jsonl", formatDate(planDate)))
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return &fileMutation{path: path, content: string(old) + string(b) + "\n"}, nil
}

func (e *Engine) applyL3Decision(root agentRoot, opts Options, original reviewEntry, decision L3ReviewDecision, ar *AgentRunResult, tx *fileMutationTransaction) (bool, bool, error) {
	original.Sensitivity = decision.Sensitivity
	var mutations []fileMutation
	var memoryPromoted, memoryDuplicate bool
	var sharedCandidate sharedMemoryCandidate
	var shouldShare, sharedChanged bool
	var skillPrepared, skillChanged bool

	memoryEntry := original
	if decision.Memory.Title != "" {
		memoryEntry.Title = decision.Memory.Title
	}
	if decision.Memory.Body != "" {
		memoryEntry.Body = decision.Memory.Body
	}
	if memoryEntry.ProposedDestination == "" {
		memoryEntry.ProposedDestination = defaultMemoryDestination(memoryEntry.Type)
	}
	if decision.Route == L3RouteMemory || decision.Route == L3RouteSplit {
		if memoryEntry.ProposedDestination == "" {
			return false, false, fmt.Errorf("reviewed memory has no destination")
		}
		// A legacy USER.md candidate has no stable member ID in this schema, so
		// it cannot be placed safely. Keep it in REVIEW until a scoped self-review
		// writes users/<member-id>/USER.md with attested provenance.
		if filepath.Base(memoryEntry.ProposedDestination) == "USER.md" {
			return false, false, nil
		}
		destPath := filepath.Join(root.Root, "memory", filepath.Base(memoryEntry.ProposedDestination))
		mutation, promoted, duplicate, err := preparePromoteEntry(destPath, memoryEntry)
		if err != nil {
			return false, false, err
		}
		memoryPromoted, memoryDuplicate = promoted, duplicate
		if mutation != nil {
			mutations = append(mutations, *mutation)
		}
		sharedCandidate, shouldShare = sharedMemoryCandidateForEntry(root, memoryEntry, opts.Now)
		if shouldShare {
			sharedMutations, err := prepareSharedMemoryCandidateMutations(root.Root, sharedCandidate)
			if err != nil {
				return false, false, err
			}
			sharedChanged, err = commitFileMutations(sharedMutations, true)
			if err != nil {
				return false, false, err
			}
			mutations = append(mutations, sharedMutations...)
		}
	}
	if decision.Route == L3RouteSkill || decision.Route == L3RouteSplit {
		decision.Skill = sanitizeL3SkillDraft(decision.Skill)
		if decision.Skill.Name == "" || decision.Skill.Description == "" || decision.Skill.Instructions == "" {
			return false, false, fmt.Errorf("reviewed skill draft is incomplete")
		}
		candidate := skillCandidateForDecision(root, original, decision, opts.Now)
		skillMutations, err := prepareSkillCandidateMutations(root.Root, candidate, renderSkillDraft(decision.Skill))
		if err != nil {
			return false, false, err
		}
		skillChanged, err = commitFileMutations(skillMutations, true)
		if err != nil {
			return false, false, err
		}
		mutations = append(mutations, skillMutations...)
		skillPrepared = true
	}
	changed, err := tx.commit(mutations)
	if err != nil {
		return false, false, err
	}
	ar.Changed = ar.Changed || changed
	if memoryDuplicate {
		ar.DuplicatesMerged++
	}
	if memoryPromoted {
		ar.EntriesPromoted++
	}
	if shouldShare && sharedChanged {
		ar.SharedCandidatesAdded++
	}
	if skillPrepared && skillChanged {
		ar.SkillCandidatesAdded++
	}

	// The durable local queue is the source of truth. The daemon uploads it after
	// this local transaction commits, avoiding an irreversible DB side effect
	// before REVIEW.md and the audit record are safely updated.
	syncPending := shouldShare && !opts.DryRun
	return true, syncPending, nil
}

func defaultMemoryDestination(entryType string) string {
	switch strings.ToLower(strings.TrimSpace(entryType)) {
	case "preference":
		return "USER.md"
	case "temporary", "quota":
		return "STATE.md"
	default:
		return "MEMORY.md"
	}
}

func (e *Engine) finishL3(root agentRoot, opts Options, reviewPath string, remaining []reviewEntry, ar AgentRunResult) (AgentRunResult, error) {
	changed, err := writeIfChanged(reviewPath, renderReview(remaining), opts.DryRun)
	if err != nil {
		return ar, err
	}
	ar.Changed = ar.Changed || changed
	if ar.EntriesReviewed > 0 {
		for _, d := range dateRange(opts.Since, opts.Until) {
			path := filepath.Join(root.Root, "memory", "daily", formatDate(d)+".md")
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			_, _ = writeIfChanged(path, setStatusTime(string(b), "l3_promoted_at", opts.Now), opts.DryRun)
		}
	}
	return ar, nil
}
func preparePromoteEntry(destPath string, entry reviewEntry) (*fileMutation, bool, bool, error) {
	b, err := os.ReadFile(destPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, false, false, err
	}
	content := string(b)
	body := strings.TrimSpace(entry.Body)
	if strings.Contains(normalizeForDedupe(content), normalizeForDedupe(body)) || existingSemanticDuplicate(content, body) {
		return nil, false, true, nil
	}
	block := fmt.Sprintf("\n§\n[type:%s]\n[source:%s]\n[evidence:%s]\n- %s\n", entry.Type, entry.SourceDate, strings.Join(entry.Evidence, ","), body)
	if entry.Type == "temporary" || entry.Type == "quota" {
		if exp := inferExpiresAt(body, entry.SourceDate); exp != "" {
			block = strings.Replace(block, "[evidence:", "[expires_at:"+exp+"]\n[evidence:", 1)
		}
	}
	newContent := strings.TrimRight(content, "\n") + "\n" + block
	return &fileMutation{path: destPath, content: newContent}, true, false, nil
}

var dateRE = regexp.MustCompile(`\b(20\d{2}-\d{2}-\d{2})\b`)

func existingSemanticDuplicate(content, body string) bool {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		if line == "" || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "#") || line == "§" {
			continue
		}
		if semanticDuplicate(line, body) {
			return true
		}
	}
	return false
}

func inferExpiresAt(body, sourceDate string) string {
	if m := dateRE.FindStringSubmatch(body); len(m) == 2 {
		return m[1]
	}
	if sourceDate == "" {
		return ""
	}
	d, err := time.Parse("2006-01-02", sourceDate)
	if err != nil {
		return ""
	}
	return d.AddDate(0, 0, 30).Format("2006-01-02")
}

func (e *Engine) runL4(root agentRoot, opts Options) (AgentRunResult, error) {
	ar := AgentRunResult{WorkspaceID: root.WorkspaceID, AgentID: root.AgentID, Root: root.Root}
	evidence, _ := evidenceForAgent(opts, root.WorkspaceID, root.AgentID, opts.Since, opts.Until)
	if _, agentErr := runStageAgent(opts, root, StageL4, stageFilesWithScoped(root.Root, "memory/REVIEW.md", "memory/MEMORY.md", "memory/STATE.md", "notes/decisions.md"), evidence, nil); agentErr != nil {
		return ar, agentErr
	}
	reviewChanged, archived, err := sweepReview(filepath.Join(root.Root, "memory", "REVIEW.md"), opts.Now, opts.DryRun)
	if err != nil {
		return ar, err
	}
	ar.Changed = ar.Changed || reviewChanged
	ar.EntriesArchived += archived
	stateChanged, stateArchived, err := sweepExpiredState(filepath.Join(root.Root, "memory", "STATE.md"), opts.Now, opts.DryRun)
	if err != nil {
		return ar, err
	}
	ar.Changed = ar.Changed || stateChanged
	ar.EntriesArchived += stateArchived
	for _, name := range []string{"MEMORY.md", "STATE.md"} {
		merged, err := dedupeBulletBlocks(filepath.Join(root.Root, "memory", name), opts.DryRun)
		if err != nil {
			return ar, err
		}
		if merged > 0 {
			ar.Changed = true
			ar.DuplicatesMerged += merged
		}
	}
	projectEntries, _ := os.ReadDir(filepath.Join(root.Root, "projects"))
	for _, entry := range projectEntries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		projectRoot := filepath.Join(root.Root, "projects", entry.Name())
		stateChanged, stateArchived, err := sweepExpiredState(filepath.Join(projectRoot, "STATE.md"), opts.Now, opts.DryRun)
		if err != nil {
			return ar, err
		}
		ar.Changed = ar.Changed || stateChanged
		ar.EntriesArchived += stateArchived
		for _, name := range []string{"MEMORY.md", "STATE.md", "DECISIONS.md"} {
			merged, err := dedupeBulletBlocks(filepath.Join(projectRoot, name), opts.DryRun)
			if err != nil {
				return ar, err
			}
			if merged > 0 {
				ar.Changed = true
				ar.DuplicatesMerged += merged
			}
		}
	}
	if err := appendAudit(root.Root, "l4", opts.Until, map[string]any{"entries_archived": ar.EntriesArchived, "duplicates_merged": ar.DuplicatesMerged}, opts.DryRun); err != nil {
		return ar, err
	}
	return ar, nil
}

func statusHasValue(content, key string) bool {
	prefix := "- " + key + ":"
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), prefix)) != ""
		}
	}
	return false
}

func setStatusTime(content, key string, now time.Time) string {
	prefix := "- " + key + ":"
	stamp := now.UTC().Format(time.RFC3339)
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			lines[i] = prefix + " " + stamp
			return strings.Join(lines, "\n")
		}
	}
	return strings.TrimRight(content, "\n") + "\n" + prefix + " " + stamp + "\n"
}

func sweepReview(path string, now time.Time, dryRun bool) (bool, int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, 0, nil
		}
		return false, 0, err
	}
	entries, err := parseReview(string(b))
	if err != nil {
		return false, 0, err
	}
	var kept []reviewEntry
	archived := 0
	for _, entry := range entries {
		if entry.Status == "promoted" || entry.Status == "rejected" || entry.Status == "expired" || entry.Expired(now) {
			archived++
			continue
		}
		kept = append(kept, entry)
	}
	changed, err := writeIfChanged(path, renderReview(kept), dryRun)
	return changed, archived, err
}

func sweepExpiredState(path string, now time.Time, dryRun bool) (bool, int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, 0, nil
		}
		return false, 0, err
	}
	parts := strings.Split(string(b), "\n§\n")
	if len(parts) <= 1 {
		return false, 0, nil
	}
	kept := []string{strings.TrimRight(parts[0], "\n")}
	archived := 0
	for _, part := range parts[1:] {
		exp := bracketValue(part, "expires_at")
		if exp != "" {
			d, err := time.Parse("2006-01-02", exp)
			if err == nil && dateOnly(d).Before(dateOnly(now)) {
				archived++
				continue
			}
		}
		kept = append(kept, strings.TrimRight(part, "\n"))
	}
	if archived == 0 {
		return false, 0, nil
	}
	content := strings.Join(kept, "\n§\n") + "\n"
	changed, err := writeIfChanged(path, content, dryRun)
	return changed, archived, err
}

func dedupeBulletBlocks(path string, dryRun bool) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	lines := strings.Split(string(b), "\n")
	seen := map[string]bool{}
	var out []string
	removed := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "-") {
			key := normalizeForDedupe(trimmed)
			if key != "" && seen[key] {
				removed++
				continue
			}
			seen[key] = true
		}
		out = append(out, line)
	}
	if removed == 0 {
		return 0, nil
	}
	_, err = writeIfChanged(path, strings.Join(out, "\n"), dryRun)
	return removed, err
}

func bracketValue(content, key string) string {
	prefix := "[" + key + ":"
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) && strings.HasSuffix(line, "]") {
			return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, prefix), "]"))
		}
	}
	return ""
}

func sortEntries(entries []reviewEntry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
}
