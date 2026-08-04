package daemon

import (
	"strings"
	"time"
	"unicode/utf8"
)

type taskMessageTrajectoryBuffer struct {
	kind      string
	lineage   string
	text      strings.Builder
	updatedAt time.Time
}

func (b *taskMessageTrajectoryBuffer) append(kind, text, lineage string, now time.Time, emit func(kind, text, lineage string)) {
	if text == "" {
		return
	}
	if b.kind != "" && (b.kind != kind || b.lineage != lineage) {
		b.flush(now, true, emit)
	}
	if b.kind == "" {
		b.kind = kind
		b.lineage = lineage
	}
	b.text.WriteString(text)
	b.updatedAt = now
}

func (b *taskMessageTrajectoryBuffer) flush(now time.Time, force bool, emit func(kind, text, lineage string)) {
	if b.kind == "" || b.text.Len() == 0 {
		return
	}
	if !force && now.Sub(b.updatedAt) < taskMessageTrajectoryCoalesceWindow {
		return
	}
	emit(b.kind, truncateTaskMessageTrajectory(b.text.String()), b.lineage)
	b.kind = ""
	b.lineage = ""
	b.text.Reset()
	b.updatedAt = time.Time{}
}

func truncateTaskMessageTrajectory(text string) string {
	if utf8.RuneCountInString(text) <= taskMessageTrajectoryMaxChars {
		return text
	}
	runes := []rune(text)
	return string(runes[:taskMessageTrajectoryMaxChars]) + "…"
}
