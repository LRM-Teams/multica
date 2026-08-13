package handler

import (
	"context"
	"errors"
	"time"

	"github.com/multica-ai/multica/server/internal/service"
)

// ReapMixedRLQuiescence is the handler-owned scheduling boundary for the
// persisted mixed-RL lifecycle. It deliberately contains no timer state: the
// service evaluates database timestamps and freezes through the ledger lock.
func (h *Handler) ReapMixedRLQuiescence(ctx context.Context, now time.Time) error {
	if h == nil || h.Queries == nil || h.TxStarter == nil {
		return errors.New("mixed-RL quiescence dependencies are unavailable")
	}
	_, err := service.NewMixedRLFreezeService(h.Queries, h.TxStarter).ReapMixedRLQuiescence(ctx, now)
	return err
}
