package service

import (
	"context"
	"errors"
	"sync"
)

var ErrCanonicalMessagePersistRequired = errors.New("atomic canonical channel message persistence is required")

// CanonicalChannelMessagePersistence is returned only after the adapter has
// committed the canonical message and the complete durable recipient state in
// one database transaction. Recipients includes newly created delivery plans
// on first send and any plans repaired during an idempotent replay.
type CanonicalChannelMessagePersistence[T any, R any] struct {
	Message    T
	Created    bool
	Recipients []R
}

// CanonicalChannelMessageOperation supplies transport-specific implementations
// while this application service owns validation, the atomic persistence
// boundary, publication, and explicit post-ack continuation. Publish is called
// only for a newly created message and only after PersistAtomically commits.
type CanonicalChannelMessageOperation[T any, R any] struct {
	Validate          func(context.Context) error
	PersistAtomically func(context.Context) (CanonicalChannelMessagePersistence[T, R], error)
	Publish           func(context.Context, T) error
	PostAck           func(context.Context, T, bool, []R)
}

type canonicalChannelMessageAck[T any, R any] struct {
	once       sync.Once
	message    T
	created    bool
	recipients []R
	postAck    func(context.Context, T, bool, []R)
}

// CanonicalChannelMessageResult is the committed send result. Acknowledge is
// deliberately separate from SendCanonicalChannelMessage: HTTP callers invoke
// it only after writing the response, while non-HTTP callers invoke it once
// their equivalent acceptance boundary has completed.
type CanonicalChannelMessageResult[T any, R any] struct {
	Message T
	Created bool
	ack     *canonicalChannelMessageAck[T, R]
}

// Acknowledge starts post-ack behavior at most once. A nil PostAck callback is
// valid, making this method a no-op.
func (r CanonicalChannelMessageResult[T, R]) Acknowledge(ctx context.Context) {
	if r.ack == nil || r.ack.postAck == nil {
		return
	}
	r.ack.once.Do(func() {
		r.ack.postAck(ctx, r.ack.message, r.ack.created, r.ack.recipients)
	})
}

// SendCanonicalChannelMessage is the single application-service boundary for
// frontend and env-dispatch channel sends. Persistence includes exact recipient
// selection and all delivery obligations; publication can therefore never race
// ahead of durable delivery state. An idempotent replay still enters the atomic
// persistence callback so legacy partial sends are repaired, but it is not
// republished and ordinary message side effects remain suppressed.
func SendCanonicalChannelMessage[T any, R any](ctx context.Context, operation CanonicalChannelMessageOperation[T, R]) (CanonicalChannelMessageResult[T, R], error) {
	var zero CanonicalChannelMessageResult[T, R]
	if operation.PersistAtomically == nil {
		return zero, ErrCanonicalMessagePersistRequired
	}
	if operation.Validate != nil {
		if err := operation.Validate(ctx); err != nil {
			return zero, err
		}
	}
	persisted, err := operation.PersistAtomically(ctx)
	if err != nil {
		return zero, err
	}
	result := CanonicalChannelMessageResult[T, R]{
		Message: persisted.Message,
		Created: persisted.Created,
		ack: &canonicalChannelMessageAck[T, R]{
			message: persisted.Message, created: persisted.Created,
			recipients: persisted.Recipients, postAck: operation.PostAck,
		},
	}
	if persisted.Created && operation.Publish != nil {
		if err := operation.Publish(ctx, persisted.Message); err != nil {
			return zero, err
		}
	}
	return result, nil
}
