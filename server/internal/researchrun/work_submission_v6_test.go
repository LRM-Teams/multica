package researchrun

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBoundV6SecondStageNamesAuthorizedSchemaOnMismatch(t *testing.T) {
	validator := boundV6SecondStage{
		schemaID: "research.result.verify.v1",
		schema:   json.RawMessage(`{"type":"object"}`),
	}

	err := validator.ValidateV6Payload("research.cross_validation.v1", json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), `use "research.result.verify.v1"`) {
		t.Fatalf("error=%v", err)
	}
}

func TestBoundV6SecondStageReadsFrozenSchemaRegistry(t *testing.T) {
	validator := boundV6SecondStage{
		schemaID: "research.result.verify.v1",
		schema: json.RawMessage(`{
			"payload_schemas": {
				"research.result.verify.v1": {
					"type": "object",
					"additionalProperties": false,
					"required": ["summary"],
					"properties": {"summary": {"type": "string", "minLength": 1}}
				}
			}
		}`),
	}

	if err := validator.ValidateV6Payload("research.result.verify.v1", json.RawMessage(`{"summary":"verified"}`)); err != nil {
		t.Fatal(err)
	}
}
