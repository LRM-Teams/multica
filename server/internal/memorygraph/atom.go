// SPDX-License-Identifier: Apache-2.0

package memorygraph

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// AtomTrustClass grades how trustworthy a tool citation is for a stated fact.
// Server code stamps the class; proposers never set it.
type AtomTrustClass string

const (
	// AtomTrustNone marks atoms citing no tool at all (e.g. user statements).
	AtomTrustNone AtomTrustClass = "none"
	// AtomTrustReadOnly marks atoms whose every citation observed state
	// without mutating it.
	AtomTrustReadOnly AtomTrustClass = "trusted_read_only"
	// AtomTrustMutation marks atoms citing at least one state-mutating tool.
	AtomTrustMutation AtomTrustClass = "mutation"
	// AtomTrustUnknown marks unclassified tools; it fails safe and is never
	// treated as trusted.
	AtomTrustUnknown AtomTrustClass = "unknown"
)

// Atom is the minimal independently searchable/consumable statement derived
// from one published Segment (spec 8.1). Identity and scope fields are
// stamped by the server from the Segment; only body/kind/source refs come
// from a proposer.
type Atom struct {
	AtomID            string
	SegmentID         string
	Body              string
	Kind              string
	SourceMessageSeqs []int32
	SourceTool        string
	ToolTrustClass    string
	ContentHash       string
	ArtifactRef       string
	Visibility        string
	ChannelID         string
	ProjectID         string
	PublishSeq        int64
}

// readOnlyTools observe state without side effects. Aliases mirror the Raft
// tool semantic table so provider slugs classify consistently.
var readOnlyTools = map[string]bool{
	"read_file": true, "glob": true, "grep": true, "web_fetch": true,
	"web_search": true, "read_history": true, "search_messages": true,
	"check_messages": true, "wait_for_message": true, "view_file": true,
	"list_tasks": true, "list_issues": true, "get_issue": true,
	"search_issues": true, "list_issue_comments": true, "list_server": true,
	"list_reminders": true, "list_channel_members": true,
}

// mutationTools change user- or system-visible state.
var mutationTools = map[string]bool{
	"write_file": true, "edit_file": true, "bash": true, "todo_write": true,
	"send_message": true, "create_tasks": true, "claim_tasks": true,
	"unclaim_task": true, "update_task_status": true, "comment_issue": true,
	"delete_issue_comment": true, "upload_file": true, "add_channel_member": true,
	"join_channel": true, "leave_channel": true, "schedule_reminder": true,
	"snooze_reminder": true, "update_reminder": true, "cancel_reminder": true,
	"log_reminder": true, "collab_tool_call": true, "exec": true,
}

// toolAliases fold provider slugs to the canonical Raft tool semantic,
// mirroring the daemon's alias table without importing it.
var toolAliases = map[string]string{
	"read": "read_file", "readfile": "read_file", "file_read": "read_file",
	"open": "read_file", "cat": "read_file",
	"rg": "grep", "search": "grep", "search_code": "grep",
	"search_files": "glob",
	"webfetch":     "web_fetch", "fetchurl": "web_fetch", "fetch_url": "web_fetch",
	"websearch": "web_search", "searchweb": "web_search", "search_web": "web_search",
	"read_messages": "read_history",
	"write":         "write_file", "writefile": "write_file", "file_write": "write_file",
	"create": "write_file", "createfile": "write_file", "create_file": "write_file",
	"edit": "edit_file", "editfile": "edit_file", "file_edit": "edit_file",
	"file_change": "edit_file", "strreplacefile": "edit_file",
	"str_replace": "edit_file", "multi_edit": "edit_file", "patch_apply": "edit_file",
	"shell": "bash", "sh": "bash", "zsh": "bash", "exec": "bash",
	"exec_command": "bash", "command": "bash", "command_execution": "bash",
	"run_terminal_command": "bash", "run_shell_command": "bash", "terminal": "bash",
	"todowrite": "todo_write", "set_todo_list": "todo_write", "settodolist": "todo_write",
	"message_send": "send_message",
}

// normalizeToolSlug folds a provider tool slug to a comparable form, mirroring
// the daemon's canonical semantic folding without importing it.
func normalizeToolSlug(raw string) string {
	tool := strings.ToLower(strings.TrimSpace(raw))
	tool = strings.TrimPrefix(tool, "mcp_chat_")
	if strings.HasPrefix(tool, "mcp__") {
		parts := strings.Split(tool, "__")
		tool = parts[len(parts)-1]
	}
	tool = strings.TrimSpace(strings.TrimPrefix(tool, "tool:"))
	if canonical, ok := toolAliases[tool]; ok {
		return canonical
	}
	return tool
}

// ClassifyToolTrust stamps the trust class for one tool citation.
func ClassifyToolTrust(tool string) AtomTrustClass {
	normalized := normalizeToolSlug(tool)
	if normalized == "" {
		return AtomTrustNone
	}
	if readOnlyTools[normalized] {
		return AtomTrustReadOnly
	}
	if mutationTools[normalized] {
		return AtomTrustMutation
	}
	return AtomTrustUnknown
}

