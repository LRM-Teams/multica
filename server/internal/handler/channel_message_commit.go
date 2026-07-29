package handler

import (
	"context"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// prepareCanonicalChannelMessageCommit remains the canonical message-service
// hook. The retired server-managed group patrol no longer needs a transactional
// side effect here.
func (h *Handler) prepareCanonicalChannelMessageCommit(
	context.Context,
	db.DBTX,
	service.CanonicalChannelMessage,
) (func(context.Context), error) {
	return nil, nil
}
