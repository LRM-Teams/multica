// SPDX-License-Identifier: Apache-2.0

// Package spreadsheet holds the spreadsheet-domain assertion manifest
// contract for the skill-evolution evaluation plane (spec §12.8). It is a
// pure contract package: no XLSX parser, formula engine, or storage
// dependency may be added before Gate G2 freezes the evaluator supply
// chain.
package spreadsheet

import (
	"fmt"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/skillevolution"
)

// MutationDimension is one allowed mutation axis of a spreadsheet task
// (spec §12.8). Changes outside the declared allowlist are collateral
// mutations and fail the artifact-integrity gate.
type MutationDimension string

const (
	MutateValue     MutationDimension = "value"
	MutateFormula   MutationDimension = "formula"
	MutateType      MutationDimension = "type"
	MutateStyle     MutationDimension = "style"
	MutateMerge     MutationDimension = "merge"
	MutateName      MutationDimension = "name"
	MutateStructure MutationDimension = "structure"
)

func (m MutationDimension) Valid() bool {
	switch m {
	case MutateValue, MutateFormula, MutateType, MutateStyle, MutateMerge, MutateName, MutateStructure:
		return true
	default:
		return false
	}
}

// AssertionKind is the spreadsheet assertion taxonomy (spec §12.8):
// canonical diff targets plus the output-path confinement check.
type AssertionKind string

const (
	AssertValue      AssertionKind = "value"
	AssertFormula    AssertionKind = "formula"
	AssertType       AssertionKind = "type"
	AssertStyle      AssertionKind = "style"
	AssertStructure  AssertionKind = "structure"
	AssertOutputPath AssertionKind = "output_path"
)

func (k AssertionKind) Valid() bool {
	switch k {
	case AssertValue, AssertFormula, AssertType, AssertStyle, AssertStructure, AssertOutputPath:
		return true
	default:
		return false
	}
}

// cellScoped assertions must carry a sheet and A1 range; workbook-scoped
// assertions (structure, output_path) must not.
func (k AssertionKind) cellScoped() bool {
	switch k {
	case AssertValue, AssertFormula, AssertType, AssertStyle:
		return true
	default:
		return false
	}
}

// Severity maps to the hard/soft gate split (spec §12.4): a failed hard
// assertion fails the gate regardless of soft scores.
type Severity string

const (
	SeverityHard Severity = "hard"
	SeveritySoft Severity = "soft"
)

func (s Severity) Valid() bool {
	return s == SeverityHard || s == SeveritySoft
}

// RecalculationPolicy declares how formula results are verified
// (spec §12.8): pinned-engine recalculation, optionally with an
// Excel/LibreOffice compatibility shadow recorded alongside.
type RecalculationPolicy string

const (
	RecalcPinnedEngine             RecalculationPolicy = "pinned_engine"
	RecalcPinnedEngineCompatShadow RecalculationPolicy = "pinned_engine_compat_shadow"
)

func (p RecalculationPolicy) Valid() bool {
	return p == RecalcPinnedEngine || p == RecalcPinnedEngineCompatShadow
}

// WorkbookRef pins the immutable input workbook by hash, lineage
// fingerprint, and backing storage ref (spec §12.8).
type WorkbookRef struct {
	ArtifactHash       string `json:"artifact_hash"`
	LineageFingerprint string `json:"lineage_fingerprint"`
	StorageRef         string `json:"storage_ref"`
}

// RangeDecl names an allowed sheet plus an A1-notation range.
type RangeDecl struct {
	Sheet string `json:"sheet"`
	A1    string `json:"range"`
}

// AssertionSpec is one machine-checkable declaration in the manifest.
// OracleRefHash pins the hidden oracle; the evaluator never returns it to
// the proposer.
type AssertionSpec struct {
	AssertionID   string        `json:"assertion_id"`
	Kind          AssertionKind `json:"kind"`
	Sheet         string        `json:"sheet,omitempty"`
	CellRange     string        `json:"range,omitempty"`
	Severity      Severity      `json:"severity"`
	Required      bool          `json:"required"`
	OracleRefHash string        `json:"oracle_ref_hash"`
}

