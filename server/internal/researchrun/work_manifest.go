package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type V6AttemptAccess struct {
	WorkspaceID, RunID, WorkItemID, AttemptID, AgentID, InboxTaskID string
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
