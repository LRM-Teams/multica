package service

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	noteBriefContextKey     = "note_brief"
	noteBriefContextVersion = 1
)

// NoteBrief is the immutable task-context pointer to a product note page used
// as a Worker brief (S2-C1). Page body is not stored here — load under ACL at
// dispatch / claim time. See docs/notes-editor-worker-contract.md.
type NoteBrief struct {
	Version int    `json:"version"`
	PageID  string `json:"page_id"`
	Title   string `json:"title,omitempty"`
}

// WithNoteBrief preserves every existing task context key while adding the
// note brief pointer. Empty page_id is rejected.
func WithNoteBrief(contextJSON []byte, brief NoteBrief) ([]byte, error) {
	if err := validateNoteBrief(brief); err != nil {
		return nil, err
	}
	contextMap := map[string]json.RawMessage{}
	if len(contextJSON) > 0 {
		if err := json.Unmarshal(contextJSON, &contextMap); err != nil {
			return nil, err
		}
	}
	if contextMap == nil {
		contextMap = map[string]json.RawMessage{}
	}
	raw, err := json.Marshal(brief)
	if err != nil {
		return nil, err
	}
	contextMap[noteBriefContextKey] = raw
	return json.Marshal(contextMap)
}

// NoteBriefFromContext returns false when the key is absent. Invalid payloads
// return present=true with an error so callers fail closed.
func NoteBriefFromContext(contextJSON []byte) (NoteBrief, bool, error) {
	if len(contextJSON) == 0 {
		return NoteBrief{}, false, nil
	}
	var contextMap map[string]json.RawMessage
	if err := json.Unmarshal(contextJSON, &contextMap); err != nil {
		return NoteBrief{}, false, err
	}
	raw, ok := contextMap[noteBriefContextKey]
	if !ok {
		return NoteBrief{}, false, nil
	}
	var brief NoteBrief
	if err := json.Unmarshal(raw, &brief); err != nil {
		return NoteBrief{}, true, err
	}
	if err := validateNoteBrief(brief); err != nil {
		return NoteBrief{}, true, err
	}
	return brief, true, nil
}

func validateNoteBrief(brief NoteBrief) error {
	if brief.Version != noteBriefContextVersion {
		return fmt.Errorf("unsupported note brief version %d", brief.Version)
	}
	if strings.TrimSpace(brief.PageID) == "" {
		return fmt.Errorf("note brief page_id is empty")
	}
	return nil
}
