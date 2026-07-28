package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus"
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

// agentHumanRouteHits counts AgentPrincipal requests that hit a human data-plane
// route. Must stay at 0 after #801 cutover. Label "site" is a stable landmark.
//
// IMPORTANT: this collector is registered on the *app* metrics Registry
// (see metrics.NewRegistry → RegisterAgentHumanRouteMetrics), not the process
// default registry. The /metrics scrape uses that custom Gatherer; registering
// only on prometheus.DefaultRegisterer made series invisible (NO_DATA).
var agentHumanRouteHits = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "multica",
	Subsystem: "agent_surface",
	Name:      "human_route_hits_total",
	Help:      "AgentPrincipal requests that hit human (non-/api/agent) data-plane routes. Must be 0 after #801 cutover.",
}, []string{"site"})

// Known human-route sites that can record hits. Seeded at process start so
// scrapes always show series (value 0) instead of absent series (NO_DATA).
// AgentHumanRouteKnownSites is the exact seeded site set (exported for tests).
var AgentHumanRouteKnownSites = []string{
	"RejectAgentOnHumanAPI",
	"ListChannels",
	"ListChannelMembers",
	"loadAttachmentForRequest",
	"loadAttachmentForDownload",
	"loadIssueForUser",
	"loadProjectForResource",
}

// RegisterAgentHumanRouteMetrics registers the alias-zero counter on the given
// registerer (each app metrics Registry) and seeds known site labels at 0.
//
// The same CounterVec is shared; it must register on *every* NewRegistry()
// gatherer so scrapes never see NO_DATA. Same-registry re-entry is idempotent
// (AlreadyRegisteredError). A process-global one-shot must not skip later
// registries (Barry #1297 BLOCK).
func RegisterAgentHumanRouteMetrics(reg prometheus.Registerer) {
	if reg == nil {
		return
	}
	if err := reg.Register(agentHumanRouteHits); err != nil {
		var already prometheus.AlreadyRegisteredError
		if !errors.As(err, &already) {
			// Unexpected registration failure — surface loudly in tests/prod start.
			panic("RegisterAgentHumanRouteMetrics: " + err.Error())
		}
		// Same registry already has this collector — ok (idempotent).
	}
	for _, site := range AgentHumanRouteKnownSites {
		// Touch label sets so Prometheus exports series before any Inc.
		agentHumanRouteHits.WithLabelValues(site)
	}
}

// RecordAgentHumanRouteHit increments the alias-zero observation counter.
func RecordAgentHumanRouteHit(site string) {
	if site == "" {
		site = "unknown"
	}
	agentHumanRouteHits.WithLabelValues(site).Inc()
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
				RecordAgentHumanRouteHit("RejectAgentOnHumanAPI")
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
