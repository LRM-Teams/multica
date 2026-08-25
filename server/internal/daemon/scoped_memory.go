package daemon

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/memorycuration"
	"github.com/multica-ai/multica/server/internal/memoryscope"
)

const (
	executionMemoryBudgetBytes  = 16 * 1024
	agentScopeMemoryBudgetBytes = 6 * 1024
)

type scopedMemoryPaths struct {
	UserDir    string
	ProjectDir string
	ChannelDir string
}

type executionMemoryCandidate struct {
	Memory   execenv.MemoryContextForEnv
	Priority int
	Order    int
}

// prepareExecutionMemory packs turn-scope (user/project/channel) and agent-scope
// memories together. Prefer prepareTurnScopeMemory + prepareAgentScopeMemory at
// call sites that inject them on different surfaces.
func prepareExecutionMemory(agentRoot string, task Task, serverMemories []execenv.MemoryContextForEnv) ([]execenv.MemoryContextForEnv, scopedMemoryPaths) {
	turn, paths := prepareTurnScopeMemory(agentRoot, task, serverMemories)
	agent, _ := prepareAgentScopeMemory(agentRoot, task, serverMemories)
	return append(turn, agent...), paths
}

// prepareTurnScopeMemory loads only member/project/channel memory for the
// current wake (pre-message / RuntimeContext). Agent-scope memory is excluded.
func prepareTurnScopeMemory(agentRoot string, task Task, serverMemories []execenv.MemoryContextForEnv) ([]execenv.MemoryContextForEnv, scopedMemoryPaths) {
	paths := scopedMemoryPathsForTask(agentRoot, task)
	includeUser := taskIncludesUserMemory(task)

	candidates := make([]executionMemoryCandidate, 0, len(serverMemories)+8)
	order := 0
	add := func(priority int, memory execenv.MemoryContextForEnv) {
		memory.Name = strings.TrimSpace(memory.Name)
		memory.Content = strings.TrimSpace(memory.Content)
		if memory.Name == "" || memory.Content == "" {
			return
		}
		if isAgentMemoryScope(memory.Scope) {
			return
		}
		candidates = append(candidates, executionMemoryCandidate{Memory: memory, Priority: priority, Order: order})
		order++
	}
	for _, memory := range serverMemories {
		scope := strings.ToLower(strings.TrimSpace(memory.Scope))
		if isAgentMemoryScope(scope) {
			continue
		}
		if !includeUser && (scope == "user" || scope == "member") {
			continue
		}
		if paths.ProjectDir == "" && scope == "project" {
			continue
		}
		add(serverMemoryPriority(memory), memory)
	}
	addFile := func(priority int, name, path, scope, subjectType, subjectID, template string, maxBytes int) {
		content := readScopedMemoryFile(path, template, maxBytes)
		add(priority, execenv.MemoryContextForEnv{Name: name, Content: content, Scope: scope, SubjectType: subjectType, SubjectID: subjectID})
	}

	if paths.UserDir != "" {
		addFile(0, "Current user preferences", filepath.Join(paths.UserDir, "USER.md"), "user", "member", task.InitiatorID, userMemoryTemplate, 2*1024)
		addFile(4, "Current user relationship context", filepath.Join(paths.UserDir, "RELATIONSHIP.md"), "user", "member", task.InitiatorID, relationshipMemoryTemplate, 1024)
	}
	if paths.ProjectDir != "" {
		addFile(1, "Current project state", filepath.Join(paths.ProjectDir, "STATE.md"), "project", "project", task.ProjectID, projectStateTemplate, 2*1024)
		addFile(2, "Current project decisions", filepath.Join(paths.ProjectDir, "DECISIONS.md"), "project", "project", task.ProjectID, projectDecisionsTemplate, 3*1024)
		addFile(3, "Current project memory", filepath.Join(paths.ProjectDir, "MEMORY.md"), "project", "project", task.ProjectID, projectMemoryTemplate, 4*1024)
	}
	if paths.ChannelDir != "" {
		addFile(4, "Current channel context", filepath.Join(paths.ChannelDir, "CONTEXT.md"), "channel", "channel", task.ChannelID, channelMemoryTemplate, 1536)
	}

	return packExecutionMemoryCandidates(candidates, executionMemoryBudgetBytes), paths
}

