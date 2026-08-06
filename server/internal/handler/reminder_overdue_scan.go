package handler

import (
	"context"
)

// ProcessOverdueReminders is retained as a scheduler compatibility point.
// The legacy Activity timeline was the sole consumer of these observations;
// Runner activity intentionally exposes only Runner-produced execution facts.
func (h *Handler) ProcessOverdueReminders(ctx context.Context, limit int) (int, error) {
	return 0, nil
}
