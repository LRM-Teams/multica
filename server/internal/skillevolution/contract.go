// SPDX-License-Identifier: Apache-2.0

// Package skillevolution defines the fail-closed contracts shared by future
// Pattern, candidate, evaluator, and approval services. It deliberately has no
// storage or provider dependency so contracts can be validated at every trust
// boundary before a ledger or resolver is reached.
package skillevolution

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const maxContractBytes = 256 << 10

var ErrInvalidContract = errors.New("invalid skill evolution contract")

type RefKind string

const (
	RefPattern           RefKind = "pattern"
	RefSkillCandidate    RefKind = "skill_candidate"
	RefAssertionManifest RefKind = "assertion_manifest"
	RefEvaluationRun     RefKind = "evaluation_run"
	RefApproval          RefKind = "approval"
)

// SkillEvolutionRef is an internal, capability-scoped reference. It is not a
// public MemoryRef kind and cannot be resolved by task-recall APIs.
type SkillEvolutionRef struct {
	Kind        RefKind `json:"kind"`
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
}

func (r SkillEvolutionRef) Validate() error {
	switch r.Kind {
	case RefPattern, RefSkillCandidate, RefAssertionManifest, RefEvaluationRun, RefApproval:
	default:
		return fmt.Errorf("%w: unknown evolution ref kind %q", ErrInvalidContract, r.Kind)
	}
	if err := validateOpaqueID("id", r.ID); err != nil {
		return err
	}
	return validateOpaqueID("workspace_id", r.WorkspaceID)
}

// SkillCandidateContract is the immutable, one-Skill proposal envelope. The
// later ledger binds it to a CAS revision; this contract prevents a proposer
// from smuggling multi-Skill changes or unverified artifact identities.
type SkillCandidateContract struct {
	ContractKind          string              `json:"contract_kind"`
	SchemaVersion         int                 `json:"schema_version"`
	CandidateID           string              `json:"candidate_id"`
	TargetSkillID         string              `json:"target_skill_id,omitempty"`
	NewSkillName          string              `json:"new_skill_name,omitempty"`
	BaseArtifactHash      string              `json:"base_artifact_hash"`
	CandidateArtifactHash string              `json:"candidate_artifact_hash"`
	ProposedDiffHash      string              `json:"proposed_diff_hash"`
	RequestedScope        string              `json:"requested_scope"`
	MotivatingPatterns    []SkillEvolutionRef `json:"motivating_patterns"`
}

// DecodedSkillCandidateContract carries the canonical bytes used as the
// idempotency/hash input for a candidate submission.
type DecodedSkillCandidateContract struct {
	Contract  SkillCandidateContract
	Canonical []byte
	Hash      string
}

// DecodeSkillCandidateContract rejects unknown fields and trailing JSON before
// validating the fixed v1 envelope. Caller-provided content is never trusted
// merely because it successfully unmarshals.
func DecodeSkillCandidateContract(raw []byte) (DecodedSkillCandidateContract, error) {
	if len(raw) == 0 || len(raw) > maxContractBytes {
		return DecodedSkillCandidateContract{}, fmt.Errorf("%w: payload size must be between 1 and %d bytes", ErrInvalidContract, maxContractBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var contract SkillCandidateContract
	if err := decoder.Decode(&contract); err != nil {
		return DecodedSkillCandidateContract{}, fmt.Errorf("%w: decode candidate: %v", ErrInvalidContract, err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return DecodedSkillCandidateContract{}, err
	}
	if err := contract.Validate(); err != nil {
		return DecodedSkillCandidateContract{}, err
	}
	canonical, err := json.Marshal(contract)
	if err != nil {
		return DecodedSkillCandidateContract{}, fmt.Errorf("%w: canonicalize candidate: %v", ErrInvalidContract, err)
	}
	sum := sha256.Sum256(canonical)
	return DecodedSkillCandidateContract{
		Contract:  contract,
		Canonical: canonical,
		Hash:      "sha256:" + hex.EncodeToString(sum[:]),
	}, nil
}

func (c SkillCandidateContract) Validate() error {
	if c.ContractKind != "skill_candidate" || c.SchemaVersion != 1 {
		return fmt.Errorf("%w: contract_kind=skill_candidate and schema_version=1 are required", ErrInvalidContract)
	}
	if err := validateOpaqueID("candidate_id", c.CandidateID); err != nil {
		return err
	}
	if (c.TargetSkillID == "") == (c.NewSkillName == "") {
		return fmt.Errorf("%w: exactly one of target_skill_id or new_skill_name is required", ErrInvalidContract)
	}
	if c.TargetSkillID != "" {
		if err := validateOpaqueID("target_skill_id", c.TargetSkillID); err != nil {
			return err
		}
	}
	if c.NewSkillName != "" {
		if err := validateSkillName(c.NewSkillName); err != nil {
			return err
		}
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"base_artifact_hash", c.BaseArtifactHash},
		{"candidate_artifact_hash", c.CandidateArtifactHash},
		{"proposed_diff_hash", c.ProposedDiffHash},
	} {
		if !validSHA256(field.value) {
			return fmt.Errorf("%w: %s must be a sha256 hash", ErrInvalidContract, field.name)
		}
	}
	switch c.RequestedScope {
	case "agent", "channel", "workspace":
	default:
		return fmt.Errorf("%w: requested_scope %q is invalid", ErrInvalidContract, c.RequestedScope)
	}
	if len(c.MotivatingPatterns) == 0 || len(c.MotivatingPatterns) > 64 {
		return fmt.Errorf("%w: motivating_patterns must contain 1 to 64 refs", ErrInvalidContract)
	}
	seen := make(map[string]struct{}, len(c.MotivatingPatterns))
	for _, ref := range c.MotivatingPatterns {
		if ref.Kind != RefPattern {
			return fmt.Errorf("%w: motivating_patterns must contain pattern refs", ErrInvalidContract)
		}
		if err := ref.Validate(); err != nil {
			return err
		}
		key := string(ref.Kind) + "\x00" + ref.WorkspaceID + "\x00" + ref.ID
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("%w: motivating_patterns contains a duplicate ref", ErrInvalidContract)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%w: trailing JSON value", ErrInvalidContract)
		}
		return fmt.Errorf("%w: trailing payload: %v", ErrInvalidContract, err)
	}
	return nil
}

func validateOpaqueID(name, value string) error {
	if value == "" || len(value) > 256 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: %s is invalid", ErrInvalidContract, name)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: %s contains a control character", ErrInvalidContract, name)
		}
	}
	return nil
}

func validateSkillName(name string) error {
	if len(name) > 128 || name == "" {
		return fmt.Errorf("%w: new_skill_name is invalid", ErrInvalidContract)
	}
	for i, r := range name {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != '-' {
			return fmt.Errorf("%w: new_skill_name is invalid", ErrInvalidContract)
		}
		if i == 0 && (r < 'a' || r > 'z') {
			return fmt.Errorf("%w: new_skill_name is invalid", ErrInvalidContract)
		}
	}
	return nil
}

func validSHA256(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil
}

// ValidSHA256 exposes the canonical sha256 contract-hash check for
// sub-packages that define domain manifests over the same envelope rules.
func ValidSHA256(value string) bool { return validSHA256(value) }

// ValidateOpaqueID exposes the shared opaque-id rules for sub-package
// domain manifests.
func ValidateOpaqueID(name, value string) error { return validateOpaqueID(name, value) }