// prepareAgentScopeMemory loads only agent-global memory for session-start
// injection (system prompt / AGENTS brief). It is not re-read into per-message
// turn context.
func prepareAgentScopeMemory(agentRoot string, task Task, serverMemories []execenv.MemoryContextForEnv) ([]execenv.MemoryContextForEnv, scopedMemoryPaths) {
	paths := scopedMemoryPathsForTask(agentRoot, task)
	candidates := make([]executionMemoryCandidate, 0, len(serverMemories)+4)
	order := 0
	add := func(priority int, memory execenv.MemoryContextForEnv) {
		memory.Name = strings.TrimSpace(memory.Name)
		memory.Content = strings.TrimSpace(memory.Content)
		if memory.Name == "" || memory.Content == "" {
			return
		}
		if !isAgentMemoryScope(memory.Scope) {
			return
		}
		candidates = append(candidates, executionMemoryCandidate{Memory: memory, Priority: priority, Order: order})
		order++
	}
	for _, memory := range serverMemories {
		if isAgentMemoryScope(memory.Scope) {
			add(6, memory)
		}
	}
	addFile := func(priority int, name, path, scope, subjectType, subjectID, template string, maxBytes int) {
		content := readScopedMemoryFile(path, template, maxBytes)
		add(priority, execenv.MemoryContextForEnv{Name: name, Content: content, Scope: scope, SubjectType: subjectType, SubjectID: subjectID})
	}
	if agentRoot != "" {
		addFile(6, "Agent global memory", filepath.Join(agentRoot, "memory", "MEMORY.md"), "agent", "agent", task.AgentID, agentMemoryTemplate, 2*1024)
		addFile(5, "Agent active state", filepath.Join(agentRoot, "memory", "STATE.md"), "agent", "agent", task.AgentID, agentStateTemplate, 2*1024)
		if todayPath := scopedMemoryTodayPath(agentRoot, time.Now()); todayPath != "" {
			addFile(7, "Today activity summary", todayPath, "agent", "agent", task.AgentID, "", 2*1024)
		}
	}
	return packExecutionMemoryCandidates(candidates, agentScopeMemoryBudgetBytes), paths
}

// mergeExecutionMemories keeps legacy scoped memories and appends extras
// (e.g. graph recall) with dedupe + shared budget. Extras win on content
// collision only when they appear first in the packed result after priority sort.
func mergeExecutionMemories(legacy, extras []execenv.MemoryContextForEnv) []execenv.MemoryContextForEnv {
	if len(extras) == 0 {
		return legacy
	}
	if len(legacy) == 0 {
		return extras
	}
	candidates := make([]executionMemoryCandidate, 0, len(legacy)+len(extras))
	order := 0
	for _, memory := range legacy {
		candidates = append(candidates, executionMemoryCandidate{Memory: memory, Priority: serverMemoryPriority(memory), Order: order})
		order++
	}
	for _, memory := range extras {
		// Graph / supplemental recall stays near the end so durable scoped
		// facts keep budget priority, but still participates in the pack.
		priority := serverMemoryPriority(memory)
		if priority < 5 {
			priority = 5
		}
		candidates = append(candidates, executionMemoryCandidate{Memory: memory, Priority: priority, Order: order})
		order++
	}
	return packExecutionMemoryCandidates(candidates, executionMemoryBudgetBytes)
}

func packExecutionMemoryCandidates(candidates []executionMemoryCandidate, budget int) []execenv.MemoryContextForEnv {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		return candidates[i].Order < candidates[j].Order
	})
	result := make([]execenv.MemoryContextForEnv, 0, len(candidates))
	seen := map[[32]byte]struct{}{}
	remaining := budget
	for _, candidate := range candidates {
		content := strings.TrimSpace(candidate.Memory.Content)
		hash := sha256.Sum256([]byte(strings.ToLower(content)))
		if _, duplicate := seen[hash]; duplicate {
			continue
		}
		seen[hash] = struct{}{}
		if remaining <= 0 {
			break
		}
		content = truncateUTF8Bytes(content, remaining)
		if content == "" {
			continue
		}
		candidate.Memory.Content = content
		result = append(result, candidate.Memory)
		remaining -= len(content)
	}
	return result
}

func isAgentMemoryScope(scope string) bool {
	return strings.ToLower(strings.TrimSpace(scope)) == "agent"
}

func serverMemoryPriority(memory execenv.MemoryContextForEnv) int {
	switch strings.ToLower(strings.TrimSpace(memory.Scope)) {
	case "user", "member":
		return 0
	case "project":
		return 2
	case "channel":
		return 4
	case "workspace", "team":
		return 5
	default:
		return 6
	}
}

func scopedMemoryPathsForTask(agentRoot string, task Task) scopedMemoryPaths {
	if agentRoot == "" {
		return scopedMemoryPaths{}
	}
	paths := scopedMemoryPaths{}
	if task.InitiatorType == "member" && safeScopedMemoryID(task.InitiatorID) && taskIncludesUserMemory(task) {
		paths.UserDir = filepath.Join(agentRoot, "users", task.InitiatorID)
	}
	if safeScopedMemoryID(task.ProjectID) {
		paths.ProjectDir = filepath.Join(agentRoot, "projects", task.ProjectID)
	}
	if safeScopedMemoryID(task.ChannelID) {
		paths.ChannelDir = filepath.Join(agentRoot, "channels", task.ChannelID)
	}
	return paths
}

