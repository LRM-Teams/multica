package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// AgentPrincipal is the typed server-side identity for agent tokens
// (task_token, agent_inbox_token, agent_credential). It lives on the request
// context — not on client-writable headers — and is the only identity that
// /api/agent/* authorization may use for data-plane access.
//
// OwnerUserID is retained for control-plane/audit only (credential issuance
// lineage). It must never be used as a channel/issue viewer principal.
type AgentPrincipal struct {
	AgentID      string
	WorkspaceID  string
	OwnerUserID  string // control-plane / audit only — not a viewer
	ActorSource  string // task_token | agent_inbox_token | agent_credential
	CredentialID string // agent_credential only
	InboxEventID string // inbox binding when present
	DeliveryID   string
	TaskID       string // legacy task_token / inbox event id as task scope
}

// HumanPrincipal is a normal workspace member (JWT/PAT).
type HumanPrincipal struct {
	UserID string
}

type principalContextKey int

const (
	ctxKeyAgentPrincipal principalContextKey = iota + 100
	ctxKeyHumanPrincipal
)

// WithAgentPrincipal attaches the agent principal to the request context.
func WithAgentPrincipal(ctx context.Context, p AgentPrincipal) context.Context {
	return context.WithValue(ctx, ctxKeyAgentPrincipal, p)
}

// AgentPrincipalFromContext returns the agent principal when present.
func AgentPrincipalFromContext(ctx context.Context) (AgentPrincipal, bool) {
	p, ok := ctx.Value(ctxKeyAgentPrincipal).(AgentPrincipal)
	if !ok || strings.TrimSpace(p.AgentID) == "" {
		return AgentPrincipal{}, false
	}
	return p, true
}

// WithHumanPrincipal attaches the human principal to the request context.
func WithHumanPrincipal(ctx context.Context, p HumanPrincipal) context.Context {
	return context.WithValue(ctx, ctxKeyHumanPrincipal, p)
}

// HumanPrincipalFromContext returns the human principal when present.
func HumanPrincipalFromContext(ctx context.Context) (HumanPrincipal, bool) {
	p, ok := ctx.Value(ctxKeyHumanPrincipal).(HumanPrincipal)
	if !ok || strings.TrimSpace(p.UserID) == "" {
		return HumanPrincipal{}, false
	}
	return p, true
}

// IsAgentActorSource reports whether X-Actor-Source is a server-stamped agent token kind.
func IsAgentActorSource(source string) bool {
	switch strings.TrimSpace(source) {
	case "task_token", "agent_inbox_token", "agent_credential":
		return true
	default:
		return false
	}
}

// RequireAgentPrincipal allows only requests with AgentPrincipal in context.
func RequireAgentPrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := AgentPrincipalFromContext(r.Context()); !ok {
			http.Error(w, `{"error":"agent principal required"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RejectAgentOnHumanAPI fails closed when AgentPrincipal hits any non-/api/agent/*
// route (admin, labels CRUD, human issues, etc.). #801 completion: 403 surfaces
// must actually 403. Dedicated agent data-plane stays under /api/agent/*.
func RejectAgentOnHumanAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := AgentPrincipalFromContext(r.Context()); ok {
			path := r.URL.Path
			if path != "/api/agent" && !strings.HasPrefix(path, "/api/agent/") {
				http.Error(w, `{"error":"agent must use dedicated /api/agent/* route"}`, http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// AgentPrincipalAgentUUID parses AgentID as pgtype.UUID.
func (p AgentPrincipal) AgentUUID() (pgtype.UUID, bool) {
	return parseHeaderUUID(p.AgentID)
}

// AgentPrincipalWorkspaceUUID parses WorkspaceID as pgtype.UUID.
func (p AgentPrincipal) WorkspaceUUID() (pgtype.UUID, bool) {
	return parseHeaderUUID(p.WorkspaceID)
}

func parseHeaderUUID(raw string) (pgtype.UUID, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return pgtype.UUID{}, false
	}
	var u pgtype.UUID
	if err := u.Scan(raw); err != nil {
		return pgtype.UUID{}, false
	}
	return u, true
}
