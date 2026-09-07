// SPDX-License-Identifier: Apache-2.0

package skillevolution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrIdempotencyPayloadConflict marks a replay of an idempotency key with
// a different payload: the same key must always name the same request.
var ErrIdempotencyPayloadConflict = errors.New("idempotency key replayed with a different payload")

// IdempotentRequest claims one idempotency key for one canonical payload
// (spec §12.4: every ledger object carries an idempotency key). The
// payload hash is derived from the caller's canonical serialization.
type IdempotentRequest struct {
	WorkspaceID string
	Key         string
	RequestKind string
	PayloadHash string
}

// HashCanonicalPayload derives the contract-shaped sha256 of a canonical
// request body. Callers hash the exact bytes they are about to persist.
func HashCanonicalPayload(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (r IdempotentRequest) Validate() error {
	if err := validateOpaqueID("workspace_id", r.WorkspaceID); err != nil {
		return err
	}
	if err := validateOpaqueID("idempotency_key", r.Key); err != nil {
		return err
	}
	if r.RequestKind == "" || len(r.RequestKind) > 128 {
		return fmt.Errorf("%w: request_kind is invalid", ErrInvalidContract)
	}
	if !validSHA256(r.PayloadHash) {
		return fmt.Errorf("%w: payload_hash must be a sha256 hash", ErrInvalidContract)
	}
	return nil
}

// IdempotencyRecord is the persisted claim plus the response recorded on
// first execution.
type IdempotencyRecord struct {
	Request   IdempotentRequest
	Response  json.RawMessage
	CreatedAt time.Time
}

// IdempotencyStore is the replay port (ADR 0021 D7: the port lives here,
// the PostgreSQL implementation lives in the service layer).
type IdempotencyStore interface {
	// RunOnce executes work exactly once per (workspace, key): the first
	// claim records the work's response; a replay with the identical
	// payload hash returns the stored response without re-running work;
	// a different payload under the same key is
	// ErrIdempotencyPayloadConflict. When work fails, nothing is claimed.
	RunOnce(ctx context.Context, request IdempotentRequest, work func(ctx context.Context) (json.RawMessage, error)) (response json.RawMessage, replayed bool, err error)
}