func taskIncludesUserMemory(task Task) bool {
	return memoryscope.IncludeUserMemory(
		task.ChannelKind,
		task.ChatSessionID,
		task.ChatMessage,
		task.TriggerCommentContent,
		task.QuickCreatePrompt,
	)
}

func safeScopedMemoryID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value && !strings.ContainsAny(value, `/\\`)
}

const (
	userMemoryTemplate         = "# User Preferences\n\nDurable preferences stated by this stable workspace member.\n"
	relationshipMemoryTemplate = "# Relationship Context\n\nDurable collaboration context for this stable workspace member.\n"
	projectMemoryTemplate      = "# Project Memory\n\nStable facts, conventions, and reusable knowledge for this project.\n"
	projectStateTemplate       = "# Project State\n\nCurrent dated status, blockers, active initiatives, and expiring facts for this project.\n"
	projectDecisionsTemplate   = "# Project Decisions\n\nDurable project decisions and their rationale.\n"
	channelMemoryTemplate      = "# Channel Context\n\nNon-secret purpose, language, routing, and collaboration context for this channel.\n"
	agentMemoryTemplate        = "# Agent Memory\n\nSource of truth: Multica agent settings. This file supplements live agent instructions; it does not override them.\n"
	agentStateTemplate         = "# Agent State\n\nCurrent dated state, temporary facts, and active initiatives.\n"
)

func scopedMemoryTodayPath(agentRoot string, now time.Time) string {
	loc, err := time.LoadLocation(memorycuration.DefaultTimezone)
	if err != nil {
		loc = time.FixedZone("CST", 8*60*60)
	}
	return filepath.Join(agentRoot, "memory", "daily", now.In(loc).Format("2006-01-02")+".md")
}

func readScopedMemoryFile(path, template string, maxBytes int) string {
	if strings.TrimSpace(path) == "" || maxBytes <= 0 {
		return ""
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(payload))
	if template != "" && content == strings.TrimSpace(template) {
		return ""
	}
	return truncateUTF8Bytes(content, maxBytes)
}

// graphModeLegacyDailyName is the legacy daily file's injection name. The
// graph owns daily memory in graph mode, so the legacy daily is excluded by
// source even though its scope label is "agent" (spec §8 source whitelist).
const graphModeLegacyDailyName = "Today activity summary"

// filterGraphModeLegacyMemories applies the graph-mode legacy source
// whitelist (spec §8): keep user/member scopes and agent scope except the
// legacy daily summary; drop project, channel, workspace, and team scopes —
// the graph owns those, and a graph miss never falls back to them.
func filterGraphModeLegacyMemories(in []execenv.MemoryContextForEnv) []execenv.MemoryContextForEnv {
	out := make([]execenv.MemoryContextForEnv, 0, len(in))
	for _, m := range in {
		switch strings.ToLower(strings.TrimSpace(m.Scope)) {
		case "user", "member":
			out = append(out, m)
		case "agent":
			if m.Name != graphModeLegacyDailyName {
				out = append(out, m)
			}
		}
	}
	return out
}

// mergeGraphModeExecutionMemory composes graph-mode execution memory:
// whitelisted legacy user/agent memory plus any graph recall blobs. It never
// replaces the legacy user/agent snapshot and never injects legacy
// project/channel/daily memory (spec §8, §13 P0-1/P0-7).
func mergeGraphModeExecutionMemory(agentRoot string, task Task, serverMemories, graphMemories []execenv.MemoryContextForEnv) []execenv.MemoryContextForEnv {
	legacy, _ := prepareExecutionMemory(agentRoot, task, serverMemories)
	merged := filterGraphModeLegacyMemories(legacy)
	return append(merged, graphMemories...)
}

// withoutAgentScopeMemories drops agent-scope rows so they can be injected
// once at session start instead of every turn/pre-message.
func withoutAgentScopeMemories(in []execenv.MemoryContextForEnv) []execenv.MemoryContextForEnv {
	if len(in) == 0 {
		return in
	}
	out := make([]execenv.MemoryContextForEnv, 0, len(in))
	for _, memory := range in {
		if isAgentMemoryScope(memory.Scope) {
			continue
		}
		out = append(out, memory)
	}
	return out
}

// withoutGraphModeLegacyDaily drops the legacy daily summary in graph mode
// (spec §8: the graph owns daily memory).
func withoutGraphModeLegacyDaily(in []execenv.MemoryContextForEnv) []execenv.MemoryContextForEnv {
	if len(in) == 0 {
		return in
	}
	out := make([]execenv.MemoryContextForEnv, 0, len(in))
	for _, memory := range in {
		if memory.Name == graphModeLegacyDailyName {
			continue
		}
		out = append(out, memory)
	}
	return out
}

func truncateUTF8Bytes(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if maxBytes <= 0 || value == "" {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return strings.TrimSpace(value[:cut])
}
