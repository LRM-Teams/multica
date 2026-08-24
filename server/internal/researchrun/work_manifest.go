package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type V6AttemptAccess struct {
	WorkspaceID string `json:"workspace_id"`
	RunID       string `json:"run_id"`
	WorkItemID  string `json:"work_item_id"`
	AttemptID   string `json:"attempt_id"`
	AgentID     string `json:"agent_id"`
	InboxTaskID string `json:"inbox_task_id"`
}

func (a *V6AttemptAccess) UnmarshalJSON(data []byte) error {
	type typed V6AttemptAccess
	var current typed
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	var legacy struct {
		WorkspaceID string `json:"WorkspaceID"`
		RunID       string `json:"RunID"`
		WorkItemID  string `json:"WorkItemID"`
		AttemptID   string `json:"AttemptID"`
		AgentID     string `json:"AgentID"`
		InboxTaskID string `json:"InboxTaskID"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	*a = V6AttemptAccess{
		WorkspaceID: firstFilled(current.WorkspaceID, legacy.WorkspaceID),
		RunID:       firstFilled(current.RunID, legacy.RunID),
		WorkItemID:  firstFilled(current.WorkItemID, legacy.WorkItemID),
		AttemptID:   firstFilled(current.AttemptID, legacy.AttemptID),
		AgentID:     firstFilled(current.AgentID, legacy.AgentID),
		InboxTaskID: firstFilled(current.InboxTaskID, legacy.InboxTaskID),
	}
	return nil
}

func firstFilled(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

type V6WorkManifest struct {
	Bytes json.RawMessage
	ETag  string
}

type workManifestStore interface {
	LoadV6WorkManifest(context.Context, V6AttemptAccess) (V6WorkManifest, error)
}

type workManifestModule struct{ store workManifestStore }

func (m workManifestModule) Get(ctx context.Context, access V6AttemptAccess) (V6WorkManifest, error) {
	if m.store == nil || strings.TrimSpace(access.WorkspaceID) == "" || strings.TrimSpace(access.RunID) == "" ||
		strings.TrimSpace(access.WorkItemID) == "" || strings.TrimSpace(access.AttemptID) == "" || strings.TrimSpace(access.AgentID) == "" {
		return V6WorkManifest{}, fmt.Errorf("%w: incomplete attempt identity", ErrInvalidContract)
	}
	manifest, err := m.store.LoadV6WorkManifest(ctx, access)
	if err != nil {
		return V6WorkManifest{}, err
	}
	decoded, err := DecodeV6Contract(manifest.Bytes, V6ContractWorkManifest, nil)
	if err != nil {
		return V6WorkManifest{}, err
	}
	if decoded.ContentHash != manifest.ETag {
		return V6WorkManifest{}, fmt.Errorf("%w: stored manifest hash mismatch", ErrInvalidContract)
	}
	return manifest, nil
}
