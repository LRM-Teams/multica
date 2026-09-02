// SPDX-License-Identifier: Apache-2.0

package spreadsheet

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/skillevolution"
)

const fixturePath = "testdata/manifest_v1.json"

func loadFixtureManifest(t *testing.T) Manifest {
	t.Helper()
	raw, err := os.ReadFile(fixturePath)
	require.NoError(t, err)
	manifest, err := DecodeManifestStrict(raw)
	require.NoError(t, err)
	return manifest
}

func validManifest() Manifest {
	return Manifest{
		ContractKind:     "spreadsheet_assertion_manifest",
		SchemaVersion:    1,
		ManifestID:       "manifest-1",
		WorkspaceID:      "workspace-1",
		DatasetID:        "bench-1",
		DatasetVersion:   "2026-09-01",
		LineageSplit:     "regression|hidden|fresh_shadow",
		EnvironmentKey:   "env-1",
		EvaluatorVersion: "evaluator-1",
		Input: WorkbookRef{
			ArtifactHash:       "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			LineageFingerprint: "fp-1",
			StorageRef:         "blob://bench/input.xlsx",
		},
		AllowedSheets:    []string{"Ledger", "Summary"},
		AllowedRanges:    []RangeDecl{{Sheet: "Ledger", A1: "A1:F200"}},
		OutputPath:       "out/result.xlsx",
		AllowedMutations: []MutationDimension{MutateValue, MutateFormula},
		Assertions: []AssertionSpec{
			{
				AssertionID:   "assert-total-value",
				Kind:          AssertValue,
				Sheet:         "Summary",
				CellRange:     "B2:B2",
				Severity:      SeverityHard,
				Required:      true,
				OracleRefHash: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			},
			{
				AssertionID:   "assert-output-path",
				Kind:          AssertOutputPath,
				Severity:      SeverityHard,
				Required:      true,
				OracleRefHash: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			},
		},
		PreservedInvariants:     []string{"sheet order unchanged"},
		UnsupportedCapabilities: []string{"macros"},
		RecalculationPolicy:     RecalcPinnedEngine,
		CreatedAt:               time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
	}
}

// TestManifestFixtureCoversAllAssertionKinds keeps the fixture honest: it
// must exercise value, formula, type, style, structure, and output_path
// assertions so decoder or validation drift on any kind breaks here
// first (plan Slice 0.1).
func TestManifestFixtureCoversAllAssertionKinds(t *testing.T) {
	manifest := loadFixtureManifest(t)
	seen := map[AssertionKind]bool{}
	for _, assertion := range manifest.Assertions {
		seen[assertion.Kind] = true
	}
	for _, kind := range []AssertionKind{AssertValue, AssertFormula, AssertType, AssertStyle, AssertStructure, AssertOutputPath} {
		assert.True(t, seen[kind], "fixture must cover %s assertion", kind)
	}
	assert.Equal(t, "manifest-spreadsheet-1", manifest.ManifestID)
	assert.Equal(t, RecalcPinnedEngineCompatShadow, manifest.RecalculationPolicy)
}

func TestManifestValidateAcceptsAndRejects(t *testing.T) {
	require.NoError(t, validManifest().Validate())

	unknownMutation := validManifest()
	unknownMutation.AllowedMutations = []MutationDimension{"macro"}
	assert.ErrorIs(t, unknownMutation.Validate(), skillevolution.ErrInvalidContract)

	outsideSheet := validManifest()
	outsideSheet.AllowedRanges = append(outsideSheet.AllowedRanges, RangeDecl{Sheet: "Secret", A1: "A1:B2"})
	assert.ErrorIs(t, outsideSheet.Validate(), skillevolution.ErrInvalidContract)

	assertionOutsideSheet := validManifest()
	assertionOutsideSheet.Assertions[0].Sheet = "Secret"
	assert.ErrorIs(t, assertionOutsideSheet.Validate(), skillevolution.ErrInvalidContract)

	escapingPath := validManifest()
	escapingPath.OutputPath = "../escape.xlsx"
	assert.ErrorIs(t, escapingPath.Validate(), skillevolution.ErrInvalidContract)

	absolutePath := validManifest()
	absolutePath.OutputPath = "/tmp/escape.xlsx"
	assert.ErrorIs(t, absolutePath.Validate(), skillevolution.ErrInvalidContract)

	noRequired := validManifest()
	for i := range noRequired.Assertions {
		noRequired.Assertions[i].Required = false
	}
	assert.ErrorIs(t, noRequired.Validate(), skillevolution.ErrInvalidContract)

	missingRange := validManifest()
	missingRange.Assertions[0].CellRange = ""
	assert.ErrorIs(t, missingRange.Validate(), skillevolution.ErrInvalidContract)

	scopedCarriesSheet := validManifest()
	scopedCarriesSheet.Assertions[1].Sheet = "Ledger"
	assert.ErrorIs(t, scopedCarriesSheet.Validate(), skillevolution.ErrInvalidContract)

	badOracle := validManifest()
	badOracle.Assertions[0].OracleRefHash = "sha256:short"
	assert.ErrorIs(t, badOracle.Validate(), skillevolution.ErrInvalidContract)

	badSeverity := validManifest()
	badSeverity.Assertions[0].Severity = "medium"
	assert.ErrorIs(t, badSeverity.Validate(), skillevolution.ErrInvalidContract)

	badRecalc := validManifest()
	badRecalc.RecalculationPolicy = "libreoffice_only"
	assert.ErrorIs(t, badRecalc.Validate(), skillevolution.ErrInvalidContract)
}

func TestDecodeManifestStrictFailClosed(t *testing.T) {
	raw, err := os.ReadFile(fixturePath)
	require.NoError(t, err)

	_, err = DecodeManifestStrict(raw)
	require.NoError(t, err)

	unknown := strings.Replace(string(raw), `"manifest_id"`, `"manifes_id":"x","manifest_id"`, 1)
	_, err = DecodeManifestStrict([]byte(unknown))
	require.ErrorIs(t, err, skillevolution.ErrInvalidContract)
	assert.Contains(t, err.Error(), "unknown field")

	_, err = DecodeManifestStrict(append(raw, []byte(` {"leak":1}`)...))
	require.ErrorIs(t, err, skillevolution.ErrInvalidContract)

	empty, err := json.Marshal(Manifest{})
	require.NoError(t, err)
	_, err = DecodeManifestStrict(empty)
	assert.ErrorIs(t, err, skillevolution.ErrInvalidContract)
}
