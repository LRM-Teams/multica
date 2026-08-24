// Friction-gated memory (spec: docs/superpowers/specs/2026-08-20-friction-gated-memory-spec.zh-CN.md).
//
// Friction signals are countable events — interruptions, rejected actions,
// retry loops, review rework, provider error streaks — never an LLM score.
// A non-zero friction vector marks a turn whose lesson deserves durable
// memory; a smooth turn stays in the daily index and expires with it.
package memorysignal

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// ActionFriction is an optional agent self-report enriching a hard-counted
	// friction turn with topic/scope semantics (kind: method | infra).
	ActionFriction = "friction"
	// ActionDecision is an optional agent self-report that a decision was
	// finalized this turn.
	ActionDecision = "decision"

	KindDecision = "decision"
	KindFriction = "friction"

	SourceFrictionGuard = "friction_guard"
	SourceDecisionGuard = "decision_guard"

	frictionKindInfra = "infra"
)

// FrictionVector is the per-turn raw count vector. Counts are episodes, not
// raw events: one retry loop of eight identical calls counts once.
type FrictionVector struct {
	HumanCorrection int `json:"human_correction,omitempty"`
	ActionRejected  int `json:"action_rejected,omitempty"`
	RetryLoop       int `json:"retry_loop,omitempty"`
	Rework          int `json:"rework,omitempty"`
	SelfErrorStreak int `json:"self_error_streak,omitempty"`
}

// IsZero reports whether the turn was smooth (no friction observed).
func (v FrictionVector) IsZero() bool {
	return v == FrictionVector{}
}

// Summary lists only the non-zero signals, e.g. "retry_loop×2, human_correction×1".
func (v FrictionVector) Summary() string {
	parts := make([]string, 0, 5)
	appendPart := func(name string, count int) {
		if count > 0 {
			parts = append(parts, fmt.Sprintf("%s×%d", name, count))
		}
	}
	appendPart("human_correction", v.HumanCorrection)
	appendPart("action_rejected", v.ActionRejected)
	appendPart("retry_loop", v.RetryLoop)
	appendPart("rework", v.Rework)
	appendPart("self_error_streak", v.SelfErrorStreak)
	return strings.Join(parts, ", ")
}

const (
	frictionRetryThreshold = 3
	frictionErrorThreshold = 3
)

// FrictionTracker turns a task's provider message stream into a FrictionVector.
// It is fed by the daemon drain loop and is not safe for concurrent use; the
// caller serializes observations (the drain loop already holds its own order).
type FrictionTracker struct {
	vector FrictionVector

	lastToolKey      string
	identicalRun     int
	identicalCounted bool

	errorRun     int
	errorCounted bool
}

func NewFrictionTracker() *FrictionTracker {
	return &FrictionTracker{}
}

// ObserveToolUse records one tool call identified by tool name and a stable
// hash of its input. A run of >= frictionRetryThreshold identical calls counts
// as one retry-loop episode. Tool activity also breaks a provider error streak.
func (t *FrictionTracker) ObserveToolUse(tool, inputHash string) {
	if t == nil {
		return
	}
	t.breakErrorStreak()
	key := strings.TrimSpace(tool) + "\x00" + strings.TrimSpace(inputHash)
	if key == t.lastToolKey {
		t.identicalRun++
	} else {
		t.lastToolKey = key
		t.identicalRun = 1
		t.identicalCounted = false
	}
	if t.identicalRun >= frictionRetryThreshold && !t.identicalCounted {
		t.vector.RetryLoop++
		t.identicalCounted = true
	}
}

// ObserveError records one provider-level error message. A run of
// >= frictionErrorThreshold consecutive errors counts as one streak episode.
func (t *FrictionTracker) ObserveError() {
	if t == nil {
		return
	}
	t.errorRun++
	if t.errorRun >= frictionErrorThreshold && !t.errorCounted {
		t.vector.SelfErrorStreak++
		t.errorCounted = true
	}
}

// ObserveProgress records visible progress (text or thinking), which breaks a
// provider error streak but does not reset tool-retry tracking: retries with
// interleaved commentary are still retries.
func (t *FrictionTracker) ObserveProgress() {
	if t == nil {
		return
	}
	t.breakErrorStreak()
}

func (t *FrictionTracker) breakErrorStreak() {
	t.errorRun = 0
	t.errorCounted = false
}

// Vector returns the counts observed so far.
func (t *FrictionTracker) Vector() FrictionVector {
	if t == nil {
		return FrictionVector{}
	}
	return t.vector
}

// Conservative correction phrasing. "对不对" must not match "不对": RE2 has no
// lookbehind, so require start-of-text or a non-对 rune before 不对.
var correctionRE = regexp.MustCompile(`(?i)((^|[^对])不对|你搞错|搞错了|不是这样|不是让你|别这样改|别再这样|停一下|先停下|重来一遍|重新来过|推倒重来|换个方法|换个思路|改回去|撤销刚才|that's\s+wrong|you're\s+wrong|not\s+what\s+i\s+(asked|meant)|start\s+over|undo\s+that)`)

// LooksLikeCorrection is true when the trigger message corrects or redirects
// the agent's previous work. A correcting trigger marks the current turn as
// running under a human correction (friction-gated memory spec §3).
func LooksLikeCorrection(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	return correctionRE.MatchString(text)
}

