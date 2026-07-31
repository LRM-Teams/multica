package researchwake

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestRequireActiveMember_MissPath(t *testing.T) {
	err := RequireActiveMember("", pgx.ErrNoRows)
	assertWakeError(t, err, ReasonNotMember)
}

func TestRequireActiveMember_PendingReview(t *testing.T) {
	err := RequireActiveMember("pending_prompt_review", nil)
	assertWakeError(t, err, ReasonPendingReview)
}

func TestRequireActiveMember_Archived(t *testing.T) {
	err := RequireActiveMember("archived", nil)
	assertWakeError(t, err, ReasonArchived)
}

func TestRequireActiveMember_Active(t *testing.T) {
	if err := RequireActiveMember("active", nil); err != nil {
		t.Fatalf("active member should pass, got %v", err)
	}
}

func TestRequireActiveMember_DBError(t *testing.T) {
	dbErr := errors.New("connection reset")
	err := RequireActiveMember("", dbErr)
	if err == nil {
		t.Fatal("expected wrapped db error")
	}
	var wakeErr *Error
	if errors.As(err, &wakeErr) {
		t.Fatalf("expected non-product error, got %#v", wakeErr)
	}
	if !errors.Is(err, dbErr) {
		t.Fatalf("expected wrapped db error, got %v", err)
	}
}

func TestFailurePresentation_NotMemberDoesNotMentionDaemon(t *testing.T) {
	err := RequireActiveMember("", pgx.ErrNoRows)
	reason, _, body, hint := FailurePresentation(err)
	if reason != ReasonNotMember {
		t.Fatalf("reason = %q, want %q", reason, ReasonNotMember)
	}
	lower := strings.ToLower(body)
	if strings.Contains(lower, "daemon") || strings.Contains(lower, "runtime") {
		t.Fatalf("body should not mention daemon/runtime for member miss: %q", body)
	}
	if hint == "" {
		t.Fatal("expected recovery hint")
	}
}

func TestFailurePresentation_ModelRequired(t *testing.T) {
	err := errors.New("enqueue research wake: snapshot chat task execution config: agent model is required")
	reason, title, body, hint := FailurePresentation(err)
	if reason != ReasonAgentModelRequired {
		t.Fatalf("reason = %q, want %q", reason, ReasonAgentModelRequired)
	}
	if title == "" || body == "" || hint == "" {
		t.Fatalf("expected title/body/hint, got title=%q body=%q hint=%q", title, body, hint)
	}
	lower := strings.ToLower(body + " " + hint)
	if strings.Contains(lower, "daemon") {
		t.Fatalf("model-required should not blame daemon: %q", body+" "+hint)
	}
}

func assertWakeError(t *testing.T, err error, wantReason string) {
	t.Helper()
	var wakeErr *Error
	if !errors.As(err, &wakeErr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if wakeErr.Reason != wantReason {
		t.Fatalf("reason = %q, want %q", wakeErr.Reason, wantReason)
	}
	if wakeErr.Message == "" || wakeErr.Hint == "" {
		t.Fatalf("expected message and hint, got %#v", wakeErr)
	}
}
