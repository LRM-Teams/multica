package service

import (
	"context"
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/internal/arealrl"
)

type fakeEnvDispatchSessionStarter struct {
	creds   arealrl.SessionCreds
	err     error
	calls   int
	lastRef string
	lastEnv string
}

func (f *fakeEnvDispatchSessionStarter) StartSession(_ context.Context, sessionRef, envID string) (arealrl.SessionCreds, error) {
	f.calls++
	f.lastRef = sessionRef
	f.lastEnv = envID
	if f.err != nil {
		return arealrl.SessionCreds{}, f.err
	}
	return f.creds, nil
}

// TestResolveEnvDispatchTrainingSessionOpensOnFirstAddress verifies the AC-4
// first-address path: StartSession is called exactly once with
// session_ref=binding.ID, and the returned creds are forwarded.
func TestResolveEnvDispatchTrainingSessionOpensOnFirstAddress(t *testing.T) {
	starter := &fakeEnvDispatchSessionStarter{creds: arealrl.SessionCreds{SessionID: "sess-1", ProxyKey: "proxy-key-1"}}
	res, err := ResolveEnvDispatchTrainingSession(context.Background(), starter, EnvDispatchTrainingBinding{BindingID: "binding-123"}, "env-9")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Opened {
		t.Fatalf("expected Opened=true on first address")
	}
	if res.SessionID != "sess-1" || res.ProxyKey != "proxy-key-1" {
		t.Fatalf("creds not forwarded: %+v", res)
	}
	if res.SessionRef != "binding-123" {
		t.Fatalf("session_ref must be the binding ID, got %q", res.SessionRef)
	}
	if starter.calls != 1 {
		t.Fatalf("StartSession must be called once, got %d", starter.calls)
	}
	if starter.lastRef != "binding-123" || starter.lastEnv != "env-9" {
		t.Fatalf("StartSession args wrong: ref=%q env=%q", starter.lastRef, starter.lastEnv)
	}
}

// TestResolveEnvDispatchTrainingSessionReusesOnRetry verifies the AC-4 retry
// path: a binding that already carries a persisted session ID+key reuses it
// without calling StartSession (startSessionCount==0).
func TestResolveEnvDispatchTrainingSessionReusesOnRetry(t *testing.T) {
	starter := &fakeEnvDispatchSessionStarter{creds: arealrl.SessionCreds{SessionID: "should-not-be-used", ProxyKey: "should-not-be-used"}}
	b := EnvDispatchTrainingBinding{BindingID: "binding-123", TrainingSessionID: "sess-1", TrainingSessionKey: "proxy-key-1"}
	res, err := ResolveEnvDispatchTrainingSession(context.Background(), starter, b, "env-9")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Opened {
		t.Fatalf("expected Opened=false on retry (reuse)")
	}
	if res.SessionID != "sess-1" || res.ProxyKey != "proxy-key-1" {
		t.Fatalf("must reuse persisted creds: %+v", res)
	}
	if res.SessionRef != "binding-123" {
		t.Fatalf("session_ref must be the binding ID, got %q", res.SessionRef)
	}
	if starter.calls != 0 {
		t.Fatalf("StartSession must NOT be called on retry, got %d", starter.calls)
	}
}

// TestResolveEnvDispatchTrainingSessionRequiresBindingID verifies the
// session_ref cannot be empty (binding ID is the canonical reference).
func TestResolveEnvDispatchTrainingSessionRequiresBindingID(t *testing.T) {
	starter := &fakeEnvDispatchSessionStarter{}
	if _, err := ResolveEnvDispatchTrainingSession(context.Background(), starter, EnvDispatchTrainingBinding{}, "env-9"); err == nil {
		t.Fatalf("expected error for empty binding id")
	}
}

// TestResolveEnvDispatchTrainingSessionRequiresStarterOnFirstAddress verifies a
// first-address binding without a starter fails closed rather than silently
// proceeding without a session.
func TestResolveEnvDispatchTrainingSessionRequiresStarterOnFirstAddress(t *testing.T) {
	if _, err := ResolveEnvDispatchTrainingSession(context.Background(), nil, EnvDispatchTrainingBinding{BindingID: "binding-123"}, "env-9"); err == nil {
		t.Fatalf("expected error when starter is nil on first address")
	}
}

// TestResolveEnvDispatchTrainingSessionPropagatesStartError verifies
// StartSession failures surface (the orchestrator compensates).
func TestResolveEnvDispatchTrainingSessionPropagatesStartError(t *testing.T) {
	starter := &fakeEnvDispatchSessionStarter{err: errors.New("bridge down")}
	if _, err := ResolveEnvDispatchTrainingSession(context.Background(), starter, EnvDispatchTrainingBinding{BindingID: "binding-123"}, "env-9"); err == nil {
		t.Fatalf("expected start_session error to surface")
	}
	if starter.calls != 1 {
		t.Fatalf("StartSession must be attempted once, got %d", starter.calls)
	}
}

// TestEnvDispatchTrainingRuntimePolicy verifies the training sandbox runtime is
// server-owned: model areal-default, base_url the bridge URL, api_key the
// session proxy key.
func TestEnvDispatchTrainingRuntimePolicy(t *testing.T) {
	rt := EnvDispatchTrainingRuntimePolicy("https://bridge.invalid/v1", "proxy-key-1")
	if rt.Model != "areal-default" {
		t.Fatalf("model must be areal-default, got %q", rt.Model)
	}
	if rt.BaseURL != "https://bridge.invalid/v1" {
		t.Fatalf("base_url must be the bridge URL, got %q", rt.BaseURL)
	}
	if rt.APIKey != "proxy-key-1" {
		t.Fatalf("api_key must be the session proxy key, got %q", rt.APIKey)
	}
}
