package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	SubmissionID         string         `json:"submission_id"`
	ClientRequestID      string         `json:"client_request_id"`
	ContentHash          string         `json:"content_hash"`
	Kind                 V6ContractKind `json:"kind"`
	Status               string         `json:"status"`
	Replayed             bool           `json:"replayed"`
	StateVersion         int64          `json:"state_version"`
	ThroughEventSequence int64          `json:"through_event_sequence"`
}

type v6SubmissionStore interface {
	AuthorizeV6Submission(context.Context, V6AttemptAccess) (V6SubmissionBinding, error)
	RejectV6DirectorBoundaryAttempt(context.Context, V6AttemptAccess, string) error
	RecordV6Submission(context.Context, V6AttemptAccess, DecodedV6Contract, string) (V6SubmissionOutcome, error)
	SettleV6DirectorSubmission(context.Context, string, string, string) (string, error)
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
	var registry struct {
		PayloadSchemas map[string]json.RawMessage `json:"payload_schemas"`
	}
	if json.Unmarshal(v.schema, &registry) != nil {
		return fmt.Errorf("task schema %q has an invalid frozen validator", schemaID)
	}
	schema := v.schema
	if len(registry.PayloadSchemas) > 0 {
		schema = registry.PayloadSchemas[schemaID]
	} else if schemaID != v.schemaID {
		return fmt.Errorf("task schema %q is not authorized; use %q", schemaID, v.schemaID)
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
		if binding.ExpectedKind == V6ContractDirectorActionProposal && errors.Is(err, ErrInvalidContract) {
			rejectErr := m.store.RejectV6DirectorBoundaryAttempt(ctx, in.V6AttemptAccess, truncateBytes(err.Error(), 4096))
			if rejectErr != nil && !errors.Is(rejectErr, ErrAttemptNotAssigned) {
				slog.Warn("research V6 Director boundary rejection could not be settled",
					"workspace_id", in.WorkspaceID,
					"run_id", in.RunID,
					"work_item_id", in.WorkItemID,
					"attempt_id", in.AttemptID,
					"error", rejectErr,
				)
			}
		}
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
	outcome, err := m.store.RecordV6Submission(ctx, in.V6AttemptAccess, decoded, identity.ClientRequestID)
	if err != nil || decoded.Kind != V6ContractDirectorActionProposal {
		return outcome, err
	}
	status, settleErr := m.store.SettleV6DirectorSubmission(ctx, in.WorkspaceID, in.RunID, outcome.SubmissionID)
	if settleErr != nil {
		// The durable receipt is authoritative. A request-scoped settlement
		// failure must not make the Agent resubmit a committed proposal; the
		// scheduler will retry the same submission by its stable identity.
		slog.Warn("research V6 Director proposal deferred after durable receipt",
			"workspace_id", in.WorkspaceID,
			"run_id", in.RunID,
			"submission_id", outcome.SubmissionID,
			"error", settleErr,
		)
		return outcome, nil
	}
	if status != "" {
		outcome.Status = status
	}
	return outcome, nil
}
