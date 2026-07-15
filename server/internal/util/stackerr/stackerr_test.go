package stackerr

import (
	"errors"
	"strings"
	"testing"
)

var sentinel = errors.New("sentinel")

func TestWrapNil(t *testing.T) {
	if got := Wrap(nil, "msg"); got != nil {
		t.Fatalf("Wrap(nil, _) = %v, want nil", got)
	}
}

func TestWrapCapturesStackAndCause(t *testing.T) {
	err := Wrap(sentinel, "get env")
	se, ok := err.(*StackError)
	if !ok {
		t.Fatalf("Wrap returned %T, want *StackError", err)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("errors.Is(err, sentinel) = false; Unwrap chain broken")
	}
	if len(se.stack) == 0 {
		t.Fatal("captured stack is empty")
	}
	if !strings.Contains(string(se.stack), "stackerr.TestWrapCapturesStackAndCause") {
		t.Fatalf("stack does not contain the capturing test frame:\n%s", se.stack)
	}
	if got := err.Error(); got != "get env: sentinel" {
		t.Fatalf("Error() = %q, want %q", got, "get env: sentinel")
	}
}

func TestWrapPreservesSentinelThroughUnwrap(t *testing.T) {
	// Mirrors the handler's errors.Is(err, pgx.ErrNoRows) check: wrapping a
	// sentinel must keep it reachable through Unwrap.
	err := Wrap(sentinel, "get env")
	if !errors.Is(err, sentinel) {
		t.Fatal("errors.Is(err, sentinel) = false; Unwrap chain broken by Wrap")
	}
}

func TestWrapOnStackErrorDoesNotReCapture(t *testing.T) {
	first := Wrap(sentinel, "inner")
	se1 := first.(*StackError)
	second := Wrap(first, "outer")
	se2, ok := second.(*StackError)
	if !ok {
		t.Fatalf("Wrap(*StackError) returned %T, want *StackError", second)
	}
	if &se1.stack[0] != &se2.stack[0] || string(se1.stack) != string(se2.stack) {
		t.Fatal("re-wrap captured a new stack; origin stack must be preserved")
	}
	if !errors.Is(second, sentinel) {
		t.Fatal("re-wrap broke errors.Is to original cause")
	}
	if got := second.Error(); got != "outer: inner: sentinel" {
		t.Fatalf("Error() = %q, want %q", got, "outer: inner: sentinel")
	}
}

func TestNewHasStackAndMessage(t *testing.T) {
	err := New("fork sandbox: status 503: no capacity")
	se, ok := err.(*StackError)
	if !ok {
		t.Fatalf("New returned %T, want *StackError", err)
	}
	if len(se.stack) == 0 {
		t.Fatal("New captured an empty stack")
	}
	if got := err.Error(); got != "fork sandbox: status 503: no capacity" {
		t.Fatalf("Error() = %q, want the message verbatim", got)
	}
}

func TestStackOfExtractsThroughFmtWrap(t *testing.T) {
	// Simulate the service layer's fmt.Errorf("...: %w", err) on top of a
	// stackerr-wrapped adapter error.
	err := Wrap(sentinel, "get env")
	outer := wrapFmt(err, "idempotency lookup")
	if st := StackOf(outer); len(st) == 0 {
		t.Fatal("StackOf returned nil for an fmt.Errorf-wrapped StackError")
	} else if !strings.Contains(string(st), "TestStackOfExtractsThroughFmtWrap") {
		t.Fatalf("extracted stack is not the origin stack:\n%s", st)
	}
	if st := StackOf(errors.New("plain")); st != nil {
		t.Fatalf("StackOf on a plain error = %v, want nil", st)
	}
}

// wrapFmt mimics fmt.Errorf("msg: %w", err) without importing fmt here, to prove
// StackOf traverses a standard %w wrap.
func wrapFmt(err error, msg string) error {
	return wrapped{msg: msg, cause: err}
}

type wrapped struct {
	msg   string
	cause error
}

func (w wrapped) Error() string { return w.msg + ": " + w.cause.Error() }
func (w wrapped) Unwrap() error { return w.cause }
