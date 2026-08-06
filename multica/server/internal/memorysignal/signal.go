// Package memorysignal implements the cheap, deterministic half of Multica's
// "remember without a second model call" path:
//
//   - optional co-emitted memory signals (topic/scope/summary)
//   - explicit-feedback phrase matching on the trigger message
//   - missed-write detection when a signal or phrase fires but no durable
//     memory file changed in the same task
//
// Hot-path truth remains agent file writes (USER.md / RELATIONSHIP.md / …).
// Signals are observability + leak detection, not a second write authority.
package memorysignal

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
)

const (
	ActionNone  = "none"
	ActionWrite = "write"

	KindFeedback     = "feedback"
	KindPreference   = "preference"
	KindRelationship = "relationship"
	KindFact         = "fact"
	KindPolicy       = "policy"

	SourceMissedWriteGuard = "missed_write_guard"
	SourceMemorySignal     = "memory_signal"
)

// Signal is the compact structured intent an agent may emit alongside a reply.
// Empty Action defaults to none when parsed.
type Signal struct {
	Action     string `json:"action"`
	Kind       string `json:"kind,omitempty"`
	Scope      string `json:"scope,omitempty"`
	SubjectID  string `json:"subject_id,omitempty"`
	Topic      string `json:"topic,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Importance string `json:"importance,omitempty"`
}

// WriteEntry is one reported durable (or daily) memory file change.
type WriteEntry struct {
	RelPath   string
	ScopeType string
	FileKey   string
}

// MissedWrite is a needs-review candidate the platform should enqueue when the
// trigger clearly asked for durable memory but no durable write landed.
type MissedWrite struct {
	CandidateType string
	Scope         string
	SubjectID     string
	Topic         string
	Title         string
	Content       string
	Source        string
	DedupeKey     string
}

var (
	// Light platform fallback only. Prefer agent judgment + memory signal in-prompt;
	// do not encode every workflow phrase here.
	explicitRememberRE = regexp.MustCompile(`(?i)(记住这个|记住到|记到\s*memory|写进\s*memory|写到\s*memory|记一下|写下来|记下来|remember\s+this|write\s+this\s+down|write\s+it\s+(to|into)\s+memory|don't\s+forget|do\s+not\s+forget)`)
	durableFeedbackRE  = regexp.MustCompile(`(?i)(以后都|以后要|以后得|别再|不要再|下次先|下次要|我不喜欢|不要把|这个项目必须|都必须|必须先|from\s+now\s+on|always\s+|never\s+again|don't\s+ever|do\s+not\s+ever|prefer\s+that|next\s+time|remember\s+that)`)
)

// ParseSignalJSON accepts a single JSON object or {"memory":{...}} wrapper.
func ParseSignalJSON(raw string) (Signal, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Signal{}, false
	}
	var direct Signal
	if err := json.Unmarshal([]byte(raw), &direct); err == nil && (direct.Action != "" || direct.Topic != "" || direct.Summary != "") {
		return normalizeSignal(direct), true
	}
	var wrapped struct {
		Memory Signal `json:"memory"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapped); err == nil && (wrapped.Memory.Action != "" || wrapped.Memory.Topic != "" || wrapped.Memory.Summary != "") {
		return normalizeSignal(wrapped.Memory), true
	}
	return Signal{}, false
}

// ParseSignalJSONL parses one signal per non-empty line.
func ParseSignalJSONL(raw string) []Signal {
	lines := strings.Split(raw, "\n")
	out := make([]Signal, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if sig, ok := ParseSignalJSON(line); ok {
			out = append(out, sig)
		}
	}
	return out
}

func normalizeSignal(s Signal) Signal {
	s.Action = strings.ToLower(strings.TrimSpace(s.Action))
	if s.Action == "" {
		if strings.TrimSpace(s.Summary) != "" || strings.TrimSpace(s.Topic) != "" {
			s.Action = ActionWrite
		} else {
			s.Action = ActionNone
		}
	}
	s.Kind = strings.ToLower(strings.TrimSpace(s.Kind))
	s.Scope = strings.ToLower(strings.TrimSpace(s.Scope))
	s.SubjectID = strings.TrimSpace(s.SubjectID)
	s.Topic = NormalizeTopic(s.Topic)
	s.Summary = strings.TrimSpace(s.Summary)
	s.Importance = strings.ToLower(strings.TrimSpace(s.Importance))
	return s
}

// LooksLikeExplicitRemember is true when the user made remembering the task.
func LooksLikeExplicitRemember(text string) bool {
	return explicitRememberRE.MatchString(strings.TrimSpace(text))
}

// LooksLikeDurableFeedback is true for clear standing preference / correction phrases.
func LooksLikeDurableFeedback(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if LooksLikeExplicitRemember(text) {
		return true
	}
	return durableFeedbackRE.MatchString(text)
}

// HasDurableWrite reports whether any reported write is a durable memory file
// (USER / RELATIONSHIP / MEMORY / notes / project / channel). Daily alone does not count.
func HasDurableWrite(writes []WriteEntry) bool {
	for _, w := range writes {
		if IsDurableWrite(w) {
			return true
		}
	}
	return false
}

// IsDurableWrite classifies one reported file change. Any of these paths count
// as "remembered" for missed-write checks — not only USER.md.
func IsDurableWrite(w WriteEntry) bool {
	scope := strings.TrimSpace(w.ScopeType)
	key := strings.ToUpper(strings.TrimSpace(w.FileKey))
	rel := strings.ReplaceAll(strings.TrimSpace(w.RelPath), "\\", "/")
	switch scope {
	case "user":
		return key == "USER" || key == "RELATIONSHIP" || strings.HasSuffix(rel, "/USER.md") || strings.HasSuffix(rel, "/RELATIONSHIP.md")
	case "agent_global":
		return key == "MEMORY" || strings.HasSuffix(rel, "memory/MEMORY.md")
	case "agent_notes":
		return key == "AGENTS" || key == "RELATIONSHIP_MAP" || key == "WORK_LOG" ||
			strings.HasPrefix(rel, "notes/")
	case "project":
		return key == "MEMORY" || key == "DECISIONS" || key == "STATE"
	case "channel":
		return key == "CONTEXT"
	default:
		// Path fallback when scope classification is missing.
		switch {
		case strings.HasSuffix(rel, "/USER.md"), strings.HasSuffix(rel, "/RELATIONSHIP.md"),
			rel == "memory/MEMORY.md", strings.HasPrefix(rel, "notes/"),
			strings.Contains(rel, "/MEMORY.md"), strings.Contains(rel, "/DECISIONS.md"),
			strings.HasSuffix(rel, "/CONTEXT.md"):
			return true
		default:
			return false
		}
	}
}

// ShouldReportEvenWithoutWrites tells the daemon to POST an empty write report
// so the server can run the missed-write guard.
func ShouldReportEvenWithoutWrites(triggerText string, signals []Signal) bool {
	for _, s := range signals {
		if s.Action == ActionWrite {
			return true
		}
	}
	return LooksLikeDurableFeedback(triggerText)
}

// DetectMissedWrite returns a candidate when trigger/signal asked for durable
// memory but no durable file write landed. Daily-only writes still count as miss.
func DetectMissedWrite(triggerText string, signals []Signal, writes []WriteEntry, defaultSubjectID string) (MissedWrite, bool) {
	if HasDurableWrite(writes) {
		return MissedWrite{}, false
	}

	var writeSignals []Signal
	for _, s := range signals {
		if s.Action == ActionWrite {
			writeSignals = append(writeSignals, s)
		}
	}

	triggerHit := LooksLikeDurableFeedback(triggerText)
	if len(writeSignals) == 0 && !triggerHit {
		return MissedWrite{}, false
	}

	source := SourceMissedWriteGuard
	sig := Signal{}
	if len(writeSignals) > 0 {
		sig = writeSignals[0]
		source = SourceMemorySignal
	}

	scope := firstNonEmpty(sig.Scope, "user")
	kind := firstNonEmpty(sig.Kind, KindFeedback)
	topic := sig.Topic
	if topic == "" {
		topic = InferTopic(triggerText, sig.Summary)
	}
	summary := firstNonEmpty(sig.Summary, compactTrigger(triggerText))
	if summary == "" {
		summary = "User expressed a durable preference that was not written this turn"
	}
	subject := firstNonEmpty(sig.SubjectID, defaultSubjectID)
	title := "Missed durable memory write"
	if LooksLikeExplicitRemember(triggerText) {
		title = "Missed explicit remember request"
	}

	return MissedWrite{
		CandidateType: candidateTypeForKind(kind),
		Scope:         scope,
		SubjectID:     subject,
		Topic:         topic,
		Title:         title,
		Content:       summary,
		Source:        source,
		DedupeKey:     DedupeKey(scope, subject, kind, topic),
	}, true
}

func candidateTypeForKind(kind string) string {
	switch kind {
	case KindRelationship:
		return "relationship"
	case KindPreference, KindFeedback:
		return "user_preference"
	default:
		return "follow_up"
	}
}

func compactTrigger(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > 240 {
		return string(runes[:240]) + "…"
	}
	return text
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// NormalizeTopic turns free text into a short stable topic key.
func NormalizeTopic(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			b.WriteRune(r)
			lastUnderscore = false
		case r == '_' || r == '-' || unicode.IsSpace(r):
			if b.Len() > 0 && !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

// InferTopic picks a coarse topic when the agent did not supply one.
func InferTopic(trigger, summary string) string {
	text := strings.ToLower(trigger + " " + summary)
	switch {
	case strings.Contains(text, "进度") || strings.Contains(text, "feedback") || strings.Contains(text, "progress") || strings.Contains(text, "汇报"):
		return "progress_feedback"
	case strings.Contains(text, "指出") || strings.Contains(text, "直接") || strings.Contains(text, "赞同") || strings.Contains(text, "disagree") || strings.Contains(text, "critique"):
		return "direct_critique"
	case strings.Contains(text, "隐私") || strings.Contains(text, "个人信息") || strings.Contains(text, "privacy") || strings.Contains(text, "pii"):
		return "privacy_redaction"
	case strings.Contains(text, "中文") || strings.Contains(text, "english") || strings.Contains(text, "语言") || strings.Contains(text, "language"):
		return "language_preference"
	case strings.Contains(text, "mr") || strings.Contains(text, "merge request") || strings.Contains(text, "rebase") || strings.Contains(text, "pipeline") || strings.Contains(text, "提mr") || strings.Contains(text, "开mr") || strings.Contains(text, "建mr"):
		return "mr_workflow"
	default:
		if LooksLikeExplicitRemember(trigger) {
			return "explicit_remember"
		}
		return "durable_feedback"
	}
}

// DedupeKey builds the deterministic identity for a memory candidate.
func DedupeKey(scope, subjectID, kind, topic string) string {
	scope = strings.ToLower(strings.TrimSpace(scope))
	kind = strings.ToLower(strings.TrimSpace(kind))
	topic = NormalizeTopic(topic)
	subjectID = strings.TrimSpace(subjectID)
	if scope == "" {
		scope = "user"
	}
	if kind == "" {
		kind = KindFeedback
	}
	if topic == "" {
		topic = "unspecified"
	}
	if subjectID == "" || scope == "agent" || scope == "workspace" || scope == "team" {
		return scope + "+" + kind + "+" + topic
	}
	return scope + ":" + subjectID + "+" + kind + "+" + topic
}
