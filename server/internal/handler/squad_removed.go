package handler

import "net/http"

// SquadFeatureRemoved is kept as a narrow compatibility endpoint for stale
// clients after the Squad product surface was removed.
func (h *Handler) SquadFeatureRemoved(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusGone, "squad feature has been removed")
}
