package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var ErrV6IdempotencyConflict = errors.New("research V6 idempotency conflict")

type V6SubmissionInput struct {
	V6AttemptAccess
	Raw json.RawMessage
}

type V6SubmissionBinding struct {
	ManifestID, ManifestHash string
	ExpectedKind             V6ContractKind
	TaskSchemaID             string
	TaskSchema               json.RawMessage
}

type V6SubmissionOutcome struct {
	SubmissionID, ClientRequestID, ContentHash string
	Kind                                       V6ContractKind
	Status                                     string
	Replayed                                   bool
}

type v6SubmissionStore interface {
	AuthorizeV6Submission(context.Context, V6AttemptAccess) (V6SubmissionBinding, error)
	RecordV6Submission(context.Context, V6AttemptAccess, DecodedV6Contract, string) (V6SubmissionOutcome, error)
}

type v6SubmissionModule struct{ store v6SubmissionStore }

type v6SubmissionIdentity struct {
	ClientRequestID string `json:"client_request_id"`
	WorkspaceID     string `json:"workspace_id"`
	RunID           string `json:"run_id"`
	WorkItemID      string `json:"work_item_id"`
	AttemptID       string `json:"attempt_id"`
	ManifestID      string `json:"manifest_id"`
	ManifestHash    string `json:"manifest_hash"`
	AgentID         string `json:"agent_id"`
}

type boundV6SecondStage struct {
	schemaID string
	schema   json.RawMessage
}

func (v boundV6SecondStage) ValidateV6Payload(schemaID string, payload json.RawMessage) error {
	if strings.TrimSpace(schemaID) == "" {
		return fmt.Errorf("task schema %q is not authorized", schemaID)
	}
	schema := v.schema
	if schemaID != v.schemaID {
		var registry struct {
			PayloadSchemas map[string]json.RawMessage `json:"payload_schemas"`
		}
		if json.Unmarshal(v.schema, &registry) != nil {
			return fmt.Errorf("task schema %q is not authorized", schemaID)
		}
		schema = registry.PayloadSchemas[schemaID]
	}
	if len(schema) == 0 || string(schema) == "null" || string(schema) == "{}" {
		return fmt.Errorf("task schema %q has no frozen validator", schemaID)
	}
	value, err := decodeSingleV6JSON(payload)
	if err != nil {
		return err
	}
	definitions, err := loadV6Schema()
	if err != nil {
		return err
	}
	return validateV6SchemaValue(value, schema, definitions.Definitions, "$.task_specific_payload")
}

func (m v6SubmissionModule) Submit(ctx context.Context, in V6SubmissionInput) (V6SubmissionOutcome, error) {
	if m.store == nil || len(in.Raw) == 0 {
		return V6SubmissionOutcome{}, fmt.Errorf("%w: submission is required", ErrInvalidContract)
	}
	binding, err := m.store.AuthorizeV6Submission(ctx, in.V6AttemptAccess)
	if err != nil {
		return V6SubmissionOutcome{}, err
	}
	validator := boundV6SecondStage{schemaID: binding.TaskSchemaID, schema: binding.TaskSchema}
	decoded, err := DecodeV6Contract(in.Raw, binding.ExpectedKind, validator)
	if err != nil {
		return V6SubmissionOutcome{}, err
	}
	var identity v6SubmissionIdentity
	if err = json.Unmarshal(decoded.Envelope, &identity); err != nil {
		return V6SubmissionOutcome{}, fmt.Errorf("%w: submission identity", ErrInvalidContract)
	}
	if identity.WorkspaceID != in.WorkspaceID || identity.RunID != in.RunID || identity.WorkItemID != in.WorkItemID ||
		identity.AttemptID != in.AttemptID || identity.ManifestID != binding.ManifestID || identity.ManifestHash != binding.ManifestHash {
		return V6SubmissionOutcome{}, ErrAttemptNotAssigned
	}
	if identity.AgentID != "" && identity.AgentID != in.AgentID {
		return V6SubmissionOutcome{}, ErrAttemptNotAssigned
	}
	return m.store.RecordV6Submission(ctx, in.V6AttemptAccess, decoded, identity.ClientRequestID)
}
