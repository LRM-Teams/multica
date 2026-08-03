package handler

import "net/http"

// AutopilotRetired is the LRM-1049 hard-cut: Autopilot is abolished in favor of
// Reminder. Every former Autopilot REST/webhook path returns 410 Gone.
func (h *Handler) AutopilotRetired(w http.ResponseWriter, r *http.Request) {
	writeCodedError(w, http.StatusGone, "autopilot_retired",
		"Autopilot has been removed. Use agent reminders (multica reminder) instead.")
}
