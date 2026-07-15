// Package stackerr attaches a goroutine stack trace to an error at the point it
// first arises, so the full call chain (adapter origin -> service -> handler ->
// router -> net/http) can be surfaced in a diagnostic "traceback" field of an
// error response or a server-side log line.
//
// Capture happens once, at the origin: Wrap records debug.Stack() when it first
// receives a non-stackerr error. Later %w wraps (fmt.Errorf) layer context
// messages on top without re-capturing, and Unwrap exposes the original cause so
// errors.Is / errors.As keep working (e.g. errors.Is(err, pgx.ErrNoRows)).
//
// Note: debug.Stack() is taken at the `if err != nil` check, so the innermost
// call that produced the error (the DB query / cloud-runtime RPC) has already
// returned and is NOT in the trace. The frame that IS captured is the adapter
// method + line, which identifies which call failed - the diagnostic value.
package stackerr

import (
	"errors"
	"runtime/debug"
)

// StackError wraps a cause with a message and the goroutine stack captured at
// the origin. It implements Unwrap so errors.Is / errors.As traverse to cause.
type StackError struct {
	cause error
	msg   string // context prefix added at the capture site; "" => Error() == cause
	stack []byte // debug.Stack() captured once at the origin
}

// Wrap captures the current goroutine stack and returns a *StackError wrapping
// err with msg. nil err => nil (so it is a drop-in for fmt.Errorf("m: %w", err)
// at guard clauses). If err is already a *StackError, the original stack and
// cause are preserved and msg is prepended (no re-capture, no nested stacks).
func Wrap(err error, msg string) error {
	if err == nil {
		return nil
	}
	if se, ok := err.(*StackError); ok {
		if msg == "" {
			return se
		}
		if se.msg == "" {
			return &StackError{cause: se.cause, msg: msg, stack: se.stack}
		}
		return &StackError{cause: se.cause, msg: msg + ": " + se.msg, stack: se.stack}
	}
	return &StackError{cause: err, msg: msg, stack: debug.Stack()}
}

// New returns a *StackError with a fresh cause (errors.New(msg)) and a captured
// stack. Use it for status-body errors that have no inner err, e.g.
// `stackerr.New(fmt.Sprintf("fork sandbox: status %d: %s", code, body))`.
func New(msg string) error {
	return &StackError{cause: errors.New(msg), msg: "", stack: debug.Stack()}
}

func (e *StackError) Error() string {
	if e.msg == "" {
		return e.cause.Error()
	}
	return e.msg + ": " + e.cause.Error()
}

// Unwrap returns the underlying cause, preserving errors.Is / errors.As traversal
// to sentinels like pgx.ErrNoRows or service.ErrEnvInUse.
func (e *StackError) Unwrap() error { return e.cause }

// Stack returns the captured goroutine stack trace (debug.Stack() output).
func (e *StackError) Stack() []byte { return e.stack }

// StackOf walks err's Unwrap chain and returns the first captured stack, or nil
// if err (and its chain) carries no *StackError. Used at the handler boundary to
// pull the origin traceback out of a wrapped error for rendering.
func StackOf(err error) []byte {
	var se *StackError
	if errors.As(err, &se) {
		return se.stack
	}
	return nil
}
