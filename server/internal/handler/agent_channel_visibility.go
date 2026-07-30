package handler

import (
	"fmt"
	"net/http"
	"strings"
)

const (
	agentVisibilityWorkspace = "workspace"
	agentVisibilityPrivate   = "private"
)

// normalizeAgentVisibility returns the canonical visibility value or an error
// when the value is not one of workspace|private. Empty input is not
// normalized here — callers that want a default must set it explicitly.
//
// "channel" was retired with task #908's channel-scoped-agent cut: its only
// producer (the Beckham group-manager agent) was itself retired in #1436
// ("cut over group managers to channel roles") the day before, leaving no
// live path that ever set visibility=channel. Existing legacy rows are
// migrated to private by the migration that narrows the agent_visibility_check
// CHECK constraint to match.
func normalizeAgentVisibility(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	switch v {
	case agentVisibilityWorkspace, agentVisibilityPrivate:
		return v, nil
	default:
		return "", fmt.Errorf("visibility must be workspace or private")
	}
}

// agentVisibilityBinding is the resolved visibility after applying a
// create/update request field. home_channel_id is no longer a thing this
// binding carries — task #908 retired the channel-scoped-agent mechanism
// (see normalizeAgentVisibility's doc comment) — so any home_channel_id the
// client still sends is now simply rejected as unsupported.
type agentVisibilityBinding struct {
	Visibility string
}

// resolveAgentVisibilityBinding validates the visibility field. home_channel_id
// is no longer accepted at all (task #908 channel-scoped-agent retirement).
func (h *Handler) resolveAgentVisibilityBinding(
	w http.ResponseWriter,
	visibility string,
	homeChannelID *string,
	homeChannelProvided bool,
) (agentVisibilityBinding, bool) {
	vis, err := normalizeAgentVisibility(visibility)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return agentVisibilityBinding{}, false
	}
	if homeChannelProvided && homeChannelID != nil && strings.TrimSpace(*homeChannelID) != "" {
		writeError(w, http.StatusBadRequest, "home_channel_id is no longer supported")
		return agentVisibilityBinding{}, false
	}
	return agentVisibilityBinding{Visibility: vis}, true
}
