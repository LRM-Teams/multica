package problemevolution

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CapabilityTTL is how long a verifier capability stays valid. It is short
// because a capability is minted per evaluation: a long-lived one would
// effectively be a standing read grant on the hidden answer.
const CapabilityTTL = 10 * time.Minute

// AudienceVerifier is the only audience allowed to redeem a capability. The
// evolver and the harness are deliberately not in this list — they may see the
// reward, never the answer that produced it.
const AudienceVerifier = "verifier"

// CapabilityTokenPrefix marks a problem-evolution capability in logs and makes
// an accidentally pasted token recognisable.
const CapabilityTokenPrefix = "pecap_"

// Capability denial reasons, recorded in the audit trail.
const (
	DenyReasonUnknownToken    = "unknown_token"
	DenyReasonExpired         = "expired"
	DenyReasonRevoked         = "revoked"
	DenyReasonExhausted       = "uses_exhausted"
	DenyReasonAudience        = "audience_mismatch"
	DenyReasonRunMismatch     = "run_mismatch"
	DenyReasonSecretRevoked   = "secret_revoked"
	DenyReasonMalformedToken  = "malformed_token"
	DenyReasonSecretNotSealed = "secret_not_sealed"
)

// ErrCapabilityDenied means a capability may not be redeemed.
var ErrCapabilityDenied = errors.New("problem evolution capability denied")

// NewCapabilityToken mints a token and its storage hash. Only the hash is
// persisted, so a database dump cannot be replayed as a capability.
func NewCapabilityToken() (token, tokenHash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token = CapabilityTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return token, HashCapabilityToken(token), nil
}

// HashCapabilityToken derives the stored form of a token.
func HashCapabilityToken(token string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(digest[:])
}

// CapabilityState is what the store knows about a presented capability.
type CapabilityState struct {
	Audience      string
	RunID         string
	MaxUses       int
	Uses          int
	ExpiresAt     time.Time
	Revoked       bool
	SecretRevoked bool
}

// CheckCapability decides whether a presented capability may be redeemed for a
// run. Every rejection returns a specific reason because the denial audit is
// the only signal that something tried to reach a hidden answer from the wrong
// side of the boundary.
func CheckCapability(state CapabilityState, requestedRunID string, now time.Time) error {
	if state.Audience != AudienceVerifier {
		return denial(DenyReasonAudience)
	}
	if state.Revoked {
		return denial(DenyReasonRevoked)
	}
	if state.SecretRevoked {
		return denial(DenyReasonSecretRevoked)
	}
	if !state.ExpiresAt.IsZero() && !now.Before(state.ExpiresAt) {
		return denial(DenyReasonExpired)
	}
	if state.MaxUses > 0 && state.Uses >= state.MaxUses {
		return denial(DenyReasonExhausted)
	}
	if requestedRunID != "" && state.RunID != "" && state.RunID != requestedRunID {
		return denial(DenyReasonRunMismatch)
	}
	return nil
}

// DenialReason extracts the reason from a denial error, for the audit row.
func DenialReason(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if index := strings.LastIndex(message, ": "); index >= 0 {
		return message[index+2:]
	}
	return message
}

func denial(reason string) error {
	return fmt.Errorf("%w: %s", ErrCapabilityDenied, reason)
}

// IsValidCapabilityToken reports whether a presented string is shaped like a
// capability token, so a malformed value is rejected before any lookup.
func IsValidCapabilityToken(token string) bool {
	trimmed := strings.TrimSpace(token)
	if !strings.HasPrefix(trimmed, CapabilityTokenPrefix) {
		return false
	}
	encoded := strings.TrimPrefix(trimmed, CapabilityTokenPrefix)
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	return err == nil && len(raw) == 32
}
