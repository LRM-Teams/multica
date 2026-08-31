package problemevolution

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

func testSealer(t *testing.T) *Sealer {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	sealer, err := NewSealer(key, "test:v1")
	if err != nil {
		t.Fatalf("new sealer: %v", err)
	}
	return sealer
}

func TestSealOpenRoundTrip(t *testing.T) {
	sealer := testSealer(t)
	plaintext := []byte("the hidden answer is 42")

	sealed, err := sealer.Seal(plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Contains(sealed.Ciphertext, plaintext) {
		t.Fatal("ciphertext contains the plaintext")
	}
	if bytes.Contains(sealed.WrappedKey, plaintext) {
		t.Fatal("wrapped key contains the plaintext")
	}
	opened, err := sealer.Open(sealed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("opened = %q, want %q", opened, plaintext)
	}
	if !strings.HasPrefix(sealed.ContentHash, "sha256:") {
		t.Fatalf("content hash = %q, want a sha256 prefix", sealed.ContentHash)
	}
}

func TestSealProducesDistinctCiphertextForSamePlaintext(t *testing.T) {
	sealer := testSealer(t)
	first, err := sealer.Seal([]byte("same answer"))
	if err != nil {
		t.Fatalf("seal first: %v", err)
	}
	second, err := sealer.Seal([]byte("same answer"))
	if err != nil {
		t.Fatalf("seal second: %v", err)
	}
	// Identical ciphertexts would leak that two runs share a hidden answer.
	if bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatal("two seals of the same plaintext produced identical ciphertext")
	}
	// The content hash is deliberately stable so an operator can confirm which
	// answer is stored without reading it.
	if first.ContentHash != second.ContentHash {
		t.Fatal("content hash is not stable for identical plaintext")
	}
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	sealer := testSealer(t)
	sealed, err := sealer.Seal([]byte("hidden"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	sealed.Ciphertext[0] ^= 0xff
	if _, err := sealer.Open(sealed); !errors.Is(err, ErrSecretDecrypt) {
		t.Fatalf("open tampered = %v, want a decrypt error", err)
	}
}

func TestOpenRejectsForeignMasterKey(t *testing.T) {
	sealed, err := testSealer(t).Seal([]byte("hidden"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	other, err := NewSealer(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)), "test:v1")
	if err != nil {
		t.Fatalf("new sealer: %v", err)
	}
	if _, err := other.Open(sealed); !errors.Is(err, ErrSecretDecrypt) {
		t.Fatalf("open with a foreign key = %v, want a decrypt error", err)
	}
}

func TestNewSealerRejectsUnusableKeys(t *testing.T) {
	for name, key := range map[string]string{
		"empty":     "",
		"too short": base64.StdEncoding.EncodeToString([]byte("short")),
		"garbage":   "not-base64-!!!",
	} {
		if _, err := NewSealer(key, ""); !errors.Is(err, ErrMasterKeyMissing) {
			t.Fatalf("%s key = %v, want a missing-key error", name, err)
		}
	}
}

func TestCapabilityTokenIsHashedNotStored(t *testing.T) {
	token, tokenHash, err := NewCapabilityToken()
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	if !IsValidCapabilityToken(token) {
		t.Fatalf("minted token %q is not recognised as valid", token)
	}
	if strings.Contains(tokenHash, token) || tokenHash == token {
		t.Fatal("stored hash reveals the token")
	}
	if HashCapabilityToken(token) != tokenHash {
		t.Fatal("hashing is not deterministic")
	}
	other, _, err := NewCapabilityToken()
	if err != nil {
		t.Fatalf("mint second token: %v", err)
	}
	if other == token {
		t.Fatal("two minted tokens collided")
	}
}

func TestIsValidCapabilityTokenRejectsMalformed(t *testing.T) {
	for _, token := range []string{"", "pecap_", "nope_abc", "pecap_短"} {
		if IsValidCapabilityToken(token) {
			t.Fatalf("token %q was accepted", token)
		}
	}
}

func TestCheckCapabilityDeniesWithSpecificReasons(t *testing.T) {
	now := time.Now()
	valid := CapabilityState{
		Audience:  AudienceVerifier,
		RunID:     "run-1",
		MaxUses:   1,
		Uses:      0,
		ExpiresAt: now.Add(time.Minute),
	}
	if err := CheckCapability(valid, "run-1", now); err != nil {
		t.Fatalf("expected a valid capability to pass, got %v", err)
	}

	cases := []struct {
		name   string
		mutate func(CapabilityState) CapabilityState
		runID  string
		reason string
	}{
		{"evolver audience", func(s CapabilityState) CapabilityState { s.Audience = "evolver"; return s }, "run-1", DenyReasonAudience},
		{"revoked", func(s CapabilityState) CapabilityState { s.Revoked = true; return s }, "run-1", DenyReasonRevoked},
		{"secret revoked", func(s CapabilityState) CapabilityState { s.SecretRevoked = true; return s }, "run-1", DenyReasonSecretRevoked},
		{"expired", func(s CapabilityState) CapabilityState { s.ExpiresAt = now.Add(-time.Second); return s }, "run-1", DenyReasonExpired},
		{"exhausted", func(s CapabilityState) CapabilityState { s.Uses = 1; return s }, "run-1", DenyReasonExhausted},
		{"missing run", func(s CapabilityState) CapabilityState { return s }, "", DenyReasonRunMismatch},
		{"other run", func(s CapabilityState) CapabilityState { return s }, "run-2", DenyReasonRunMismatch},
	}
	for _, testCase := range cases {
		err := CheckCapability(testCase.mutate(valid), testCase.runID, now)
		if err == nil {
			t.Fatalf("%s: expected a denial", testCase.name)
		}
		if !errors.Is(err, ErrCapabilityDenied) {
			t.Fatalf("%s: error %v is not a denial", testCase.name, err)
		}
		if reason := DenialReason(err); reason != testCase.reason {
			t.Fatalf("%s: reason = %q, want %q", testCase.name, reason, testCase.reason)
		}
	}
}

func TestCapabilityTTLIsShort(t *testing.T) {
	// A long-lived capability would be a standing read grant on the hidden
	// answer rather than a per-evaluation grant.
	if CapabilityTTL > 15*time.Minute {
		t.Fatalf("CapabilityTTL = %v, want a short window", CapabilityTTL)
	}
}
