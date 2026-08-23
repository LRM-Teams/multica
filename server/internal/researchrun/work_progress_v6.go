package researchrun

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

// V6WorkProgressEventType is the operational Run Event recorded when an Agent
// reports an in-flight progress note for its active attempt. It is
// bookkeeping only: it never advances the semantic state version and never
// triggers Director cycles.
const V6WorkProgressEventType = "v6_work_progress_reported"

// maxV6WorkProgressTextRunes bounds one progress note. Notes are UI captions,
// not artifacts; anything longer belongs in the result submission.
const maxV6WorkProgressTextRunes = 240

// maxV6WorkProgressStageRunes bounds the optional machine-readable stage key.
const maxV6WorkProgressStageRunes = 64

// maxV6WorkProgressNotesPerAttempt caps notes per attempt so a looping Agent
// cannot flood the Run Event ledger.
const maxV6WorkProgressNotesPerAttempt = 200

const v6WorkProgressChineseFallback = "Agent 正在执行调研任务，稍后会更新中文进度。"

type ReportV6WorkProgressInput struct {
	V6AttemptAccess
	ClientRequestID string
	Text            string
	Stage           string
}

type workProgressStore interface {
	ReportV6WorkProgress(context.Context, ReportV6WorkProgressInput) error
}

type workProgressModule struct{ store workProgressStore }

func (m workProgressModule) Report(ctx context.Context, in ReportV6WorkProgressInput) error {
	if m.store == nil {
		return fmt.Errorf("%w: work progress store unavailable", ErrInvalidContract)
	}
	in.ClientRequestID = strings.TrimSpace(in.ClientRequestID)
	in.Text = strings.TrimSpace(in.Text)
	in.Stage = strings.TrimSpace(in.Stage)
	if in.ClientRequestID == "" || in.Text == "" {
		return fmt.Errorf("%w: incomplete progress report", ErrInvalidContract)
	}
	in.Text = normalizeV6WorkProgressText(in.Text)
	if text := []rune(in.Text); len(text) > maxV6WorkProgressTextRunes {
		in.Text = string(text[:maxV6WorkProgressTextRunes])
	}
	if stage := []rune(in.Stage); len(stage) > maxV6WorkProgressStageRunes {
		return fmt.Errorf("%w: progress stage exceeds %d characters", ErrInvalidContract, maxV6WorkProgressStageRunes)
	}
	return m.store.ReportV6WorkProgress(ctx, in)
}

func normalizeV6WorkProgressText(text string) string {
	for _, value := range text {
		if unicode.Is(unicode.Han, value) {
			return text
		}
	}
	return v6WorkProgressChineseFallback
}
