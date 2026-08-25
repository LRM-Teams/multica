package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type V6DispatchIntentPayload struct {
	Access   V6AttemptAccess `json:"access"`
	Manifest json.RawMessage `json:"manifest"`
	Mission  string          `json:"mission"`
}

func parseV6DispatchAccessIDs(payload []byte) (attemptID, workItemID string, err error) {
	var intent V6DispatchIntentPayload
	if err = json.Unmarshal(payload, &intent); err != nil {
		return "", "", fmt.Errorf("%w: dispatch intent", ErrInvalidContract)
	}
	attemptID = strings.TrimSpace(intent.Access.AttemptID)
	workItemID = strings.TrimSpace(intent.Access.WorkItemID)
	if attemptID == "" || workItemID == "" {
		return "", "", fmt.Errorf("%w: dispatch access ids", ErrInvalidContract)
	}
	return attemptID, workItemID, nil
}

type v6DispatchStore interface {
	PrepareV6Dispatches(context.Context, int) (int, error)
	CompleteV6DispatchOutbox(context.Context, string, string, string) error
}