// Manifest is the immutable, versioned spreadsheet assertion manifest
// (spec §12.4/§12.8). Modifying a manifest after results are seen
// requires a new manifest version and a new EvaluationRun.
type Manifest struct {
	ContractKind            string              `json:"contract_kind"`
	SchemaVersion           int                 `json:"schema_version"`
	ManifestID              string              `json:"manifest_id"`
	WorkspaceID             string              `json:"workspace_id"`
	DatasetID               string              `json:"dataset_id"`
	DatasetVersion          string              `json:"dataset_version"`
	LineageSplit            string              `json:"lineage_split"`
	EnvironmentKey          string              `json:"environment_key"`
	EvaluatorVersion        string              `json:"evaluator_version"`
	Input                   WorkbookRef         `json:"input"`
	AllowedSheets           []string            `json:"allowed_sheets"`
	AllowedRanges           []RangeDecl         `json:"allowed_ranges"`
	OutputPath              string              `json:"output_path"`
	AllowedMutations        []MutationDimension `json:"allowed_mutations"`
	Assertions              []AssertionSpec     `json:"assertions"`
	PreservedInvariants     []string            `json:"preserved_invariants"`
	UnsupportedCapabilities []string            `json:"unsupported_capabilities"`
	RecalculationPolicy     RecalculationPolicy `json:"recalculation_policy"`
	CreatedAt               time.Time           `json:"created_at"`
}

