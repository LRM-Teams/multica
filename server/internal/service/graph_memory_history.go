// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"math"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// graphMemoryHistoryProvider is the production memorygraph.HistoryProvider
// (design Q18): it wraps MessageStore.MessagesForTaskInRange (same access
// path as the diagnosis tool server, diagnosis_tools.go) and returns the
// downstream task's persisted conversation as judge-visible messages.
//
// The judge lives server-side (graph_memory_judge.go) precisely because this
// history needs DB access the daemon does not have. One provider instance is
// bound to one task; the trace id parameter is unused beyond satisfying the
// interface (the task binding is established when the judge kick arrives).
type graphMemoryHistoryProvider struct {
	msgs   MessageStore
	taskID string
}

// NewGraphMemoryHistoryProvider returns a HistoryProvider over the full
// recorded message range of taskID.
func NewGraphMemoryHistoryProvider(msgs MessageStore, taskID string) memorygraph.HistoryProvider {
	return &graphMemoryHistoryProvider{msgs: msgs, taskID: taskID}
}

// DownstreamHistory returns the task's persisted messages mapped to
// judge-visible role/content pairs. Tool outputs are appended to the
// content so the judge sees what the downstream agent saw.
func (p *graphMemoryHistoryProvider) DownstreamHistory(ctx context.Context, _ string) ([]memorygraph.Message, error) {
	rows, err := p.msgs.MessagesForTaskInRange(ctx, p.taskID, 0, math.MaxInt32)
	if err != nil {
		return nil, err
	}
	out := make([]memorygraph.Message, 0, len(rows))
	for _, m := range rows {
		out = append(out, memorygraph.Message{
			Role:    m.Type,
			Content: taskMessageText(m),
		})
	}
	return out, nil
}

// taskMessageText renders one task_message row as judge-visible text:
// content plus (for tool results) the recorded output.
func taskMessageText(m db.TaskMessage) string {
	content := ""
	if m.Content.Valid {
		content = m.Content.String
	}
	if m.Output.Valid && m.Output.String != "" {
		if content != "" {
			content += "\n"
		}
		content += m.Output.String
	}
	return content
}
