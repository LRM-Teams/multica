package researchrun

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type acceptingV6SecondStage struct{}

func (acceptingV6SecondStage) ValidateV6Payload(string, json.RawMessage) error { return nil }

var v6FixtureContracts = map[string]V6ContractKind{
	"director-brief-v6.example.json":         V6ContractDirectorBrief,
	"director-action-v6.example.json":        V6ContractDirectorActionProposal,
	"director-no-op-v6.example.json":         V6ContractDirectorActionProposal,
	"work-manifest-v6.example.json":          V6ContractWorkManifest,
	"director-work-manifest-v6.example.json": V6ContractWorkManifest,
	"atomic-result-v6.example.json":          V6ContractAtomicResultSubmission,
	"discussion-turn-v6.example.json":        V6ContractDiscussionTurnSubmission,
	"integration-v6.example.json":            V6ContractIntegrationSubmission,
	"report-package-v6.example.json":         V6ContractReportPackageSubmission,
	"projection-snapshot-v6.example.json":    V6ContractProjectionSnapshot,
	"projection-delta-v6.example.json":       V6ContractProjectionDelta,
}

func TestRonaldoV6EmbeddedSchemaMatchesCanonicalDocument(t *testing.T) {
	document := readV6Fixture(t, filepath.Join("..", "..", "..", "docs", "contracts", "research-run-v6.schema.json"))
	if !bytes.Equal(document, researchRunV6DirectorSchema) {
		t.Fatal("embedded Ronaldo V6 schema drifted from the canonical document")
	}
}

func TestRonaldoV6GoldenFixturesMatchTheirStrictRootSchemas(t *testing.T) {
	schema, err := loadV6Schema()
	if err != nil {
		t.Fatal(err)
	}
	for name, kind := range v6FixtureContracts {
		t.Run(name, func(t *testing.T) {
			raw := readV6Fixture(t, filepath.Join("..", "..", "..", "docs", "research", "fixtures", name))
			value, decodeErr := decodeSingleV6JSON(raw)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if validateErr := validateV6SchemaValue(value, schema.Definitions[v6ContractDefinition[kind]], schema.Definitions, "$"); validateErr != nil {
				t.Fatal(validateErr)
			}
		})
	}
}

// Report bosses keep failing the receive gate by hand-copying frozen
// identity: missing citations, swapped node hashes, invalid citation
// enums, and package_hash set to the HTML digest. Decode must accept
// those envelopes so apply can bind server-owned inputs.
func TestDecodeV6ContractAcceptsReportPackageWithBrokenFrozenCopies(t *testing.T) {
	raw := readV6Fixture(t, filepath.Join("..", "..", "..", "docs", "research", "fixtures", "report-package-v6.example.json"))
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}

	cases := map[string]func(map[string]any){
		"missing citations": func(value map[string]any) { delete(value, "citations") },
		"null citations":    func(value map[string]any) { value["citations"] = nil },
		"invalid citation kind": func(value map[string]any) {
			value["citations"] = []any{map[string]any{
				"id": "citation-1", "label": "结果",
				"evidence_refs": []any{map[string]any{"kind": "result_s", "id": "00000000-0000-4000-8000-000000000302"}},
			}}
		},
		"invalid input node id": func(value map[string]any) {
			value["input_nodes"] = []any{map[string]any{
				"kind": "result_s", "id": "not-a-uuid", "version_id": "00000000-0000-4000-8000-000000000507",
				"tier": "S", "content_hash": "sha256:9999999999999999999999999999999999999999999999999999999999999999",
			}}
		},
		"missing package hash": func(value map[string]any) { delete(value, "package_hash") },
		"html digest as package hash": func(value map[string]any) {
			value["package_hash"] = "sha256:33b3384c2545a1b674a6ba2e0e40def56e3688f8bffe186012e0a7a6a06c974a"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeV6Contract(mutateV6Fixture(t, root, mutate), V6ContractReportPackageSubmission, nil); err != nil {
				t.Fatalf("decode report package: %v", err)
			}
		})
	}
}

func TestDecodeV6ContractRejectsBoundaryAndSchemaViolations(t *testing.T) {
	raw := readV6Fixture(t, filepath.Join("..", "..", "..", "docs", "research", "fixtures", "projection-snapshot-v6.example.json"))
	if _, err := DecodeV6Contract(raw, V6ContractProjectionSnapshot, acceptingV6SecondStage{}); err != nil {
		t.Fatalf("decode projection fixture: %v", err)
	}

	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"unknown field":  mutateV6Fixture(t, root, func(value map[string]any) { value["unexpected"] = true }),
		"missing field":  mutateV6Fixture(t, root, func(value map[string]any) { delete(value, "snapshot_id") }),
		"null field":     mutateV6Fixture(t, root, func(value map[string]any) { value["snapshot_id"] = nil }),
		"trailing value": append(append([]byte(nil), raw...), []byte(" {}")...),
		"oversize":       bytes.Repeat([]byte("x"), maxV6ContractBytes+1),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeV6Contract(input, V6ContractProjectionSnapshot, acceptingV6SecondStage{}); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if _, err := DecodeV6Contract(raw, V6ContractProjectionDelta, acceptingV6SecondStage{}); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("wrong root kind error=%v", err)
	}
}

func TestDecodeV6ContractRejectsSelfHashMismatchAndMissingSecondValidator(t *testing.T) {
	raw := readV6Fixture(t, filepath.Join("..", "..", "..", "docs", "research", "fixtures", "atomic-result-v6.example.json"))
	if _, err := DecodeV6Contract(raw, V6ContractAtomicResultSubmission, acceptingV6SecondStage{}); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("fixture placeholder hash must fail closed, error=%v", err)
	}
	if _, err := DecodeV6Contract(raw, V6ContractAtomicResultSubmission, nil); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("missing second-stage validator error=%v", err)
	}
}

func TestV6CanonicalJSONRejectsPostgresUnrepresentableNullCharacter(t *testing.T) {
	_, err := marshalV6CanonicalJSON(map[string]any{
		"content_layers": map[string]any{"content": "before\x00after"},
	})
	if err == nil || !strings.Contains(err.Error(), `field "content_layers": field "content": JSON string contains U+0000`) {
		t.Fatalf("error = %v, want field-level U+0000 rejection", err)
	}
}

func readV6Fixture(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mutateV6Fixture(t *testing.T, source map[string]any, mutate func(map[string]any)) []byte {
	t.Helper()
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]any
	if err = json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	mutate(clone)
	raw, err = json.Marshal(clone)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
