package service

import (
	"context"
	"fmt"

	"github.com/multica-ai/multica/server/internal/arealrl"
)

// EnvDispatchSessionStarter opens an AReaL training session for env-dispatch
// provisioning. The real implementation is *arealrl.Client; tests inject a
// fake. sessionRef is the canonical session reference (the env-dispatch binding
// ID), forwarded to the bridge as "session_ref"; legacy callers may still pass
// a task ID, which the bridge accepts as an alias.
type EnvDispatchSessionStarter interface {
	StartSession(ctx context.Context, sessionRef, envID string) (arealrl.SessionCreds, error)
}

// EnvDispatchTrainingBinding is the persisted training-session state read from
// an env-dispatch binding to decide reuse-vs-open on retry (AC-4 retry
// identity). BindingID is the env-dispatch binding ID used as session_ref.
type EnvDispatchTrainingBinding struct {
	BindingID          string
	TrainingSessionID  string
	TrainingSessionKey string
}

// ResolvedTrainingSession is the session identity to use for the training
// sandbox. Opened=true means a new session was started this call (first
// address); false means a persisted session was reused without calling
// StartSession (retry idempotency, startSessionCount==0).
type ResolvedTrainingSession struct {
	SessionID  string
	ProxyKey   string
	SessionRef string
	Opened     bool
}

// ResolveEnvDispatchTrainingSession returns the training session for an
// env-dispatch training binding. On first address it calls StartSession exactly
// once with session_ref=binding.ID and returns the new creds (Opened=true). On
// retry - when the binding already carries a persisted session ID and key - it
// reuses them without calling StartSession (Opened=false). AC-4.
func ResolveEnvDispatchTrainingSession(ctx context.Context, starter EnvDispatchSessionStarter, b EnvDispatchTrainingBinding, envID string) (ResolvedTrainingSession, error) {
	if b.BindingID == "" {
		return ResolvedTrainingSession{}, fmt.Errorf("env-dispatch training session: binding id required")
	}
	if b.TrainingSessionID != "" && b.TrainingSessionKey != "" {
		return ResolvedTrainingSession{SessionID: b.TrainingSessionID, ProxyKey: b.TrainingSessionKey, SessionRef: b.BindingID, Opened: false}, nil
	}
	if starter == nil {
		return ResolvedTrainingSession{}, fmt.Errorf("env-dispatch training session: starter required on first address")
	}
	creds, err := starter.StartSession(ctx, b.BindingID, envID)
	if err != nil {
		return ResolvedTrainingSession{}, fmt.Errorf("env-dispatch training start_session: %w", err)
	}
	return ResolvedTrainingSession{SessionID: creds.SessionID, ProxyKey: creds.ProxyKey, SessionRef: b.BindingID, Opened: true}, nil
}

// EnvDispatchTrainingRuntimePolicy builds the server-owned runtime policy for a
// training derived agent's sandbox: model "areal-default", base_url the given
// proxyURL, api_key the session proxy key. The caller passes the address the
// sandbox pi can reach from *inside* the sandbox VM (AREAL_PROXY_URL /
// cfg.ProxyURL) — NOT AREAL_BRIDGE_STUB_URL, which is the backend->stub address
// and may be a compose DNS name that does not resolve inside the sandbox. No
// caller-supplied external credential is used. AC-4 / [需澄清]#3.
func EnvDispatchTrainingRuntimePolicy(proxyURL, sessionKey string) ExternalModelRuntime {
	return ExternalModelRuntime{
		BaseURL: proxyURL,
		APIKey:  sessionKey,
		Model:   arealProxyModel,
	}
}