// DecodeManifestStrict applies the shared fail-closed envelope (size
// bounds, unknown fields, trailing JSON) and then validates the manifest.
func DecodeManifestStrict(raw []byte) (Manifest, error) {
	var manifest Manifest
	if err := skillevolution.DecodeStrictContract(raw, &manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (m Manifest) Validate() error {
	if m.ContractKind != "spreadsheet_assertion_manifest" || m.SchemaVersion != 1 {
		return fmt.Errorf("%w: contract_kind=spreadsheet_assertion_manifest and schema_version=1 are required", skillevolution.ErrInvalidContract)
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"manifest_id", m.ManifestID},
		{"workspace_id", m.WorkspaceID},
		{"dataset_id", m.DatasetID},
		{"dataset_version", m.DatasetVersion},
		{"lineage_split", m.LineageSplit},
		{"environment_key", m.EnvironmentKey},
		{"evaluator_version", m.EvaluatorVersion},
	} {
		if err := skillevolution.ValidateOpaqueID(field.name, field.value); err != nil {
			return err
		}
	}
	if !skillevolution.ValidSHA256(m.Input.ArtifactHash) {
		return fmt.Errorf("%w: input.artifact_hash must be a sha256 hash", skillevolution.ErrInvalidContract)
	}
	if m.Input.LineageFingerprint == "" || m.Input.StorageRef == "" {
		return fmt.Errorf("%w: input lineage fingerprint and storage ref are required", skillevolution.ErrInvalidContract)
	}
	if err := validateNoDuplicates("allowed_sheets", m.AllowedSheets); err != nil {
		return err
	}
	if len(m.AllowedSheets) == 0 {
		return fmt.Errorf("%w: allowed_sheets must not be empty", skillevolution.ErrInvalidContract)
	}
	sheets := make(map[string]struct{}, len(m.AllowedSheets))
	for _, sheet := range m.AllowedSheets {
		sheets[sheet] = struct{}{}
	}
	seenRanges := make(map[string]struct{}, len(m.AllowedRanges))
	for _, decl := range m.AllowedRanges {
		if _, ok := sheets[decl.Sheet]; !ok {
			return fmt.Errorf("%w: allowed range references sheet %q outside allowed_sheets", skillevolution.ErrInvalidContract, decl.Sheet)
		}
		if decl.A1 == "" {
			return fmt.Errorf("%w: allowed range for sheet %q is empty", skillevolution.ErrInvalidContract, decl.Sheet)
		}
		key := decl.Sheet + "!" + decl.A1
		if _, duplicate := seenRanges[key]; duplicate {
			return fmt.Errorf("%w: allowed_ranges contains a duplicate %s", skillevolution.ErrInvalidContract, key)
		}
		seenRanges[key] = struct{}{}
	}
	if err := validateConfinedPath(m.OutputPath); err != nil {
		return err
	}
	if len(m.AllowedMutations) == 0 {
		return fmt.Errorf("%w: allowed_mutations must not be empty", skillevolution.ErrInvalidContract)
	}
	seenMutations := make(map[MutationDimension]struct{}, len(m.AllowedMutations))
	for _, mutation := range m.AllowedMutations {
		if !mutation.Valid() {
			return fmt.Errorf("%w: mutation dimension %q is invalid", skillevolution.ErrInvalidContract, mutation)
		}
		if _, duplicate := seenMutations[mutation]; duplicate {
			return fmt.Errorf("%w: allowed_mutations contains a duplicate %q", skillevolution.ErrInvalidContract, mutation)
		}
		seenMutations[mutation] = struct{}{}
	}
	if err := validateNoDuplicates("preserved_invariants", m.PreservedInvariants); err != nil {
		return err
	}
	for _, invariant := range m.PreservedInvariants {
		if strings.TrimSpace(invariant) == "" {
			return fmt.Errorf("%w: preserved_invariants must not contain blank entries", skillevolution.ErrInvalidContract)
		}
	}
	if err := validateNoDuplicates("unsupported_capabilities", m.UnsupportedCapabilities); err != nil {
		return err
	}
	if !m.RecalculationPolicy.Valid() {
		return fmt.Errorf("%w: recalculation_policy %q is invalid", skillevolution.ErrInvalidContract, m.RecalculationPolicy)
	}
	if len(m.Assertions) == 0 {
		return fmt.Errorf("%w: assertions must not be empty", skillevolution.ErrInvalidContract)
	}
	seenAssertions := make(map[string]struct{}, len(m.Assertions))
	hasRequired := false
	for _, assertion := range m.Assertions {
		if err := skillevolution.ValidateOpaqueID("assertion_id", assertion.AssertionID); err != nil {
			return err
		}
		if _, duplicate := seenAssertions[assertion.AssertionID]; duplicate {
			return fmt.Errorf("%w: assertion %q appears twice", skillevolution.ErrInvalidContract, assertion.AssertionID)
		}
		seenAssertions[assertion.AssertionID] = struct{}{}
		if !assertion.Kind.Valid() {
			return fmt.Errorf("%w: assertion %q has invalid kind %q", skillevolution.ErrInvalidContract, assertion.AssertionID, assertion.Kind)
		}
		if !assertion.Severity.Valid() {
			return fmt.Errorf("%w: assertion %q has invalid severity %q", skillevolution.ErrInvalidContract, assertion.AssertionID, assertion.Severity)
		}
		if !skillevolution.ValidSHA256(assertion.OracleRefHash) {
			return fmt.Errorf("%w: assertion %q oracle_ref_hash must be a sha256 hash", skillevolution.ErrInvalidContract, assertion.AssertionID)
		}
		if assertion.Kind.cellScoped() {
			if _, ok := sheets[assertion.Sheet]; !ok {
				return fmt.Errorf("%w: assertion %q references sheet %q outside allowed_sheets", skillevolution.ErrInvalidContract, assertion.AssertionID, assertion.Sheet)
			}
			if assertion.CellRange == "" {
				return fmt.Errorf("%w: assertion %q requires a range", skillevolution.ErrInvalidContract, assertion.AssertionID)
			}
		} else if assertion.Sheet != "" || assertion.CellRange != "" {
			return fmt.Errorf("%w: workbook-scoped assertion %q must not carry sheet/range", skillevolution.ErrInvalidContract, assertion.AssertionID)
		}
		if assertion.Required {
			hasRequired = true
		}
	}
	if !hasRequired {
		return fmt.Errorf("%w: at least one assertion must be required", skillevolution.ErrInvalidContract)
	}
	if m.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is required", skillevolution.ErrInvalidContract)
	}
	return nil
}

// validateConfinedPath enforces the output-path invariant (spec §12.8):
// outputs stay inside the task workspace, never absolute and never
// escaping with parent references.
func validateConfinedPath(path string) error {
	if path == "" || strings.TrimSpace(path) != path {
		return fmt.Errorf("%w: output_path is required", skillevolution.ErrInvalidContract)
	}
	if strings.HasPrefix(path, "/") || strings.Contains(path, "..") || strings.Contains(path, "\\") {
		return fmt.Errorf("%w: output_path %q escapes the task workspace", skillevolution.ErrInvalidContract, path)
	}
	return nil
}

func validateNoDuplicates(field string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("%w: %s contains a duplicate %q", skillevolution.ErrInvalidContract, field, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}
