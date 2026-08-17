package researchrun

import (
	"context"
	"encoding/json"
)

type V6DispatchIntentPayload struct {
	Access   V6AttemptAccess `json:"access"`
	Manifest json.RawMessage `json:"manifest"`
	Mission  string          `json:"mission"`
}

type v6DispatchStore interface {
	PrepareV6Dispatches(context.Context, int) (int, error)
	CompleteV6DispatchOutbox(context.Context, string, string, string) error
}
