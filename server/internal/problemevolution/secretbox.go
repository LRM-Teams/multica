package problemevolution

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// MasterKeyEnv names the environment variable holding the base64 server master
// key. Keys live in the process environment, never in the database, so a stolen
// database dump alone cannot decrypt a hidden answer.
const MasterKeyEnv = "MULTICA_PROBLEM_EVOLUTION_MASTER_KEY"

// MasterKeyIDEnv names the key identifier recorded with each secret, so a future
// key rotation can tell which master key sealed which row.
const MasterKeyIDEnv = "MULTICA_PROBLEM_EVOLUTION_MASTER_KEY_ID"

// DefaultMasterKeyID is used when the deployment has not named its key.
const DefaultMasterKeyID = "env:v1"

// ErrMasterKeyMissing means the deployment has no master key configured, so
// secrets can be neither stored nor read.
var ErrMasterKeyMissing = errors.New("problem evolution master key is not configured")

// ErrSecretDecrypt means a stored secret could not be opened with the current
// master key: wrong key, wrong key id, or tampered ciphertext.
var ErrSecretDecrypt = errors.New("problem evolution secret could not be decrypted")

// SealedSecret is an envelope-encrypted payload as stored in the database.
type SealedSecret struct {
	Ciphertext      []byte
	Nonce           []byte
	WrappedKey      []byte
	WrappedKeyNonce []byte
	KeyID           string
	ContentHash     string
}

// Sealer performs envelope encryption with a process-held master key.
type Sealer struct {
	masterKey []byte
	keyID     string
}

// NewSealerFromEnv builds a sealer from the environment.
func NewSealerFromEnv() (*Sealer, error) {
	return NewSealer(os.Getenv(MasterKeyEnv), os.Getenv(MasterKeyIDEnv))
}

// NewSealer builds a sealer from a base64 (standard or raw URL) 32-byte key.
func NewSealer(encodedKey, keyID string) (*Sealer, error) {
	trimmed := strings.TrimSpace(encodedKey)
	if trimmed == "" {
		return nil, ErrMasterKeyMissing
	}
	key, err := decodeKey(trimmed)
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: master key must be 32 bytes, got %d", ErrMasterKeyMissing, len(key))
	}
	if keyID == "" {
		keyID = DefaultMasterKeyID
	}
	return &Sealer{masterKey: key, keyID: keyID}, nil
}

func decodeKey(encoded string) ([]byte, error) {
	for _, decoder := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if key, err := decoder.DecodeString(encoded); err == nil {
			return key, nil
		}
	}
	if key, err := hex.DecodeString(encoded); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("%w: master key is not valid base64 or hex", ErrMasterKeyMissing)
}

// KeyID reports the identifier recorded with newly sealed secrets.
func (s *Sealer) KeyID() string {
	return s.keyID
}

// Seal encrypts plaintext under a fresh data key wrapped by the master key.
//
// A per-secret data key means rotating the master key only rewraps small keys
// rather than re-encrypting every stored answer.
func (s *Sealer) Seal(plaintext []byte) (SealedSecret, error) {
	if s == nil {
		return SealedSecret{}, ErrMasterKeyMissing
	}
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		return SealedSecret{}, err
	}
	ciphertext, nonce, err := sealWith(dataKey, plaintext)
	if err != nil {
		return SealedSecret{}, err
	}
	wrappedKey, wrappedNonce, err := sealWith(s.masterKey, dataKey)
	if err != nil {
		return SealedSecret{}, err
	}
	digest := sha256.Sum256(plaintext)
	return SealedSecret{
		Ciphertext:      ciphertext,
		Nonce:           nonce,
		WrappedKey:      wrappedKey,
		WrappedKeyNonce: wrappedNonce,
		KeyID:           s.keyID,
		// The hash lets an operator confirm which answer is stored without the
		// server ever handing back the answer itself.
		ContentHash: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

// Open decrypts a sealed secret.
func (s *Sealer) Open(sealed SealedSecret) ([]byte, error) {
	if s == nil {
		return nil, ErrMasterKeyMissing
	}
	if sealed.KeyID != "" && sealed.KeyID != s.keyID {
		return nil, fmt.Errorf("%w: sealed with key %q but %q is configured", ErrSecretDecrypt, sealed.KeyID, s.keyID)
	}
	dataKey, err := openWith(s.masterKey, sealed.WrappedKey, sealed.WrappedKeyNonce)
	if err != nil {
		return nil, err
	}
	return openWith(dataKey, sealed.Ciphertext, sealed.Nonce)
}

func sealWith(key, plaintext []byte) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return aead.Seal(nil, nonce, plaintext, nil), nonce, nil
}

func openWith(key, ciphertext, nonce []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrSecretDecrypt, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrSecretDecrypt, err)
	}
	if len(nonce) != aead.NonceSize() {
		return nil, fmt.Errorf("%w: nonce length %d is invalid", ErrSecretDecrypt, len(nonce))
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrSecretDecrypt, err)
	}
	return plaintext, nil
}