// AtomTrustRank orders classes from strongest to weakest; weaker citations
// dominate an atom's overall class.
func AtomTrustRank(class AtomTrustClass) int {
	switch class {
	case AtomTrustReadOnly:
		return 3
	case AtomTrustMutation:
		return 2
	case AtomTrustUnknown:
		return 1
	default:
		return 0
	}
}

// WeakestToolTrust returns the weakest class among the citations; with no
// citations the class is none.
func WeakestToolTrust(classes ...AtomTrustClass) AtomTrustClass {
	weakest := AtomTrustNone
	for _, class := range classes {
		if AtomTrustRank(class) < AtomTrustRank(weakest) || weakest == AtomTrustNone {
			weakest = class
		}
	}
	return weakest
}

// AtomKind is the closed semantic vocabulary for independently searchable
// atoms. NodeRole remains a separate structural classification for graph nodes.
type AtomKind string

const (
	AtomFact        AtomKind = "fact"
	AtomEvent       AtomKind = "event"
	AtomInstruction AtomKind = "instruction"
	AtomPreference  AtomKind = "preference"
	AtomDecision    AtomKind = "decision"
	AtomConstraint  AtomKind = "constraint"
	AtomFallback    AtomKind = "fallback"
)

// ValidAtomKind enumerates the closed kind set a proposer may choose from.
func ValidAtomKind(kind string) bool {
	switch AtomKind(kind) {
	case AtomFact, AtomEvent, AtomInstruction, AtomPreference, AtomDecision, AtomConstraint, AtomFallback:
		return true
	default:
		return false
	}
}

// LegacyAtomKindAction names the one permitted handling for a retired kind
// label. Silent mapping onto a current kind is forbidden at every boundary.
type LegacyAtomKindAction string

const (
	// LegacyAtomKindExplicitChoice requires the owning authority to pick one
	// of AllowedTargets deliberately; the pick is recorded as a backfill
	// decision with checkpoint/reason, never inferred.
	LegacyAtomKindExplicitChoice LegacyAtomKindAction = "explicit_choice"
	// LegacyAtomKindCandidateReEvaluation requires the multi-step body to be
	// re-evaluated into one or more current-kind atoms (or fall back). A
	// Skill candidate may only arise from the independent curation flow,
	// never from the retired label itself (spec §4).
	LegacyAtomKindCandidateReEvaluation LegacyAtomKindAction = "candidate_re_evaluation"
)

// LegacyAtomKindDisposition describes how a retired kind label must be
// handled when it arrives from a legacy source (import, older extractor
// output, backfill). Dispositions exist so callers route legacy labels
// through an explicit decision instead of guessing a mapping.
type LegacyAtomKindDisposition struct {
	LegacyKind     string
	Action         LegacyAtomKindAction
	AllowedTargets []AtomKind
}

// LegacyAtomKindDispositionFor returns the disposition for a retired kind
// label, or false for anything else (current kinds and unknown values have
// no disposition — unknown values are simply invalid).
func LegacyAtomKindDispositionFor(kind string) (LegacyAtomKindDisposition, bool) {
	switch AtomKind(kind) {
	case "rule":
		return LegacyAtomKindDisposition{
			LegacyKind:     "rule",
			Action:         LegacyAtomKindExplicitChoice,
			AllowedTargets: []AtomKind{AtomInstruction, AtomConstraint},
		}, true
	case "procedure":
		return LegacyAtomKindDisposition{
			LegacyKind: "procedure",
			Action:     LegacyAtomKindCandidateReEvaluation,
			AllowedTargets: []AtomKind{
				AtomFact, AtomEvent, AtomInstruction, AtomPreference,
				AtomDecision, AtomConstraint, AtomFallback,
			},
		}, true
	default:
		return LegacyAtomKindDisposition{}, false
	}
}

// NormalizeAtomBody collapses runs of whitespace and trims, so formatting
// drift cannot fork atom identity.
func NormalizeAtomBody(body string) string {
	return strings.Join(strings.Fields(body), " ")
}

// StableAtomID derives the segment-scoped, content-addressed atom identity:
// the same candidate body (after normalization), kind, sorted sequence refs,
// and source tool inside one segment always hash to the same id — stable
// across retries and deduplicating repeated proposals.
func StableAtomID(segmentID, kind, body string, seqs []int32, sourceTool string) string {
	sorted := append([]int32(nil), seqs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	parts := make([]string, 0, len(sorted)+4)
	parts = append(parts, segmentID, kind, NormalizeAtomBody(body), normalizeToolSlug(sourceTool))
	for _, seq := range sorted {
		parts = append(parts, strconv.Itoa(int(seq)))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return "atom-" + hex.EncodeToString(sum[:])[:24]
}
