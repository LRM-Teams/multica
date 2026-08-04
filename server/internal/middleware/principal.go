package middleware

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"

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

// AgentHumanRouteKnownSites is the exact seeded site set (exported for tests).
// Scrapes must show every site at process start (value 0) — never NO_DATA.
var AgentHumanRouteKnownSites = []string{
	"RejectAgentOnHumanAPI",
	"ListChannels",
	"ListChannelMembers",
	"AddChannelMember",
	"AddChannelMembers",
	"RemoveChannelMember",
	"loadAttachmentForRequest",
	"loadAttachmentForDownload",
	"loadIssueForUser",
	"loadProjectForResource",
}

// Per-registry CounterVecs so each NewRegistry() gatherer starts at exact 0
// and scrapes always see series. RecordAgentHumanRouteHit fans out to all
// registered vecs (production has one; tests may construct several).
//
// Do NOT use process-global one-shot registration (Barry #1297): that left
// later app registries missing the series entirely.
var (
	agentHumanRouteMu    sync.Mutex
	agentHumanRouteByReg = map[prometheus.Registerer]*prometheus.CounterVec{}
	agentHumanRouteVecs  []*prometheus.CounterVec
)

func newAgentHumanRouteHitsVec() *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "multica",
		Subsystem: "agent_surface",
		Name:      "human_route_hits_total",
		Help:      "AgentPrincipal requests that hit human (non-/api/agent) data-plane routes. Must be 0 after #801 cutover.",
	}, []string{"site"})
}

func seedAgentHumanRouteSites(v *prometheus.CounterVec) {
	for _, site := range AgentHumanRouteKnownSites {
		v.WithLabelValues(site)
	}
}

// RegisterAgentHumanRouteMetrics registers a fresh alias-zero counter on the
// given registerer and seeds known site labels at 0. Same-registry re-entry
// is idempotent. Each NewRegistry() must get its own collector so second
// gatherers are never empty (Barry two-registry hard control).
func RegisterAgentHumanRouteMetrics(reg prometheus.Registerer) {
	if reg == nil {
		return
	}
	agentHumanRouteMu.Lock()
	defer agentHumanRouteMu.Unlock()

	if v, ok := agentHumanRouteByReg[reg]; ok {
		seedAgentHumanRouteSites(v)
		return
	}

	v := newAgentHumanRouteHitsVec()
	if err := reg.Register(v); err != nil {
		var already prometheus.AlreadyRegisteredError
		if errors.As(err, &already) {
			// Same registry already has an equivalent collector — adopt it if possible.
			if existing, ok := already.ExistingCollector.(*prometheus.CounterVec); ok {
				v = existing
			} else {
				panic("RegisterAgentHumanRouteMetrics: already registered with unexpected collector type")
			}
		} else {
			panic("RegisterAgentHumanRouteMetrics: " + err.Error())
		}
	}
	seedAgentHumanRouteSites(v)
	agentHumanRouteByReg[reg] = v
	agentHumanRouteVecs = append(agentHumanRouteVecs, v)
}

// RecordAgentHumanRouteHit increments the alias-zero observation counter on
// every registry-bound vec (so all live scrapers observe the hit).
func RecordAgentHumanRouteHit(site string) {
	if site == "" {
		site = "unknown"
	}
	agentHumanRouteMu.Lock()
	defer agentHumanRouteMu.Unlock()
	for _, v := range agentHumanRouteVecs {
		v.WithLabelValues(site).Inc()
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

// agentSelfManagePath allows AgentPrincipal onto the human agent resource
// routes used for self-only profile updates (task #125 / Raft align).
// Handler canUpdateAgent / canManageAgent still require path id == principal.
// Everything else under /api/agents/* remains blocked.
//
// Matches:
//   PUT  /api/agents/{uuid}
//   POST /api/agents/{uuid}/archive
//   POST /api/agents/{uuid}/restore
var agentSelfManagePath = regexp.MustCompile(`(?i)^/api/agents/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}(?:/(?:archive|restore))?$`)

func agentPrincipalMayUseHumanAgentPath(method, path string) bool {
	if !agentSelfManagePath.MatchString(path) {
		return false
	}
	switch method {
	case http.MethodPut:
		// PUT /api/agents/{id} only (no sub-resource)
		return !strings.HasSuffix(path, "/archive") && !strings.HasSuffix(path, "/restore")
	case http.MethodPost:
		return strings.HasSuffix(path, "/archive") || strings.HasSuffix(path, "/restore")
	default:
		return false
	}
}

// RejectAgentOnHumanAPI fails closed when AgentPrincipal hits any non-/api/agent/*
// route (admin, labels CRUD, human issues, etc.). #801 completion: 403 surfaces
// must actually 403. Dedicated agent data-plane stays under /api/agent/*.
// Exception (task #125): PUT/archive/restore on /api/agents/{id} for self-only
// management; handlers enforce id == principal AgentID.
func RejectAgentOnHumanAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := AgentPrincipalFromContext(r.Context()); ok {
			path := r.URL.Path
			if path == "/api/agent" || strings.HasPrefix(path, "/api/agent/") {
				next.ServeHTTP(w, r)
				return
			}
			if agentPrincipalMayUseHumanAgentPath(r.Method, path) {
				next.ServeHTTP(w, r)
				return
			}
			RecordAgentHumanRouteHit("RejectAgentOnHumanAPI")
			http.Error(w, `{"error":"agent must use dedicated /api/agent/* route"}`, http.StatusForbidden)
			return
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
