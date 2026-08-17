package researchrun

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const maxV6ContractBytes = 2 << 20

//go:embed research_run_v6_director.schema.json
var researchRunV6DirectorSchema []byte

// V6ContractKind is the closed root-envelope vocabulary of the Ronaldo V6
// protocol. It intentionally contains both server-produced and submitted
// envelopes; the caller decides which subset is valid for its boundary.
type V6ContractKind string

const (
	V6ContractDirectorBrief            V6ContractKind = "director_brief"
	V6ContractDirectorActionProposal   V6ContractKind = "director_action_proposal"
	V6ContractWorkManifest             V6ContractKind = "work_manifest"
	V6ContractAtomicResultSubmission   V6ContractKind = "atomic_result_submission"
	V6ContractDiscussionTurnSubmission V6ContractKind = "discussion_turn_submission"
	V6ContractIntegrationSubmission    V6ContractKind = "integration_submission"
	V6ContractReportPackageSubmission  V6ContractKind = "report_package_submission"
	V6ContractProjectionSnapshot       V6ContractKind = "projection_snapshot"
	V6ContractProjectionDelta          V6ContractKind = "projection_delta"
)

var v6ContractDefinition = map[V6ContractKind]string{
	V6ContractDirectorBrief:            "director_brief",
	V6ContractDirectorActionProposal:   "director_action_proposal",
	V6ContractWorkManifest:             "work_manifest",
	V6ContractAtomicResultSubmission:   "atomic_result_submission",
	V6ContractDiscussionTurnSubmission: "discussion_turn_submission",
	V6ContractIntegrationSubmission:    "integration_submission",
	V6ContractReportPackageSubmission:  "report_package_submission",
	V6ContractProjectionSnapshot:       "projection_snapshot",
	V6ContractProjectionDelta:          "projection_delta",
}

// V6SecondStageValidator validates the deliberately open payload owned by a
// named task schema. The root decoder never treats an open payload as an escape
// from the Work Item's expected schema.
type V6SecondStageValidator interface {
	ValidateV6Payload(schemaID string, payload json.RawMessage) error
}

// DecodedV6Contract is a schema-validated envelope plus its canonical replay
// identity. Canonical contains the complete envelope. ContentHash excludes the
// envelope's declared self-hash field when the contract defines one.
type DecodedV6Contract struct {
	Kind        V6ContractKind
	Envelope    json.RawMessage
	Canonical   []byte
	ContentHash string
}

type v6SchemaDocument struct {
	Definitions map[string]json.RawMessage `json:"$defs"`
}

type v6RootIdentity struct {
	ContractKind  V6ContractKind `json:"contract_kind"`
	SchemaVersion int            `json:"schema_version"`
}

