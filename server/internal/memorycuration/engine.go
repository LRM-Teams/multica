package memorycuration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Engine struct {
	l3Reviewer L3Reviewer
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
	roots, err := discoverAgentRoots(opts.WorkspacesRoot, opts.WorkspaceID, opts.AgentIDs, opts.AllAgents)
	if err != nil {
		return res, err
	}
	for _, root := range roots {
		ar := AgentRunResult{WorkspaceID: root.WorkspaceID, AgentID: root.AgentID, Root: root.Root}
		res.AgentsScanned++
		if !opts.DryRun {
			if err := ensureMemoryRoot(root.Root); err != nil {
				res.Errors = append(res.Errors, AgentError{WorkspaceID: root.WorkspaceID, AgentID: root.AgentID, Stage: stage, Error: err.Error()})
				res.AgentResults = append(res.AgentResults, ar)
				continue
			}
		}
		stages := []Stage{stage}
		if stage == StageAll {
			stages = []Stage{StageL1, StageL2, StageL3, StageL4}
		}
		for _, st := range stages {
			var sr AgentRunResult
			var err error
			switch st {
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
			if err != nil {
				res.Errors = append(res.Errors, AgentError{WorkspaceID: root.WorkspaceID, AgentID: root.AgentID, Stage: st, Error: err.Error()})
				continue
			}
			mergeAgentRunResult(&ar, sr)
		}
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
		res.AgentResults = append(res.AgentResults, ar)
	}
	return res, nil
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
		evidence, err := CollectDBEvidence(opts.Context, opts.DB, root.WorkspaceID, root.AgentID, d, d)
		if err != nil {
			return ar, err
		}
		ar.EvidenceCollected += len(evidence)
		if workLog == "" && scratch == "" && len(evidence) == 0 {
			if err := appendAudit(root.Root, "l1", d, map[string]any{"outcome": "no_relevant_activity", "timezone": opts.Timezone}, opts.DryRun); err != nil {
				return ar, err
			}
			continue
		}
		content := dailyContent(root, d, workLog, scratch, evidence, opts.Now, opts.Timezone)
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
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return ar, err
		}
		content := string(b)
		if !opts.Force && statusHasValue(content, "l2_extracted_at") {
			continue
		}
		candidates := candidatesFromDaily(content, d)
		for _, c := range candidates {
			if seen[c.HashKey()] || hasSemanticDuplicate(entries, c) || hasSemanticDuplicate(newEntries, c) {
				ar.DuplicatesMerged++
				continue
			}
			newEntries = append(newEntries, c)
			seen[c.HashKey()] = true
		}
		updated := setStatusTime(content, "l2_extracted_at", opts.Now)
		if _, err := writeIfChanged(path, updated, opts.DryRun); err != nil {
			return ar, err
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
			Sensitivity:         "none",
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
	if e.l3Reviewer == nil {
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
	output, reviewErr := e.l3Reviewer.Review(opts.Context, L3ReviewInput{WorkspaceID: root.WorkspaceID, AgentID: root.AgentID, Entries: inputs})
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
	for _, entry := range reviewable {
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
		if decision.Error != "" || decision.Confidence < minL3ActionConfidence || (decision.Route == L3RouteDiscard && decision.Confidence < minL3DiscardConfidence) {
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

		processed, syncPending, err := e.applyL3Decision(root, opts, entry, decision, &ar)
		if err != nil {
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
		if err := appendL3TraceAudit(root.Root, opts.Until, trace, opts.DryRun); err != nil {
			return ar, err
		}
	}
	return e.finishL3(root, opts, reviewPath, remaining, ar)
}

func entryEligibleForL3Review(entry reviewEntry) bool {
	return strings.EqualFold(strings.TrimSpace(entry.Status), "candidate") && strings.EqualFold(strings.TrimSpace(entry.Confidence), "high") && strings.EqualFold(strings.TrimSpace(entry.Sensitivity), "none") && strings.TrimSpace(entry.Body) != ""
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

func appendL3TraceAudit(root string, planDate time.Time, trace L3ReviewTrace, dryRun bool) error {
	return appendAudit(root, "l3", planDate, map[string]any{
		"entry_id": trace.EntryID, "entry_hash": trace.EntryHash, "route": trace.Route,
		"outcome": trace.Outcome, "confidence": trace.Confidence, "provider": trace.Provider,
		"model": trace.Model, "prompt_version": trace.PromptVersion, "duration_ms": trace.DurationMS,
		"reason_code": trace.ReasonCode,
	}, dryRun)
}

func (e *Engine) applyL3Decision(root agentRoot, opts Options, original reviewEntry, decision L3ReviewDecision, ar *AgentRunResult) (bool, bool, error) {
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
	changed, err := commitFileMutations(mutations, opts.DryRun)
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

	syncPending := false
	if shouldShare && !opts.DryRun {
		synced, syncErr := syncSharedMemoryCandidate(opts.Context, opts.DB, root, sharedCandidate)
		if syncErr != nil {
			syncPending = true
		} else if synced {
			ar.SharedCandidatesSynced++
		}
	}
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

func promoteEntry(destPath string, entry reviewEntry, dryRun bool) (promoted bool, duplicate bool, err error) {
	mutation, promoted, duplicate, err := preparePromoteEntry(destPath, entry)
	if err != nil || mutation == nil {
		return promoted, duplicate, err
	}
	_, err = commitFileMutations([]fileMutation{*mutation}, dryRun)
	return promoted, duplicate, err
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
	for _, name := range []string{"USER.md", "MEMORY.md", "STATE.md"} {
		merged, err := dedupeBulletBlocks(filepath.Join(root.Root, "memory", name), opts.DryRun)
		if err != nil {
			return ar, err
		}
		if merged > 0 {
			ar.Changed = true
			ar.DuplicatesMerged += merged
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
