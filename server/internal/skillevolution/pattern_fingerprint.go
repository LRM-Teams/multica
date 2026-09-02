// SPDX-License-Identifier: Apache-2.0

package skillevolution

// Deterministic fingerprint/lineage recall keys for pattern deduplication
// (spec §12.5): dedup FIRST recalls candidates by a deterministic
// fingerprint over task type, kind, environment and the three semantic
// fields, and only then do callers compare semantics, applicability, root
// cause and evidence to decide append/link/merge/new. A fingerprint match
// is recall, never authority: embedding similarity or a shared name can
// never auto-merge on their own.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// PatternFingerprintInput carries exactly the fields a dedup recall key
// may use. Environment- and task-scoped by design: identical wording in a
// different environment is a different recall bucket.
type PatternFingerprintInput struct {
	TaskType       string
	PatternKind    PatternKind
	EnvironmentKey string
	ToolCapability string
	Problem        string
	Applicability  string
	RootCause      string
	SourceModelID  string
	TargetModelID  string
}

func (i PatternFingerprintInput) Validate() error {
	if !i.PatternKind.Valid() {
		return fmt.Errorf("%w: pattern_kind %q is invalid", ErrInvalidContract, i.PatternKind)
	}
	if strings.TrimSpace(i.Problem) == "" || strings.TrimSpace(i.RootCause) == "" {
		return fmt.Errorf("%w: fingerprint needs a problem and a root cause", ErrInvalidContract)
	}
	return nil
}

// PatternFingerprint derives the deterministic recall key. Text fields are
// normalized (case-folded, whitespace collapsed) so copy/rename/whitespace
// variants land in the same bucket; nothing else is fuzzy.
func PatternFingerprint(input PatternFingerprintInput) (string, error) {
	if err := input.Validate(); err != nil {
		return "", err
	}
	canonical := strings.Join([]string{
		normalizeFingerprintText(input.TaskType),
		string(input.PatternKind),
		normalizeFingerprintText(input.EnvironmentKey),
		normalizeFingerprintText(input.ToolCapability),
		normalizeFingerprintText(input.SourceModelID),
		normalizeFingerprintText(input.TargetModelID),
		normalizeFingerprintText(input.Problem),
		normalizeFingerprintText(input.Applicability),
		normalizeFingerprintText(input.RootCause),
	}, "\x1f")
	return "sha256:" + hashHex([]byte(canonical)), nil
}

// PatternLineageScope is the coarser first-pass recall bucket (task type +
// kind + environment + tool): the candidate list a maintainer compares
// fingerprints against. It deliberately ignores wording.
func PatternLineageScope(input PatternFingerprintInput) (string, error) {
	if err := input.Validate(); err != nil {
		return "", err
	}
	canonical := strings.Join([]string{
		normalizeFingerprintText(input.TaskType),
		string(input.PatternKind),
		normalizeFingerprintText(input.EnvironmentKey),
		normalizeFingerprintText(input.ToolCapability),
	}, "\x1f")
	return "sha256:" + hashHex([]byte(canonical)), nil
}

// normalizeFingerprintText case-folds and collapses whitespace so that a
// copied, renamed, or re-wrapped variant of the same statement hashes
// identically. It makes no other attempt at similarity.
func normalizeFingerprintText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}

func hashHex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
