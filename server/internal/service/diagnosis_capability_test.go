// SPDX-License-Identifier: Apache-2.0

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiagnosisCapabilityToken_MintHashVerify(t *testing.T) {
	token, err := MintDiagnosisCapabilityToken()
	require.NoError(t, err)
	assert.Len(t, token, 64, "32 random bytes hex-encoded")

	other, err := MintDiagnosisCapabilityToken()
	require.NoError(t, err)
	assert.NotEqual(t, token, other, "mints must be unique")

	hash := HashDiagnosisCapabilityToken(token)
	assert.Len(t, hash, 64, "SHA-256 hex")
	assert.Equal(t, hash, HashDiagnosisCapabilityToken(token), "hash is deterministic")

	assert.True(t, VerifyDiagnosisCapabilityToken(token, hash))
	assert.False(t, VerifyDiagnosisCapabilityToken(other, hash))
	assert.False(t, VerifyDiagnosisCapabilityToken(token, ""), "empty stored hash never matches")
	assert.False(t, VerifyDiagnosisCapabilityToken("", hash))
}
