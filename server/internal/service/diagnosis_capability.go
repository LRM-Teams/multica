// SPDX-License-Identifier: Apache-2.0

package service

import (
	"crypto/rand"
	"crypto/subtle"
	"fmt"

	"github.com/multica-ai/multica/server/internal/auth"
)

// Per-run capability tokens authorize the sandboxed diagnosis agent against
// the /api/v1/diagnosis-runs/{runID} API surface. The raw token is handed to
// the agent exactly once (via task env); the server persists only its SHA-256
// hash on the run row (migration 278). This mirrors the bearer-token pattern
// of the loopback tool server (diagnosis_tool_server.go), except the hash is
// persisted so any API replica can verify tokens for the same run.

// MintDiagnosisCapabilityToken returns a new cryptographically random
// 32-byte capability token, hex-encoded (64 chars).
func MintDiagnosisCapabilityToken() (string, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return "", fmt.Errorf("diagnosis capability token: %w", err)
	}
	return fmt.Sprintf("%x", token), nil
}

// HashDiagnosisCapabilityToken returns the persisted token fingerprint. It is
// deliberately the same SHA-256-hex scheme as auth.HashToken so the
// diagnosis-auth middleware (which cannot import this package) verifies
// tokens through the shared primitive.
func HashDiagnosisCapabilityToken(token string) string {
	return auth.HashToken(token)
}

// VerifyDiagnosisCapabilityToken constant-time-compares a presented token
// against the stored hash. An empty stored hash never matches: server-mode
// runs have no capability token and must not be reachable over the run API.
func VerifyDiagnosisCapabilityToken(token, storedHash string) bool {
	if storedHash == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(HashDiagnosisCapabilityToken(token)), []byte(storedHash)) == 1
}