// DecodeV6Contract performs the common V6 boundary work: size/trailing-value
// rejection, root selection, strict schema validation, second-stage dispatch,
// canonicalization and declared hash verification.
func DecodeV6Contract(raw []byte, expected V6ContractKind, secondStage V6SecondStageValidator) (DecodedV6Contract, error) {
	if len(raw) == 0 || len(raw) > maxV6ContractBytes {
		return DecodedV6Contract{}, fmt.Errorf("%w: V6 payload size must be between 1 and %d bytes", ErrInvalidContract, maxV6ContractBytes)
	}

	value, err := decodeSingleV6JSON(raw)
	if err != nil {
		return DecodedV6Contract{}, fmt.Errorf("%w: decode V6 contract: %v", ErrInvalidContract, err)
	}
	root, ok := value.(map[string]any)
	if !ok {
		return DecodedV6Contract{}, fmt.Errorf("%w: V6 contract root must be an object", ErrInvalidContract)
	}
	identityRaw, err := json.Marshal(root)
	if err != nil {
		return DecodedV6Contract{}, fmt.Errorf("%w: encode V6 identity: %v", ErrInvalidContract, err)
	}
	var identity v6RootIdentity
	if err = json.Unmarshal(identityRaw, &identity); err != nil || identity.ContractKind == "" || identity.SchemaVersion != 6 {
		return DecodedV6Contract{}, fmt.Errorf("%w: V6 contract_kind and schema_version=6 are required", ErrInvalidContract)
	}
	if identity.ContractKind != expected {
		return DecodedV6Contract{}, fmt.Errorf("%w: expected %q, received %q", ErrInvalidContract, expected, identity.ContractKind)
	}
	definitionName, exists := v6ContractDefinition[identity.ContractKind]
	if !exists {
		return DecodedV6Contract{}, fmt.Errorf("%w: unknown V6 contract kind %q", ErrInvalidContract, identity.ContractKind)
	}

	schema, err := loadV6Schema()
	if err != nil {
		return DecodedV6Contract{}, err
	}
	definition, exists := schema.Definitions[definitionName]
	if !exists {
		return DecodedV6Contract{}, fmt.Errorf("%w: V6 schema definition %q is missing", ErrInvalidContract, definitionName)
	}
	if err = validateV6SchemaValue(value, definition, schema.Definitions, "$"); err != nil {
		return DecodedV6Contract{}, fmt.Errorf("%w: %v", ErrInvalidContract, err)
	}
	if err = validateV6SecondStage(root, identity.ContractKind, secondStage); err != nil {
		return DecodedV6Contract{}, fmt.Errorf("%w: %v", ErrInvalidContract, err)
	}

	canonical, err := marshalV6CanonicalJSON(value)
	if err != nil {
		return DecodedV6Contract{}, fmt.Errorf("%w: canonicalize V6 contract: %v", ErrInvalidContract, err)
	}
	hashRoot := cloneV6Object(root)
	selfHashField := v6SelfHashField(identity.ContractKind)
	declaredHash := ""
	if selfHashField != "" {
		declaredHash, _ = hashRoot[selfHashField].(string)
		delete(hashRoot, selfHashField)
	}
	hashCanonical, err := marshalV6CanonicalJSON(hashRoot)
	if err != nil {
		return DecodedV6Contract{}, fmt.Errorf("%w: canonicalize V6 hash input: %v", ErrInvalidContract, err)
	}
	contentHash := ArtifactContentHashFromCanonicalJSON(hashCanonical)
	if declaredHash != "" && declaredHash != contentHash {
		return DecodedV6Contract{}, fmt.Errorf("%w: %s does not match canonical payload", ErrInvalidContract, selfHashField)
	}
	return DecodedV6Contract{Kind: identity.ContractKind, Envelope: append(json.RawMessage(nil), raw...), Canonical: canonical, ContentHash: contentHash}, nil
}

func decodeSingleV6JSON(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("trailing JSON value")
		}
		return nil, fmt.Errorf("trailing JSON: %w", err)
	}
	return value, nil
}

func loadV6Schema() (v6SchemaDocument, error) {
	var schema v6SchemaDocument
	if err := json.Unmarshal(researchRunV6DirectorSchema, &schema); err != nil {
		return v6SchemaDocument{}, fmt.Errorf("%w: embedded V6 schema is invalid: %v", ErrInvalidContract, err)
	}
	return schema, nil
}

func v6SelfHashField(kind V6ContractKind) string {
	switch kind {
	case V6ContractWorkManifest:
		return "manifest_hash"
	case V6ContractAtomicResultSubmission, V6ContractIntegrationSubmission:
		return "content_hash"
	default:
		return ""
	}
}

func cloneV6Object(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func validateV6SecondStage(root map[string]any, kind V6ContractKind, validator V6SecondStageValidator) error {
	var schemaField, payloadField string
	switch kind {
	case V6ContractDirectorActionProposal:
		// Each action owns its payload schema and is dispatched independently.
		actions, _ := root["actions"].([]any)
		for index, rawAction := range actions {
			action, _ := rawAction.(map[string]any)
			if err := dispatchV6SecondStage(action, "payload_schema", "payload", validator); err != nil {
				return fmt.Errorf("%w: actions[%d]: %v", ErrInvalidContract, index, err)
			}
		}
		return nil
	case V6ContractAtomicResultSubmission:
		schemaField, payloadField = "task_specific_schema", "task_specific_payload"
	default:
		return nil
	}
	return dispatchV6SecondStage(root, schemaField, payloadField, validator)
}

func dispatchV6SecondStage(owner map[string]any, schemaField, payloadField string, validator V6SecondStageValidator) error {
	schemaID, _ := owner[schemaField].(string)
	payload, present := owner[payloadField]
	if strings.TrimSpace(schemaID) == "" || !present {
		return fmt.Errorf("second-stage schema and payload are required")
	}
	if validator == nil {
		return fmt.Errorf("second-stage validator %q is unavailable", schemaID)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode second-stage payload: %w", err)
	}
	if err = validator.ValidateV6Payload(schemaID, raw); err != nil {
		return fmt.Errorf("second-stage schema %q: %w", schemaID, err)
	}
	return nil
}
