// SPDX-License-Identifier: Apache-2.0

package skillevolution

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testHash = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func validCandidateContract() SkillCandidateContract {
	return SkillCandidateContract{
		ContractKind:          "skill_candidate",
		SchemaVersion:         1,
		CandidateID:           "candidate-1",
		TargetSkillID:         "skill-1",
		BaseArtifactHash:      testHash,
		CandidateArtifactHash: testHash,
		ProposedDiffHash:      testHash,
		RequestedScope:        "agent",
		MotivatingPatterns: []SkillEvolutionRef{{
			Kind: RefPattern, ID: "pattern-1", WorkspaceID: "workspace-1",
		}},
	}
}

func TestDecodeSkillCandidateContractStrictAndCanonical(t *testing.T) {
	payload, err := json.Marshal(validCandidateContract())
	require.NoError(t, err)
	decoded, err := DecodeSkillCandidateContract(payload)
	require.NoError(t, err)
	assert.Equal(t, validCandidateContract(), decoded.Contract)
	assert.Regexp(t, `^sha256:[0-9a-f]{64}$`, decoded.Hash)
	assert.JSONEq(t, string(payload), string(decoded.Canonical))

	decodedAgain, err := DecodeSkillCandidateContract(decoded.Canonical)
	require.NoError(t, err)
	assert.Equal(t, decoded.Hash, decodedAgain.Hash)
}

func TestDecodeSkillCandidateContractRejectsUnknownAndTrailingJSON(t *testing.T) {
	payload, err := json.Marshal(validCandidateContract())
	require.NoError(t, err)
	unknown := strings.TrimSuffix(string(payload), "}") + `,"unexpected":true}`
	_, err = DecodeSkillCandidateContract([]byte(unknown))
	require.ErrorIs(t, err, ErrInvalidContract)
	assert.Contains(t, err.Error(), "unknown field")

	_, err = DecodeSkillCandidateContract(append(payload, []byte(` {}`)...))
	require.ErrorIs(t, err, ErrInvalidContract)
	assert.Contains(t, err.Error(), "trailing JSON")
}

func TestSkillCandidateContractRejectsNonAtomicOrUntrustedFields(t *testing.T) {
	both := validCandidateContract()
	both.NewSkillName = "new_skill"
	require.ErrorIs(t, both.Validate(), ErrInvalidContract)

	noTarget := validCandidateContract()
	noTarget.TargetSkillID = ""
	require.ErrorIs(t, noTarget.Validate(), ErrInvalidContract)

	badHash := validCandidateContract()
	badHash.ProposedDiffHash = "not-a-hash"
	require.ErrorIs(t, badHash.Validate(), ErrInvalidContract)

	wrongRef := validCandidateContract()
	wrongRef.MotivatingPatterns[0].Kind = RefEvaluationRun
	require.ErrorIs(t, wrongRef.Validate(), ErrInvalidContract)

	duplicate := validCandidateContract()
	duplicate.MotivatingPatterns = append(duplicate.MotivatingPatterns, duplicate.MotivatingPatterns[0])
	require.ErrorIs(t, duplicate.Validate(), ErrInvalidContract)
}

func TestSkillEvolutionRefIsInternalAndClosed(t *testing.T) {
	for _, kind := range []RefKind{RefPattern, RefSkillCandidate, RefAssertionManifest, RefEvaluationRun, RefApproval} {
		require.NoError(t, (SkillEvolutionRef{Kind: kind, ID: "id", WorkspaceID: "workspace"}).Validate())
	}
	assert.ErrorIs(t, (SkillEvolutionRef{Kind: "graph_node", ID: "id", WorkspaceID: "workspace"}).Validate(), ErrInvalidContract)
	assert.ErrorIs(t, (SkillEvolutionRef{Kind: RefPattern, ID: " id", WorkspaceID: "workspace"}).Validate(), ErrInvalidContract)
}
