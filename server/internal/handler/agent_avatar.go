package handler

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	agentAvatarSourceAssigned = "assigned"
	agentAvatarSourcePicked   = "picked"
	agentAvatarSourceUploaded = "uploaded"
)

var canonicalAgentAvatarPreset = regexp.MustCompile(`^/agent-avatars/human-(0[1-9]|1[0-9]|2[0-4])\.jpg$`)

// AgentAvatarSelection is write intent, not persisted provenance supplied by
// the client. The server verifies the referenced object/catalog entry and
// derives avatar_url + avatar_source from that trusted fact.
type AgentAvatarSelection struct {
	Kind         string `json:"kind"`
	AttachmentID string `json:"attachment_id,omitempty"`
	PresetURL    string `json:"preset_url,omitempty"`
}

type resolvedAgentAvatar struct {
	Set          bool
	URL          pgtype.Text
	Source       string
	AttachmentID pgtype.UUID
}

func assignedAgentAvatar(rawURL string) resolvedAgentAvatar {
	trimmed := strings.TrimSpace(rawURL)
	return resolvedAgentAvatar{
		Set:    trimmed != "",
		URL:    pgtype.Text{String: trimmed, Valid: trimmed != ""},
		Source: agentAvatarSourceAssigned,
	}
}

func (h *Handler) resolveAgentAvatarSelection(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID pgtype.UUID,
	userID string,
	targetAgentID pgtype.UUID,
	selection *AgentAvatarSelection,
) (resolvedAgentAvatar, bool) {
	if selection == nil {
		return resolvedAgentAvatar{}, true
	}

	switch selection.Kind {
	case agentAvatarSourceUploaded:
		if strings.TrimSpace(selection.AttachmentID) == "" || strings.TrimSpace(selection.PresetURL) != "" {
			writeError(w, http.StatusBadRequest, "uploaded avatar_selection requires only attachment_id")
			return resolvedAgentAvatar{}, false
		}
		attachmentID, ok := parseUUIDOrBadRequest(w, selection.AttachmentID, "avatar attachment id")
		if !ok {
			return resolvedAgentAvatar{}, false
		}
		attachment, err := h.Queries.GetAttachment(r.Context(), db.GetAttachmentParams{
			ID:          attachmentID,
			WorkspaceID: workspaceID,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, "avatar attachment not found")
			return resolvedAgentAvatar{}, false
		}
		if attachment.UploaderType != "member" || uuidToString(attachment.UploaderID) != userID {
			writeError(w, http.StatusForbidden, "avatar attachment must belong to the current user")
			return resolvedAgentAvatar{}, false
		}
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(attachment.ContentType)), "image/") {
			writeError(w, http.StatusBadRequest, "avatar attachment must be an image")
			return resolvedAgentAvatar{}, false
		}
		if strings.TrimSpace(attachment.Url) == "" {
			writeError(w, http.StatusConflict, "avatar attachment has no stored URL")
			return resolvedAgentAvatar{}, false
		}
		var hasChannelMessageReference bool
		if err := h.DB.QueryRow(r.Context(), `
			SELECT EXISTS (
				SELECT 1
				FROM channel_message_attachment
				WHERE attachment_id = $1
				  AND workspace_id = $2
			)
		`, attachment.ID, attachment.WorkspaceID).Scan(&hasChannelMessageReference); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to validate avatar attachment")
			return resolvedAgentAvatar{}, false
		}
		if attachment.IssueID.Valid || attachment.CommentID.Valid || attachment.ChatSessionID.Valid ||
			attachment.ChatMessageID.Valid || attachment.ChannelID.Valid || hasChannelMessageReference {
			writeError(w, http.StatusConflict, "avatar attachment is already bound")
			return resolvedAgentAvatar{}, false
		}
		var boundAgentID pgtype.UUID
		if err := h.DB.QueryRow(r.Context(), `
			SELECT id
			FROM agent
			WHERE avatar_attachment_id = $1
		`, attachment.ID).Scan(&boundAgentID); err != nil && !isNotFound(err) {
			writeError(w, http.StatusInternalServerError, "failed to validate avatar attachment")
			return resolvedAgentAvatar{}, false
		}
		if boundAgentID.Valid && (!targetAgentID.Valid || boundAgentID.Bytes != targetAgentID.Bytes) {
			writeError(w, http.StatusConflict, "avatar attachment is already bound")
			return resolvedAgentAvatar{}, false
		}
		return resolvedAgentAvatar{
			Set:          true,
			URL:          pgtype.Text{String: attachment.Url, Valid: true},
			Source:       agentAvatarSourceUploaded,
			AttachmentID: attachment.ID,
		}, true

	case agentAvatarSourcePicked:
		presetURL := selection.PresetURL
		if strings.TrimSpace(selection.AttachmentID) != "" || presetURL != strings.TrimSpace(presetURL) || !canonicalAgentAvatarPreset.MatchString(presetURL) {
			writeError(w, http.StatusBadRequest, "picked avatar_selection requires a canonical preset_url")
			return resolvedAgentAvatar{}, false
		}
		return resolvedAgentAvatar{
			Set:    true,
			URL:    pgtype.Text{String: presetURL, Valid: true},
			Source: agentAvatarSourcePicked,
		}, true

	default:
		writeError(w, http.StatusBadRequest, "avatar_selection.kind must be uploaded or picked")
		return resolvedAgentAvatar{}, false
	}
}

func applyCreateAgentAvatar(params *db.CreateAgentParams, avatar resolvedAgentAvatar) {
	params.AvatarSource = agentAvatarSourceAssigned
	if !avatar.Set {
		return
	}
	params.AvatarUrl = avatar.URL
	params.AvatarSource = avatar.Source
	params.AvatarAttachmentID = avatar.AttachmentID
}

func applyUpdateAgentAvatar(params *db.UpdateAgentParams, avatar resolvedAgentAvatar) {
	if !avatar.Set {
		return
	}
	params.AvatarSelectionSet = true
	params.AvatarUrl = avatar.URL
	params.AvatarSource = avatar.Source
	params.AvatarAttachmentID = avatar.AttachmentID
}
