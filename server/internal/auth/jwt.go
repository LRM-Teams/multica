package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
)

const defaultJWTSecret = "multica-dev-secret-change-in-production"

var (
	jwtSecret     []byte
	jwtSecretOnce sync.Once
)

func JWTSecret() []byte {
	jwtSecretOnce.Do(func() {
		secret := os.Getenv("JWT_SECRET")
		if secret == "" {
			secret = defaultJWTSecret
		}
		jwtSecret = []byte(secret)
	})

	return jwtSecret
}

// GeneratePATToken creates a new personal access token: "mul_" + 40 random hex chars.
func GeneratePATToken() (string, error) {
	b := make([]byte, 20) // 20 bytes = 40 hex chars
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate PAT token: %w", err)
	}
	return "mul_" + hex.EncodeToString(b), nil
}

// GenerateDaemonToken creates a new daemon auth token: "mdt_" + 40 random hex chars.
func GenerateDaemonToken() (string, error) {
	b := make([]byte, 20) // 20 bytes = 40 hex chars
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate daemon token: %w", err)
	}
	return "mdt_" + hex.EncodeToString(b), nil
}

// GenerateAgentInboxDeliveryToken creates a temporary bearer token for one
// agent inbox delivery. It intentionally keeps the existing "mat_" wire prefix
// so the daemon/CLI transport can reuse the same bearer-token path, but the
// server stores and authorizes it through agent_inbox_token rather than the
// legacy task_token table. This is the P0 stop-bleed path; the long-term Raft
// shape is an agent credential plus server-side delivery validation.
func GenerateAgentInboxDeliveryToken() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate agent inbox delivery token: %w", err)
	}
	return "mat_" + hex.EncodeToString(b), nil
}

// GenerateAgentCredentialToken creates a durable per-launch Agent transport
// credential. Agent credentials use their own wire prefix so they cannot be
// confused with task or inbox delivery tokens.
func GenerateAgentCredentialToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate agent credential token: %w", err)
	}
	return "sk_agent_" + hex.EncodeToString(b), nil
}

// GenerateSandboxNodeKey creates a stable user-visible key for one sandboxd node.
func GenerateSandboxNodeKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate sandbox node key: %w", err)
	}
	return "msk_" + hex.EncodeToString(b), nil
}

// GenerateSandboxNodeToken creates a machine credential for a shared sandbox
// node. Unlike daemon tokens, it is node-scoped rather than workspace-scoped.
func GenerateSandboxNodeToken() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate sandbox node token: %w", err)
	}
	return "msn_" + hex.EncodeToString(b), nil
}

// GenerateSandboxJobToken creates a short-lived token scoped to one sandbox job.
func GenerateSandboxJobToken() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate sandbox job token: %w", err)
	}
	return "mst_" + hex.EncodeToString(b), nil
}

// HashToken returns the hex-encoded SHA-256 hash of a token string.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