// AugmentFrictionFromTrigger folds server-side observable friction into the
// daemon-reported vector: a correcting trigger counts one human_correction
// episode for the turn it steers.
func AugmentFrictionFromTrigger(friction FrictionVector, triggerText string) FrictionVector {
	if LooksLikeCorrection(triggerText) {
		friction.HumanCorrection++
	}
	return friction
}

// Deliberately narrow: only decision-final phrasing, not deliberation
// ("我还没决定" must not match). Agent judgment in-prompt stays primary;
// this is the cheap platform fallback, same philosophy as durableFeedbackRE.
var (
	decisionFinalRE = regexp.MustCompile(`(?i)(就用|就按|就这么|定了|敲定|拍板|决定用|决定采用|最终方案|统一改成|统一用|以后一律|let's\s+go\s+with|we(?:'ll|\s+will)\s+(?:use|go\s+with)|decided\s+(?:to|on)|final\s+decision)`)
	// RE2 has no lookbehind; strip negated/deliberative decision phrases
	// ("还没决定用哪个", "haven't decided") before matching the final form.
	decisionNegatedRE = regexp.MustCompile(`(?i)((还没|没有|没|尚未|未|无法|不能|难以|要不要|是否|不好)\s*(决定|敲定|拍板|定)|(haven't|has\s+not|hasn't|not\s+yet|cannot|can't)\s+decided?)`)
)

// LooksLikeDecisionFinal is true when the text finalizes a decision.
func LooksLikeDecisionFinal(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	cleaned := decisionNegatedRE.ReplaceAllString(text, "")
	return decisionFinalRE.MatchString(cleaned)
}

// DetectMissedDecision returns a decision_guard candidate when a decision was
// finalized this turn (phrase or explicit signal) but no decision-capable
// durable file (project / channel / agent scope) was written. User-preference
// writes do not satisfy a decision.
func DetectMissedDecision(triggerText string, signals []Signal, writes []WriteEntry) (MissedWrite, bool) {
	var decisionSignals []Signal
	for _, s := range signals {
		if s.Action == ActionDecision {
			decisionSignals = append(decisionSignals, s)
		}
	}
	if len(decisionSignals) == 0 && !LooksLikeDecisionFinal(triggerText) {
		return MissedWrite{}, false
	}
	if hasDecisionCapableWrite(writes) {
		return MissedWrite{}, false
	}

	sig := Signal{}
	if len(decisionSignals) > 0 {
		sig = decisionSignals[0]
	}
	scope := firstNonEmpty(sig.Scope, "project")
	topic := NormalizeTopic(sig.Topic)
	if topic == "" {
		topic = "decision_final"
	}
	summary := firstNonEmpty(sig.Summary, compactTrigger(triggerText))
	if summary == "" {
		summary = "A decision was finalized this turn but not written to DECISIONS/CONTEXT"
	}
	return MissedWrite{
		CandidateType: candidateTypeForKind(KindDecision),
		Scope:         scope,
		SubjectID:     strings.TrimSpace(sig.SubjectID),
		Topic:         topic,
		Title:         "Missed decision write",
		Content:       summary,
		Source:        SourceDecisionGuard,
		DedupeKey:     DedupeKey(scope, sig.SubjectID, KindDecision, topic),
	}, true
}

func hasDecisionCapableWrite(writes []WriteEntry) bool {
	for _, w := range writes {
		if !IsDurableWrite(w) {
			continue
		}
		switch strings.TrimSpace(w.ScopeType) {
		case "project", "channel", "agent_global", "agent_notes":
			return true
		case "user":
			continue
		default:
			rel := strings.ReplaceAll(strings.TrimSpace(w.RelPath), "\\", "/")
			if strings.HasSuffix(rel, "/USER.md") || strings.HasSuffix(rel, "/RELATIONSHIP.md") {
				continue
			}
			return true
		}
	}
	return false
}

// DetectFrictionMiss returns a friction_guard candidate when the turn had
// non-zero friction but ended without any durable memory write (daily does
// not count). An agent-declared infra friction signal suppresses the guard:
// infrastructure failures are not method lessons (spec §3.1).
func DetectFrictionMiss(friction FrictionVector, signals []Signal, writes []WriteEntry) (MissedWrite, bool) {
	if friction.IsZero() {
		return MissedWrite{}, false
	}
	var methodSignal Signal
	hasMethodSignal := false
	for _, s := range signals {
		if s.Action != ActionFriction {
			continue
		}
		if s.Kind == frictionKindInfra {
			return MissedWrite{}, false
		}
		if !hasMethodSignal {
			methodSignal = s
			hasMethodSignal = true
		}
	}
	if HasDurableWrite(writes) {
		return MissedWrite{}, false
	}

	scope := firstNonEmpty(methodSignal.Scope, "agent")
	topic := NormalizeTopic(methodSignal.Topic)
	if topic == "" {
		topic = "friction_lesson"
	}
	summary := strings.TrimSpace(methodSignal.Summary)
	if summary == "" {
		summary = "High-friction turn ended without a durable memory write (" + friction.Summary() + ")"
	}
	return MissedWrite{
		CandidateType: candidateTypeForKind(KindFriction),
		Scope:         scope,
		SubjectID:     strings.TrimSpace(methodSignal.SubjectID),
		Topic:         topic,
		Title:         "Missed friction lesson write",
		Content:       summary,
		Source:        SourceFrictionGuard,
		DedupeKey:     DedupeKey(scope, methodSignal.SubjectID, KindFriction, topic),
	}, true
}
