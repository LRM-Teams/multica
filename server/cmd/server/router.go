package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/cloudruntime"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/integrations/doubaospeech"
	"github.com/multica-ai/multica/server/internal/integrations/lark"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/internal/sandboxws"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/storage"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var defaultOrigins = []string{
	"http://localhost:3000", // Next.js dev
	"http://localhost:5173", // electron-vite dev
	"http://localhost:5174", // electron-vite dev (fallback port)
}

func allowedOrigins() []string {
	raw := strings.TrimSpace(os.Getenv("CORS_ALLOWED_ORIGINS"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("FRONTEND_ORIGIN"))
	}
	if raw == "" {
		return defaultOrigins
	}

	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		origin := strings.TrimSpace(part)
		if origin != "" {
			origins = append(origins, origin)
		}
	}
	if len(origins) == 0 {
		return defaultOrigins
	}
	return origins
}

// parseTrustedProxies parses a comma-separated list of CIDR prefixes from the
// MULTICA_TRUSTED_PROXIES env var. Invalid entries are dropped with a single
// warn-line per entry rather than crashing the server — a typo in one CIDR
// shouldn't take the whole API down. Returns nil for empty input, which the
// rate limiter treats as "trust no proxy headers, use RemoteAddr only".
func parseTrustedProxies(raw string) []netip.Prefix {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []netip.Prefix
	for _, part := range strings.Split(raw, ",") {
		s := strings.TrimSpace(part)
		if s == "" {
			continue
		}
		p, err := netip.ParsePrefix(s)
		if err != nil {
			slog.Warn("MULTICA_TRUSTED_PROXIES: ignoring invalid CIDR",
				"value", s, "error", err)
			continue
		}
		out = append(out, p)
	}
	return out
}

// NewRouter creates the fully-configured Chi router with all middleware and routes.
// rdb is optional: when non-nil the runtime local-skill request stores are
// swapped for Redis-backed implementations so multiple API nodes share the
// same pending queue (required for multi-node prod). This should be a request
// path Redis client, not the realtime relay's blocking read client. A nil rdb
// keeps the default in-memory stores which are fine for single-node dev and
// tests.
func NewRouter(pool *pgxpool.Pool, hub *realtime.Hub, bus *events.Bus, analyticsClient analytics.Client, rdb *redis.Client) chi.Router {
	r, _ := NewRouterWithOptions(pool, hub, bus, analyticsClient, rdb, RouterOptions{})
	return r
}

type RouterOptions struct {
	HTTPMetrics     *obsmetrics.HTTPMetrics
	BusinessMetrics *obsmetrics.BusinessMetrics
	DaemonHub       *daemonws.Hub
	SandboxHub      *sandboxws.Hub
	DaemonWakeup    service.TaskWakeupNotifier
	// HeartbeatScheduler, when non-nil, replaces the default synchronous
	// passthrough scheduler on the constructed Handler. main.go injects a
	// BatchedHeartbeatScheduler here so the caller can also drive Run/Stop;
	// tests leave this nil and get the legacy synchronous behavior.
	HeartbeatScheduler handler.HeartbeatScheduler
}

// NewRouterWithOptions builds the fully-configured Chi router and
// returns the *handler.Handler it was constructed from. Callers that
// need to drive background lifecycle on services attached to the
// handler (e.g. starting the Lark inbound Hub under a long-running
// context, calling Wait on shutdown) use the returned handler;
// callers that only need the HTTP handler (tests, the simple
// NewRouter shim) discard the second value.
func NewRouterWithOptions(pool *pgxpool.Pool, hub *realtime.Hub, bus *events.Bus, analyticsClient analytics.Client, rdb *redis.Client, opts RouterOptions) (chi.Router, *handler.Handler) {
	queries := db.New(pool)
	emailSvc := service.NewEmailService()
	daemonHub := opts.DaemonHub
	if daemonHub == nil {
		daemonHub = daemonws.NewHub()
	}
	sandboxHub := opts.SandboxHub
	if sandboxHub == nil {
		sandboxHub = sandboxws.NewHub()
	}

	// Initialize storage with S3 as primary, fallback to local
	var store storage.Storage
	s3 := storage.NewS3StorageFromEnv()
	if s3 != nil {
		store = s3
	} else {
		local := storage.NewLocalStorageFromEnv()
		if local != nil {
			store = local
		}
	}

	cfSigner := auth.NewCloudFrontSignerFromEnv()
	evolutionReviewer, evolutionReviewEnabled := service.NewEvolutionReviewerFromEnv()

	signupConfig := handler.Config{
		AllowSignup:                           os.Getenv("ALLOW_SIGNUP") != "false",
		AllowedEmails:                         splitAndTrim(os.Getenv("ALLOWED_EMAILS")),
		AllowedEmailDomains:                   splitAndTrim(os.Getenv("ALLOWED_EMAIL_DOMAINS")),
		DisableWorkspaceCreation:              os.Getenv("DISABLE_WORKSPACE_CREATION") == "true",
		PublicURL:                             strings.TrimRight(strings.TrimSpace(os.Getenv("MULTICA_PUBLIC_URL")), "/"),
		TrustedProxies:                        parseTrustedProxies(os.Getenv("MULTICA_TRUSTED_PROXIES")),
		CloudRuntimeFleetURL:                  cloudRuntimeFleetURLFromEnv(),
		CloudRuntimeFleetTimeout:              envDuration("MULTICA_CLOUD_FLEET_TIMEOUT", 35*time.Second),
		DefaultSelfPlayTemplate:               defaultSelfPlayTemplateFromEnv(),
		AttachmentDownloadMode:                os.Getenv("ATTACHMENT_DOWNLOAD_MODE"),
		AttachmentDownloadURLTTL:              envDuration("ATTACHMENT_DOWNLOAD_URL_TTL", 30*time.Minute),
		ChannelAmbientGateMode:                strings.TrimSpace(os.Getenv("MULTICA_AMBIENT_QUEUE_GATE_MODE")),
		ChannelAmbientGateWindow:              envDuration("MULTICA_AMBIENT_QUEUE_GATE_WINDOW", 1*time.Minute),
		ChannelAmbientGateMaxRecentPerAgent:   envPositiveInt("MULTICA_AMBIENT_QUEUE_GATE_AGENT_CAP", 1),
		ChannelAmbientGateMaxRecentPerChannel: envPositiveInt("MULTICA_AMBIENT_QUEUE_GATE_CHANNEL_CAP", 32),
		ChannelAmbientGateMaxRecentPerRuntime: envPositiveInt("MULTICA_AMBIENT_QUEUE_GATE_RUNTIME_CAP", 64),
		EvolutionReviewer:                     evolutionReviewer,
		EvolutionReviewEnabled:                evolutionReviewEnabled,
		WebPushVAPIDPublicKey:                 strings.TrimSpace(os.Getenv("WEB_PUSH_VAPID_PUBLIC_KEY")),
		WebPushVAPIDPrivateKey:                strings.TrimSpace(os.Getenv("WEB_PUSH_VAPID_PRIVATE_KEY")),
		WebPushVAPIDSubject:                   strings.TrimSpace(os.Getenv("WEB_PUSH_VAPID_SUBJECT")),
		WebPushAppURL:                         strings.TrimRight(strings.TrimSpace(os.Getenv("MULTICA_APP_URL")), "/"),
	}
	h := handler.New(queries, pool, hub, bus, emailSvc, store, cfSigner, analyticsClient, signupConfig, daemonHub)
	h.VoiceProvider = doubaospeech.New(doubaospeech.Config{
		APIKey:    os.Getenv("DOUBAO_SPEECH_API_KEY"),
		SpeakerID: os.Getenv("DOUBAO_TTS_SPEAKER_ID"),
	})
	if err := configureVoiceCallService(h, queries, os.Getenv); err != nil {
		slog.Error("voice call integration disabled", "error", err)
	} else if h.VoiceCallService != nil {
		slog.Info("voice call integration enabled", "provider", "volcengine")
	}
	h.SandboxHub = sandboxHub
	handler.ConfigureEphemeralSandboxManager(h)
	h.StartChannelBridge()
	h.Metrics = opts.BusinessMetrics
	h.TaskService.Metrics = opts.BusinessMetrics
	h.IssueService.Metrics = opts.BusinessMetrics
	if opts.BusinessMetrics != nil {
		// Wire the BusinessMetrics receiver into the cloud runtime client
		// so every outbound Fleet/Gateway request feeds the
		// multica_cloudruntime_request_* histograms.
		if client, ok := h.CloudRuntime.(*cloudruntime.Client); ok {
			client.SetRecorder(opts.BusinessMetrics)
		}
	}
	if opts.DaemonWakeup != nil {
		h.TaskService.Wakeup = opts.DaemonWakeup
	}
	if rdb != nil {
		h.ModelListStore = handler.NewRedisModelListStore(rdb)
		h.RestartStore = handler.NewRedisRestartStore(rdb)
		h.AgentLifecycleDispatchStore = handler.NewRedisAgentLifecycleDispatchStore(rdb, h.DB)
		h.LocalSkillListStore = handler.NewRedisLocalSkillListStore(rdb)
		h.LocalSkillImportStore = handler.NewRedisLocalSkillImportStore(rdb)
		h.LivenessStore = handler.NewRedisLivenessStore(rdb)
		h.MemberPresenceStore = handler.NewRedisMemberPresenceStore(rdb)
		h.WebhookRateLimiter = handler.NewRedisWebhookRateLimiter(rdb, handler.DefaultWebhookRateLimit())
		h.WebhookIPRateLimiter = handler.NewRedisWebhookIPRateLimiter(rdb, handler.DefaultWebhookIPRateLimit())
	}
	// LRM-462: human member presence from browser/app WS sessions.
	h.WireMemberPresenceHooks()

	// Lark integration. Only wired when MULTICA_LARK_SECRET_KEY is set:
	// the InstallationService refuses to fall back to plaintext storage
	// for app_secret, and the BindingTokenService cannot mint usable
	// tokens without it either. When the key is absent the Lark
	// handlers return 503 with a clear message; the rest of the server
	// continues to start so self-host deployments that have not opted
	// in to Lark are unaffected.
	if larkKey, err := secretbox.LoadKey("MULTICA_LARK_SECRET_KEY"); err == nil {
		box, err := secretbox.New(larkKey)
		if err != nil {
			slog.Error("lark: secretbox.New failed; lark integration disabled", "error", err)
		} else {
			installSvc, err := lark.NewInstallationService(queries, box)
			if err != nil {
				slog.Error("lark: InstallationService init failed; lark integration disabled", "error", err)
			} else {
				h.LarkInstallations = installSvc
				h.LarkBindingTokens = lark.NewBindingTokenService(queries, pool)
				slog.Info("lark integration enabled")

				// APIClient: wire the real Lark Open Platform HTTP client
				// (IM v1 send/patch + binding-prompt + bot info). Setting
				// MULTICA_LARK_SECRET_KEY is the operator's opt-in for
				// the integration as a whole; we don't expose a separate
				// "HTTP enabled" knob because the inbound dispatcher
				// without outbound replies is not a useful production
				// state, and CI / integration tests that want to avoid
				// real Lark traffic can point MULTICA_LARK_HTTP_BASE_URL
				// at a mock server.
				//
				// MULTICA_LARK_HTTP_BASE_URL is an OPTIONAL deployment-wide
				// override. Normal operation leaves it empty: each call then
				// resolves its open-platform host from the installation's
				// region (open.feishu.cn vs open.larksuite.com), so one
				// deployment serves both clouds. Set it only to force every
				// installation onto one host — a proxy, a mock for tests, or
				// a single-cloud staging setup.
				larkClient := lark.NewHTTPAPIClient(lark.HTTPClientConfig{
					BaseURL: strings.TrimSpace(os.Getenv("MULTICA_LARK_HTTP_BASE_URL")),
					Logger:  slog.Default(),
				})
				h.LarkAPIClient = larkClient
				patcher := lark.NewPatcher(queries, installSvc, larkClient, lark.PatcherConfig{})
				patcher.Register(bus)

				// Typing indicator: shows a "processing" reaction on the user's
				// message while the agent is working, then removes it before the
				// reply is sent. Best-effort; failures are logged only.
				typingIndicator := lark.NewTypingIndicatorManager(larkClient, installSvc, queries, slog.Default())
				patcher.SetTypingIndicatorManager(typingIndicator)

				// Inbound pipeline: lark_inbound_audit logger,
				// channel-aware ChatSessionService, and the
				// Dispatcher that orders identity / dedup / append /
				// /issue / enqueue per §4.3. The Dispatcher depends
				// on the same IssueService + TaskService that back
				// HTTP, so /issue-created issues share counter, dup
				// guard, project boundary, broadcast, analytics and
				// agent-enqueue with the rest of the product.
				auditLogger := lark.NewAuditLogger(queries)
				chatSvc := lark.NewChatSessionService(queries, pool)
				dispatcher := &lark.Dispatcher{
					Queries:      queries,
					Chat:         chatSvc,
					Audit:        auditLogger,
					IssueService: h.IssueService,
					TaskService:  h.TaskService,
					Logger:       slog.Default(),
				}
				// Debounce the per-session run trigger so a burst of
				// messages (e.g. "forward a transcript, then type a note")
				// collapses into one agent run instead of one per message.
				// MUL-2968.
				dispatcher.EnableRunBatching(lark.DefaultChatRunBatchWindow)

				// WS Hub: lease + supervisor goroutines per installation.
				// The WSLongConnConnector talks Lark's long-conn protocol
				// over gorilla/websocket. The connector wraps every read
				// with a ctx-cancel watchdog so lease loss / shutdown
				// breaks the blocking ReadMessage in bounded time — the
				// invariant §4.4 leans on. If the endpoint fetcher fails
				// to initialize (bad MULTICA_LARK_CALLBACK_BASE_URL or
				// similar config error), buildLarkConnectorFactory logs
				// and falls back to the NoopConnector so the lease /
				// supervisor lifecycle still runs against real DB rows —
				// inbound messages will be silently dropped until the
				// config is fixed, with the boot log labelling the mode
				// "noop" so operators can spot it.
				connectorFactory, connectorLabel := buildLarkConnectorFactory(installSvc, larkClient)
				h.LarkHub = lark.NewHub(queries, connectorFactory, dispatcher, lark.HubConfig{})
				h.LarkHub.SetTypingIndicatorManager(typingIndicator)

				// OutcomeReplier wires the outbound side of the
				// EventEmitter contract: NeedsBinding / AgentOffline /
				// AgentArchived translate to a Lark-side reply card.
				// Requires the real APIClient (the stub returns
				// ErrAPIClientNotConfigured on every send) and the
				// binding token service. When either is missing, the
				// Hub falls back to the noop replier and the outcomes
				// get logged but not delivered — clearly visible in
				// boot output so operators understand the gap.
				replier := lark.NewLarkOutcomeReplier(lark.OutcomeReplierConfig{
					APIClient:   larkClient,
					BindingSvc:  h.LarkBindingTokens,
					Credentials: installSvc,
					Queries:     queries,
					PublicURL:   signupConfig.PublicURL,
					Logger:      slog.Default(),
				})
				h.LarkHub.SetOutcomeReplier(replier)
				// The agent-offline / agent-archived notice is now decided
				// at debounce-flush time rather than synchronously from
				// Handle, so the dispatcher drives that reply itself through
				// the same replier. MUL-2968.
				dispatcher.FlushReply = replier.Reply
				slog.Info("lark inbound pipeline wired", "connector", connectorLabel)

				// One-shot union_id backfill for installations created
				// before migration 112 added bot_union_id. Runs off the
				// hot startup path so a slow Lark round-trip cannot block
				// HTTP listener boot. New installs already write
				// bot_union_id during the device-flow finalize, so this
				// is bridge code — it will simply find no rows to update
				// on a fresh deployment and exit. MUL-2671.
				go lark.BackfillBotUnionIDs(context.Background(), queries, larkClient, installSvc, slog.Default())

				// Upgrade repair for deployments that ran the whole
				// integration against Lark international via the deployment-
				// wide base-URL override before per-installation region
				// existed: migration 116 backfilled their rows to 'feishu',
				// so relabel them to 'lark' (their true cloud) before the
				// operator clears the override. No-op on mainland / fresh
				// deployments. Off the hot startup path like the union_id
				// backfill. MUL-3083.
				go lark.BackfillRegionFromLegacyOverride(context.Background(), queries,
					strings.TrimSpace(os.Getenv("MULTICA_LARK_HTTP_BASE_URL")),
					strings.TrimSpace(os.Getenv("MULTICA_LARK_CALLBACK_BASE_URL")),
					slog.Default())

				// Device-flow registration service: end-to-end install
				// pipeline that talks to accounts.feishu.cn (RFC 8628)
				// for the QR-scan handshake and then commits the
				// resulting Bot credentials + the installer's
				// lark_user_binding in one DB transaction. The optional
				// MULTICA_LARK_REGISTRATION_DOMAIN / _LARK_DOMAIN env
				// vars override the protocol hosts for staging / dev.
				regCfg := lark.RegistrationConfig{
					Domain:     strings.TrimSpace(os.Getenv("MULTICA_LARK_REGISTRATION_DOMAIN")),
					LarkDomain: strings.TrimSpace(os.Getenv("MULTICA_LARK_REGISTRATION_LARK_DOMAIN")),
				}
				regClient := lark.NewRegistrationClient(regCfg)
				regSvc, rerr := lark.NewRegistrationService(
					lark.RegistrationServiceConfig{Logger: slog.Default()},
					regClient,
					larkClient,
					queries,
					pool,
					installSvc,
					h.LarkBindingTokens,
				)
				if rerr != nil {
					slog.Error("lark: RegistrationService init failed; install disabled", "error", rerr)
				} else {
					// Publish lark_installation:created at row-commit time so the
					// connection badge refreshes on every workspace client, not just
					// the tab that polls the install status to success.
					regSvc.SetEventBus(bus)
					h.LarkRegistration = regSvc
					slog.Info("lark device-flow install enabled")
				}
			}
		}
	} else {
		slog.Info("lark integration disabled (MULTICA_LARK_SECRET_KEY not set)")
	}
	if opts.HeartbeatScheduler != nil {
		h.HeartbeatScheduler = opts.HeartbeatScheduler
	}
	// Auth caches: PAT cache is shared between the regular Auth middleware,
	// the DaemonAuth fallback (mul_) path, and the revoke handler
	// (invalidate). DaemonTokenCache backs the DaemonAuth mdt_ path. Both
	// constructors return nil when rdb is nil — every consumer handles that
	// as "no cache, always hit DB".
	patCache := auth.NewPATCache(rdb)
	daemonTokenCache := auth.NewDaemonTokenCache(rdb)
	h.PATCache = patCache
	h.DaemonTokenCache = daemonTokenCache
	h.MembershipCache = auth.NewMembershipCache(rdb)

	// Cloud PAT verifier: validates mcn_ tokens against Multica Cloud
	// Fleet. Returns nil when no Fleet URL is configured — the Auth /
	// DaemonAuth middlewares treat nil as "mcn_ not supported" and
	// reject with 401, instead of falling through to mul_/JWT paths.
	// Reuses MULTICA_CLOUD_FLEET_URL (the same URL the cloud-runtime
	// proxy uses) so a deployment doesn't need a second config knob.
	cloudPATVerifier := auth.NewCloudPATVerifier(auth.CloudPATVerifierConfig{
		FleetBaseURL: signupConfig.CloudRuntimeFleetURL,
		Redis:        rdb,
	})

	// Wire WS heartbeat after stores are finalized so the WS path uses the
	// same (possibly Redis-backed) stores as the HTTP path.
	daemonHub.SetHeartbeatHandler(h.HandleDaemonWSHeartbeat)
	daemonHub.SetReminderHandlers(
		h.HandleDaemonReminderSnapshot,
		h.HandleDaemonReminderFireAttempt,
		h.HandleDaemonReminderOwnerLifecycle,
		h.HandleDaemonReminderOwnerLifecycleAck,
	)
	daemonHub.SetReminderProjectionHandlers(
		h.HandleDaemonReminderProjection,
		h.HandleDaemonReminderProjectionAck,
	)
	daemonHub.SetAgentDeliveryAckHandler(h.HandleAgentDeliveryAck)
	daemonHub.SetAgentRecoveryHandler(h.HandleAgentMessageRecovery)
	daemonHub.SetAgentMessageHandoffHandler(h.HandleAgentMessageHandoff)
	health := newServerHealth(pool)

	r := chi.NewRouter()

	// Global middleware
	r.Use(chimw.RequestID)
	r.Use(middleware.ClientMetadata)
	r.Use(middleware.RequestLogger)
	if opts.HTTPMetrics != nil {
		r.Use(opts.HTTPMetrics.Middleware)
	}
	r.Use(chimw.Recoverer)
	r.Use(middleware.ContentSecurityPolicy)
	origins := allowedOrigins()

	// Share allowed origins with WebSocket origin checker.
	realtime.SetAllowedOrigins(origins)

	// Share the same trusted-proxy CIDRs (MULTICA_TRUSTED_PROXIES) so the
	// WebSocket origin check honors X-Forwarded-Host only from trusted proxies,
	// using one config source instead of a parallel one.
	realtime.SetTrustedProxies(signupConfig.TrustedProxies)

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   origins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Workspace-ID", "X-Workspace-Slug", "X-Request-ID", "X-Agent-ID", "X-Task-ID", "X-CSRF-Token", "X-Client-Platform", "X-Client-Version", "X-Client-OS"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Health / readiness checks
	r.Get("/health", health.liveHandler)
	r.Get("/readyz", health.readyHandler)
	r.Get("/healthz", health.readyHandler)

	// Realtime subsystem metrics — connection counts, slow-client evictions,
	// and per-event-type send QPS counters. Exposed as JSON so it can be
	// scraped by ops or surfaced in the admin UI without adding a Prometheus
	// dependency. See MUL-1138 (Phase 0).
	//
	// Access is restricted (MUL-1342): when REALTIME_METRICS_TOKEN is set,
	// callers must present it via Authorization: Bearer <token>. When the
	// env var is unset the handler only serves loopback callers so local
	// dev keeps working without exposing the metrics on a public listener.
	r.Get("/health/realtime", realtimeMetricsHandler(os.Getenv("REALTIME_METRICS_TOKEN")))

	// WebSocket
	mc := &membershipChecker{queries: queries}
	pr := &patResolver{queries: queries, cache: patCache}
	slugResolver := realtime.SlugResolver(func(ctx context.Context, slug string) (string, error) {
		ws, err := queries.GetWorkspaceBySlug(ctx, slug)
		if err != nil {
			return "", err
		}
		return util.UUIDToString(ws.ID), nil
	})
	r.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
		realtime.HandleWebSocket(hub, mc, pr, slugResolver, w, r)
	})

	// Local file serving (when using local storage)
	if local, ok := store.(*storage.LocalStorage); ok {
		r.Get("/uploads/*", func(w http.ResponseWriter, r *http.Request) {
			file := strings.TrimPrefix(r.URL.Path, "/uploads/")
			local.ServeFile(w, r, file)
		})
	}

	// Auth (public) — per-IP rate limiting.
	if rdb == nil {
		slog.Warn("rate limiting disabled: REDIS_URL not configured")
	}
	trustedProxies := middleware.ParseTrustedProxies(os.Getenv("RATE_LIMIT_TRUSTED_PROXIES"))
	authRL := middleware.RateLimit(rdb, envPositiveInt("RATE_LIMIT_AUTH", 5), time.Minute, trustedProxies)
	authVerifyRL := middleware.RateLimit(rdb, envPositiveInt("RATE_LIMIT_AUTH_VERIFY", 20), time.Minute, trustedProxies)
	contactSalesRL := middleware.RateLimit(rdb, envPositiveInt("RATE_LIMIT_CONTACT_SALES", 5), time.Hour, trustedProxies)
	r.With(authRL).Post("/auth/send-code", h.SendCode)
	r.With(authVerifyRL).Post("/auth/verify-code", h.VerifyCode)
	r.With(authRL).Post("/auth/google", h.GoogleLogin)
	r.Post("/auth/logout", h.Logout)

	// Device authorization (RFC 8628) — task #36. Public: the CLI calling
	// these two has no session yet (that's the point of the flow); the
	// bearer secret is device_code itself, not a session/API token.
	deviceCodeRL := middleware.RateLimit(rdb, envPositiveInt("RATE_LIMIT_DEVICE_CODE", 5), time.Minute, trustedProxies)
	deviceTokenRL := middleware.RateLimit(rdb, envPositiveInt("RATE_LIMIT_DEVICE_TOKEN", 30), time.Minute, trustedProxies)
	r.With(deviceCodeRL).Post("/api/device/code", h.RequestDeviceCode)
	r.With(deviceTokenRL).Post("/api/device/token", h.IssueDeviceToken)

	// Public API
	r.Get("/api/config", h.GetConfig)
	r.With(contactSalesRL).Post("/api/contact-sales", h.CreateContactSales)

	// Sticker library — embedded, non-sensitive assets. Public + unauthenticated
	// because the images are loaded by <img> tags (which can't send auth headers)
	// and the catalog is global, not workspace-scoped.
	r.Get("/api/stickers", h.ListStickers)
	r.Get("/api/stickers/{id}", h.GetStickerAsset)

	// LRM-1049: Autopilot webhook ingress removed (product retired).
	// GitHub App webhook (no Multica auth — requests are authenticated via
	// HMAC-SHA256 signature in the handler) and post-install setup callback.
	r.Post("/api/webhooks/github", h.HandleGitHubWebhook)
	r.Get("/api/github/setup", h.GitHubSetupCallback)
	// Stripe webhook (no Multica auth — Stripe signs the raw body
	// with a shared secret, the multica-cloud upstream verifies. We
	// only forward the bytes + the Stripe-Signature header; see
	// HandleCloudBillingStripeWebhook for the rationale).
	r.Post("/api/webhooks/stripe", h.HandleCloudBillingStripeWebhook)
	// Volcengine RTC callbacks (no Multica auth). GET is the provider's
	// connectivity check. POST supports both signed server-level VoiceChat
	// events and signed binary conversation messages.
	r.Get(voiceCallCallbackPath, h.HandleVoiceCallCallback)
	r.Post(voiceCallCallbackPath, h.HandleVoiceCallCallback)
	// Volcengine RTC CustomLLM endpoint (no Multica user auth). A dedicated
	// bearer credential authenticates the provider before any transcript is
	// decoded.
	r.Post(voiceCallLLMPath, h.HandleVoiceCallLLM)

	// Daemon API routes (require daemon token or valid user token)
	r.Route("/api/daemon", func(r chi.Router) {
		r.Use(middleware.DaemonAuth(queries, patCache, daemonTokenCache, cloudPATVerifier))

		r.Post("/register", h.DaemonRegister)
		r.Post("/deregister", h.DaemonDeregister)
		r.Post("/starting", h.DaemonMarkStarting)
		r.Post("/heartbeat", h.DaemonHeartbeat)
		r.Get("/ws", h.DaemonWebSocket)
		r.Post("/runtimes/{runtimeId}/agent-inbox/drain", h.DrainAgentInboxByRuntime)
		r.Post("/runtimes/{runtimeId}/agents/{agentId}/credential", h.EnsureDaemonAgentCredential)
		r.Get("/runtimes/{runtimeId}/agents/{agentId}/runtime-config", h.DaemonGetAgentRuntimeConfig)
		r.Post("/runtimes/{runtimeId}/agents/{agentId}/crashed", h.ReportAgentProviderCrashed)
		r.Post("/runtimes/{runtimeId}/agents/{agentId}/crashed/clear", h.ClearAgentProviderCrashed)
		r.Post("/runtimes/{runtimeId}/agents/{agentId}/session/reset", h.ResetAgentRuntimeSession)
		r.Get("/runtimes/{runtimeId}/tasks/pending", h.ListPendingTasksByRuntime)
		r.Post("/runtimes/{runtimeId}/update/{updateId}/result", h.ReportUpdateResult)
		r.Post("/runtimes/{runtimeId}/machine-upgrades/{upgradeId}/accept", h.AcceptMachineUpgrade)
		r.Post("/runtimes/{runtimeId}/machine-upgrades/{upgradeId}/progress", h.ReportMachineUpgradeProgress)
		r.Post("/runtimes/{runtimeId}/models/{requestId}/result", h.ReportModelListResult)
		r.Post("/runtimes/{runtimeId}/local-skills/{requestId}/result", h.ReportLocalSkillListResult)
		r.Post("/runtimes/{runtimeId}/local-skills/import/{requestId}/result", h.ReportLocalSkillImportResult)
		r.Post("/runtimes/{runtimeId}/shared-skills/sync", h.SyncRuntimeSharedSkills)
		r.Post("/runtimes/{runtimeId}/evolution/submissions", h.SyncEvolutionSubmissions)
		r.Post("/runtimes/{runtimeId}/memory-curation/{runId}/result", h.ReportMemoryCurationRunResult)
		r.Post("/runtimes/{runtimeId}/agent-lifecycle/{operationId}/result", h.ReportAgentLifecycleOperationResult)
		r.Post("/runtimes/{runtimeId}/agent-start-intents/{startDispatchId}/report", h.ReportAgentStartIntent)
		r.Post("/agent-memory-writes", h.ReportAgentMemoryWrites)
		r.Post("/agent-memory-center/sync", h.SyncAgentMemoryCenter)
		r.Post("/agent-memory-center/hydrate", h.HydrateAgentMemoryCenter)

		r.Get("/tasks/{taskId}/status", h.GetTaskStatus)
		r.Post("/tasks/{taskId}/progress", h.ReportTaskProgress)
		r.Post("/tasks/{taskId}/messages", h.ReportTaskMessages)
		r.Get("/tasks/{taskId}/messages", h.ListTaskMessages)
		r.Post("/agent-inbox/events/{eventId}/ack", h.AckAgentInboxEvent)
		r.Post("/agent-inbox/events/{eventId}/renew", h.RenewAgentInboxEvent)
		r.Post("/agent-inbox/events/{eventId}/execution", h.StartAgentInboxExecution)
		r.Post("/agent-inbox/events/{eventId}/usage", h.ReportAgentInboxUsage)
		r.Post("/agent-inbox/events/{eventId}/messages", h.ReportAgentInboxMessages)
		r.Post("/agent-inbox/events/{eventId}/complete", h.CompleteAgentInboxEvent)
		r.Post("/agent-inbox/events/{eventId}/fail", h.FailAgentInboxEvent)

		r.Post("/runtimes/{runtimeId}/recover-orphans", h.RecoverOrphanedTasks)
		r.Post("/tasks/{taskId}/session", h.PinTaskSession)
	})

	// Sandbox node API routes. Node tokens (msn_) identify shared sandbox
	// infrastructure; job tokens (mst_) are scoped to a single claimed job.
	r.Route("/api/sandbox/node", func(r chi.Router) {
		r.Use(middleware.SandboxNodeAuth(queries))
		r.Post("/register", h.SandboxNodeRegister)
		r.Post("/heartbeat", h.SandboxNodeHeartbeat)
		r.Get("/ws", h.SandboxNodeWebSocket)
		r.Post("/jobs/claim", h.ClaimSandboxJobs)
	})
	r.Route("/api/sandbox/jobs", func(r chi.Router) {
		r.Use(middleware.SandboxJobAuth(queries))
		r.Post("/{jobId}/start", h.StartSandboxJob)
		r.Post("/{jobId}/complete", h.CompleteSandboxJob)
		r.Post("/{jobId}/fail", h.FailSandboxJob)
	})

	// Diagnosis-run API (spec 005). The sandboxed diagnosis agent reaches its
	// tool surface exclusively here, authenticated by a per-run capability
	// token (no user JWT); DiagnosisRunAuth resolves {runID}, verifies the
	// token against the run's stored hash, and injects the run record.
	r.Route("/api/v1/diagnosis-runs/{runID}", func(r chi.Router) {
		r.Use(middleware.DiagnosisRunAuth(diagnosisRunLoaderAdapter{store: service.NewDiagnosisStateStore(queries)}))
		r.Post("/get-segment-messages", h.DiagnosisRunGetSegmentMessages)
		r.Post("/record-step-rewards", h.DiagnosisRunRecordStepRewards)
		r.Get("/diagnosis-progress", h.DiagnosisRunProgress)
		r.Post("/finish-segment", h.DiagnosisRunFinishSegment)
		r.Post("/complete-diagnosis", h.DiagnosisRunCompleteDiagnosis)
		r.Get("/task-context", h.DiagnosisRunTaskContext)
	})

	// Protected API routes
	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(queries, patCache, cloudPATVerifier))
		r.Use(middleware.RefreshCloudFrontCookies(cfSigner))
		// #801: AgentPrincipal may only use /api/agent/* (fail-closed admin/human).
		r.Use(middleware.RejectAgentOnHumanAPI)

		// --- User-scoped routes (no workspace context required) ---
		r.Get("/api/me", h.GetMe)
		r.Patch("/api/me", h.UpdateMe)
		r.Patch("/api/me/onboarding", h.PatchOnboarding)
		r.Post("/api/me/onboarding/complete", h.CompleteOnboarding)
		r.Post("/api/me/onboarding/cloud-waitlist", h.JoinCloudWaitlist)
		// DEPRECATED — shim routes for desktop < v3 during the rollout
		// window. v3 frontend creates the Helper agent + starter issue
		// via generic CreateAgent / CreateIssue and only calls /complete
		// here. Remove once X-Client-Version telemetry confirms zero
		// pre-v3 desktops are still calling these. Handlers live in
		// server/internal/handler/onboarding_shim.go.
		r.Post("/api/me/onboarding/runtime-bootstrap", h.BootstrapOnboardingRuntime)
		r.Post("/api/me/onboarding/no-runtime-bootstrap", h.BootstrapOnboardingNoRuntime)
		r.Post("/api/cli-token", h.IssueCliToken)
		r.Post("/api/upload-file", h.UploadFile)
		r.Post("/api/feedback", h.CreateFeedback)
		r.Get("/api/honor/rules", h.GetHonorRules)
		r.Get("/api/me/honor", h.GetMyHonor)
		r.Get("/api/me/honor/compare", h.GetHonorCompare)
		r.Patch("/api/me/honor", h.PatchMyHonor)
		r.Post("/api/me/honor/presence", h.PostHonorPresence)
		r.Get("/api/users/{userId}/honor", h.GetUserHonor)

		// Attachment download — user-scoped (auth-only), NOT
		// workspace-scoped. The handler self-resolves the workspace
		// from the attachment row and enforces membership inside, so
		// this route is callable as a native browser <img>/<video>
		// src that cannot attach X-Workspace-Slug / X-Workspace-ID
		// headers. Persisting `/api/attachments/<id>/download` into
		// comment markdown depends on this — see MUL-3130. The
		// metadata / delete endpoints below stay workspace-scoped
		// because they are JSON-API consumers that always have
		// workspace context.
		r.Get("/api/attachments/{id}/download", h.DownloadAttachment)

		r.Route("/api/workspaces", func(r chi.Router) {
			r.Get("/", h.ListWorkspaces)
			r.Post("/", h.CreateWorkspace)
			r.Route("/{id}", func(r chi.Router) {
				// Member-level access
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireWorkspaceMemberFromURL(queries, "id"))
					r.Get("/", h.GetWorkspace)
					r.Get("/members", h.ListMembersWithUser)
					r.Get("/member-presence", h.ListMemberPresence)
					r.Get("/memory-curation/status", h.GetWorkspaceMemoryCurationStatus)
					r.Get("/memory-curation/profile", h.GetMemoryCuratorProfile)
					r.Put("/memory-curation/profile", h.UpdateMemoryCuratorProfile)
					r.Get("/memory-curation/daily-summary", h.ListMemoryCurationDailySummary)
					r.Get("/memory-curation/candidates", h.ListMemoryCurationCandidates)
					r.Get("/memory-curation/candidates/{candidateId}", h.GetMemoryCurationCandidate)
					r.Get("/memory-curation/team-knowledge", h.ListTeamKnowledgeItems)
					r.Get("/memory-curation/team-knowledge/{itemId}", h.GetTeamKnowledgeItem)
					r.Get("/memory-curation/team-knowledge/{itemId}/neighbors", h.ListKnowledgeNeighbors)
					r.Post("/knowledge/promote", h.PromoteKnowledgePage)
					r.Route("/voice-calls", func(r chi.Router) {
						r.Use(handler.RequireHumanActor)
						r.Post("/", h.CreateVoiceCall)
						r.Get("/{callId}", h.GetVoiceCall)
						r.Post("/{callId}/connect", h.ConnectVoiceCall)
						r.Post("/{callId}/answer", h.AnswerVoiceCall)
						r.Post("/{callId}/stop", h.StopVoiceCall)
						// Duplex product path (LRM-949): activate + WS media.
						// Independent of RTC VoiceChat connect; requires DOUBAO_DIALOG_API_KEY.
						r.Post("/{callId}/duplex", h.StartVoiceCallDuplex)
						r.Get("/{callId}/duplex/ws", h.VoiceCallDuplexWS)
					})
					r.Post("/leave", h.LeaveWorkspace)
					r.Get("/invitations", h.ListWorkspaceInvitations)
					// Listing GitHub installations is member-visible so the
					// integrations tab no longer renders blank for non-admins;
					// the handler strips the management handle and adds a
					// can_manage hint so the UI can gate connect/disconnect.
					r.Get("/github/installations", h.ListGitHubInstallations)
					r.Get("/sandbox/bindings", h.ListWorkspaceSandboxBindings)
					// Members may bind nodes they own; UpsertSandboxWorkspaceBinding
					// enforces owner_user_id = caller. Admin gate blocked the
					// Sandboxes "Add node" flow after workspace switch for
					// non-admins with a raw "insufficient permissions" error.
					r.Post("/sandbox/bindings", h.BindSandboxNodeToWorkspace)
					r.Patch("/agents/{agentId}/role", h.UpdateAgentWorkspaceRole)
					// Read-only inventory/diagnostic for fleet update visibility
					// (#815 companion). Does not mutate update state.
					r.Get("/runtimes/update-inventory-diagnostic", h.GetWorkspaceUpdateInventoryDiagnostic)
				})
				// Admin-level access
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireWorkspaceRoleFromURL(queries, "id", "owner", "admin"))
					r.Put("/", h.UpdateWorkspace)
					r.Patch("/", h.UpdateWorkspace)
					r.Post("/memory-curation/runs", h.StartMemoryCurationRun)
					r.Get("/memory-curation/backfill-preview", h.PreviewMemoryCurationBackfill)
					r.Post("/memory-curation/backfill", h.StartMemoryCurationBackfill)
					r.Get("/memory-curation/runs/{runId}", h.GetMemoryCurationRun)
					r.Post("/members", h.CreateInvitation)
					r.Route("/members/{memberId}", func(r chi.Router) {
						r.Patch("/", h.UpdateMember)
						r.Delete("/", h.DeleteMember)
					})
					r.Delete("/invitations/{invitationId}", h.RevokeInvitation)
				})
				// Owner-only access
				r.With(middleware.RequireWorkspaceRoleFromURL(queries, "id", "owner")).Delete("/", h.DeleteWorkspace)

				// GitHub integration — connect / disconnect remain admin-only;
				// the read-only list endpoint lives in the member-level group
				// above so non-admins can see the workspace's connection state.
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireWorkspaceRoleFromURL(queries, "id", "owner", "admin"))
					r.Get("/github/connect", h.GitHubConnect)
					r.Delete("/github/installations/{installationId}", h.DeleteGitHubInstallation)
				})

				// Lark integration. Listing is member-visible (same
				// rationale as GitHub: the Integrations tab must
				// render for non-admins so they see "wired up by whom").
				// Install / revoke require admin to prevent a non-admin
				// from binding a Bot to a workspace agent or yanking
				// an installation out from under one.
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireWorkspaceMemberFromURL(queries, "id"))
					r.Get("/lark/installations", h.ListLarkInstallations)
				})
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireWorkspaceRoleFromURL(queries, "id", "owner", "admin"))
					r.Delete("/lark/installations/{installationId}", h.RevokeLarkInstallation)
					// Device-flow scan-to-install. Begin opens a new
					// registration session against Lark and returns
					// the QR-code URL; the frontend dialog then polls
					// /install/{sessionId}/status until success or
					// terminal failure.
					r.Post("/lark/install/begin", h.BeginLarkInstall)
					r.Get("/lark/install/{sessionId}/status", h.GetLarkInstallStatus)
				})
			})
		})

		// Lark binding-token redemption. NOT workspace-scoped because
		// the redeemer hits this BEFORE they have any workspace
		// context — the redemption itself is what mints their
		// lark_user_binding row. Identity comes from the session;
		// the token only proves "this open_id requested binding," and
		// is combined with the logged-in user to create the mapping.
		r.Post("/api/lark/binding/redeem", h.RedeemLarkBindingToken)

		// Web Push VAPID public key and unbind. Authenticated but not workspace-
		// scoped so logout can remove the browser/device binding reliably.
		r.Get("/api/web-push/public-key", h.GetWebPushPublicKey)
		r.Delete("/api/web-push/subscriptions", h.DeleteWebPushSubscription)

		// User-scoped invitation routes (no workspace context required)
		r.Get("/api/invitations", h.ListMyInvitations)
		r.Get("/api/invitations/{id}", h.GetMyInvitation)
		r.Post("/api/invitations/{id}/accept", h.AcceptInvitation)
		r.Post("/api/invitations/{id}/decline", h.DeclineInvitation)

		r.Route("/api/tokens", func(r chi.Router) {
			r.Get("/", h.ListPersonalAccessTokens)
			r.Post("/", h.CreatePersonalAccessToken)
			r.Post("/current/renew", h.RenewCurrentPersonalAccessToken)
			r.Delete("/{id}", h.RevokePersonalAccessToken)
		})

		// Device authorization (RFC 8628) confirmation — task #36. Any
		// logged-in user may confirm/deny a device code for their own
		// account; not workspace-scoped (a PAT isn't workspace-scoped).
		r.Get("/api/device/pending", h.GetPendingDeviceAuthorization)
		r.Post("/api/device/confirm", h.ConfirmDeviceAuthorization)

		// Sandbox node administration. Node CRUD is authenticated + owner-scoped;
		// workspace use requires an explicit membership-scoped binding (any
		// member may bind a node they own).
		r.Get("/api/sandbox/nodes", h.ListSandboxNodes)
		r.Post("/api/sandbox/nodes", h.CreateSandboxNode)
		r.Get("/api/sandbox/nodes/{nodeId}/templates", h.ListSandboxNodeTemplates)
		r.Get("/api/sandbox/nodes/{nodeId}/docker-images", h.ListSandboxNodeDockerImages)
		r.Patch("/api/sandbox/nodes/{nodeId}", h.UpdateSandboxNode)
		r.Delete("/api/sandbox/nodes/{nodeId}", h.DeleteSandboxNode)
		r.Post("/api/sandbox/nodes/{nodeId}/tokens", h.CreateSandboxNodeToken)

		// Cloud Billing proxy. Same upstream service / port as
		// cloud-runtime — multica-cloud's Fleet and Billing share
		// :8080 and the same chi router. All routes here forward
		// to /api/v1/billing/* with X-User-ID stamped from the
		// authenticated context.
		//
		// User-scoped (account-level), NOT workspace-scoped — sits
		// outside the RequireWorkspaceMember group so a user can
		// inspect their balance, top up, and open the Billing Portal
		// without an active workspace selected. The upstream owner
		// model is single-user; X-Workspace-ID would be ignored even
		// if we sent it. The Stripe webhook is the public outlier
		// and lives outside the entire Auth group (see above).
		//
		// IMPORTANT — task-token actors are blocked here. The Auth
		// middleware happily turns an mat_ task token into a normal
		// X-User-ID stamp (so agents can comment, claim issues, etc.
		// as their owner), but billing is account-level and a running
		// agent reading its owner's balance / opening a checkout
		// session is the kind of lateral-movement we're explicitly
		// trying to prevent. handler.RequireHumanActor checks the
		// authoritative server-set X-Actor-Source header and 403s
		// any task-token request. See actor_guards.go for the full
		// rationale.
		r.Route("/api/cloud-billing", func(r chi.Router) {
			r.Use(handler.RequireHumanActor)

			r.Get("/balance", h.GetCloudBillingBalance)
			r.Get("/transactions", h.ListCloudBillingTransactions)
			r.Get("/batches", h.ListCloudBillingBatches)
			r.Get("/topups", h.ListCloudBillingTopups)
			r.Get("/price-tiers", h.ListCloudBillingPriceTiers)
			r.Post("/checkout-sessions", h.CreateCloudBillingCheckoutSession)
			r.Get("/checkout-sessions/{sessionId}", h.GetCloudBillingCheckoutSession)
			r.Post("/portal-sessions", h.CreateCloudBillingPortalSession)
		})

		// --- Workspace-scoped routes (all require workspace membership) ---
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireWorkspaceMember(queries))

			// Voice synthesis and recognition consume account-level provider
			// quota. Workspace membership scopes the request; the human-actor
			// guard prevents task and infrastructure credentials from spending
			// that quota through this user-facing surface.
			r.Route("/api/voice", func(r chi.Router) {
				r.Use(handler.RequireHumanActor)
				r.Post("/tts", h.SynthesizeVoice)
				r.Post("/asr", h.TranscribeVoice)
			})

			// Workspace global search (Channels / DMs / Messages / People).
			r.Get("/api/search", h.SearchGlobal)

			// Assignee frequency
			r.Get("/api/assignee-frequency", h.GetAssigneeFrequency)
			r.Get("/api/member-profiles/{memberType}/{memberId}", h.GetMemberProfile)

			// Notes
			r.Route("/api/notes/pages", func(r chi.Router) {
				r.Get("/", h.ListNotePages)
				r.Post("/", h.CreateNotePage)
				r.Get("/trash", h.ListDeletedNotePages)
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", h.GetNotePage)
					r.Patch("/", h.UpdateNotePage)
					r.Delete("/", h.DeleteNotePage)
					r.Post("/duplicate", h.DuplicateNotePage)
					r.Delete("/permanent", h.PermanentlyDeleteNotePage)
					r.Post("/restore", h.RestoreNotePage)
					r.Put("/shares", h.UpdateNotePageShares)
				})
			})

			// Issues
			r.Route("/api/issues", func(r chi.Router) {
				r.Get("/search", h.SearchIssues)
				r.Get("/review-stats", h.GetIssueReviewStats)
				r.Get("/child-progress", h.ChildIssueProgress)
				r.Get("/children", h.ListChildrenByParents)
				r.Get("/grouped", h.ListGroupedIssues)
				r.Get("/", h.ListIssues)
				r.Post("/", h.CreateIssue)
				r.Post("/quick-create", h.QuickCreateIssue)
				r.Post("/batch-update", h.BatchUpdateIssues)
				r.Post("/batch-delete", h.BatchDeleteIssues)
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", h.GetIssue)
					r.Put("/", h.UpdateIssue)
					r.Put("/channel", h.SetIssueSourceChannel)
					r.Delete("/", h.DeleteIssue)
					r.Post("/comments/trigger-preview", h.PreviewCommentTriggers)
					r.Post("/comments", h.CreateComment)
					r.Get("/comments", h.ListComments)
					r.Get("/timeline", h.ListTimeline)
					r.Get("/subscribers", h.ListIssueSubscribers)
					r.Post("/subscribe", h.SubscribeToIssue)
					r.Post("/unsubscribe", h.UnsubscribeFromIssue)
					r.Get("/active-task", h.GetActiveTaskForIssue)
					r.Post("/tasks/{taskId}/cancel", h.CancelTask)
					r.Post("/rerun", h.RerunIssue)
					r.Get("/task-runs", h.ListTasksByIssue)
					r.Get("/usage", h.GetIssueUsage)
					r.Post("/reactions", h.AddIssueReaction)
					r.Delete("/reactions", h.RemoveIssueReaction)
					r.Get("/attachments", h.ListAttachments)
					r.Get("/children", h.ListChildIssues)
					r.Get("/labels", h.ListLabelsForIssue)
					r.Post("/labels", h.AttachLabel)
					r.Delete("/labels/{labelId}", h.DetachLabel)
					r.Get("/metadata", h.ListIssueMetadata)
					r.Put("/metadata/{key}", h.SetIssueMetadataKey)
					r.Delete("/metadata/{key}", h.DeleteIssueMetadataKey)
					r.Get("/pull-requests", h.ListPullRequestsForIssue)
				})
			})

			// Sandbox instances (workspace-facing control plane).
			r.Get("/api/sandboxes", h.ListSandboxInstances)
			r.Post("/api/sandboxes", h.CreateSandboxInstance)
			r.Get("/api/sandbox/nodes/{nodeId}/snapshots", h.ListSandboxNodeSnapshots)
			r.Delete("/api/sandbox/snapshots/{snapshotId}", h.DeleteSandboxSnapshot)
			r.Route("/api/sandboxes/{instanceId}", func(r chi.Router) {
				r.Get("/", h.GetSandboxInstance)
				r.Patch("/", h.UpdateSandboxInstance)
				r.Delete("/", h.DeleteSandboxInstance)
				r.Post("/stop", h.StopSandboxInstance)
				r.Post("/resume", h.ResumeSandboxInstance)
				r.Post("/create-template", h.CreateSandboxSnapshotTemplate)
			})

			// Task messages (user-facing, not daemon auth)
			r.Get("/api/tasks/{taskId}/messages", h.ListTaskMessagesByUser)

			r.Get("/api/evolution/metrics", h.GetEvolutionMetrics)

			r.Route("/api/evolution/training/examples", func(r chi.Router) {
				r.Use(middleware.RequireWorkspaceRole(queries, "owner", "admin"))
				r.Get("/", h.ListEvolutionTrainingExamples)
				r.Post("/", h.CreateEvolutionTrainingExample)
				r.Patch("/{exampleId}", h.UpdateEvolutionTrainingExample)
			})
			r.Route("/api/evolution/model-configs", func(r chi.Router) {
				r.Use(middleware.RequireWorkspaceRole(queries, "owner", "admin"))
				r.Get("/", h.ListEvolutionModelRuntimeConfigs)
				r.Put("/{modelKind}", h.UpdateEvolutionModelRuntimeConfig)
			})
			r.Route("/api/evolution/model-evals", func(r chi.Router) {
				r.Use(middleware.RequireWorkspaceRole(queries, "owner", "admin"))
				r.Get("/", h.ListEvolutionModelEvalRuns)
				r.Post("/", h.CreateEvolutionModelEvalRun)
			})

			r.Route("/api/evolution/units/{unitId}/versions", func(r chi.Router) {
				r.Use(middleware.RequireWorkspaceRole(queries, "owner", "admin"))
				r.Get("/", h.ListEvolutionSkillVersions)
				r.Get("/{versionId}", h.GetEvolutionSkillVersion)
				r.Get("/{versionId}/eval", h.GetEvolutionSkillVersionEval)
				r.Post("/{versionId}/rollback", h.RollbackEvolutionSkillVersion)
			})

			r.Route("/api/evolution/submissions", func(r chi.Router) {
				r.Use(middleware.RequireWorkspaceRole(queries, "owner", "admin"))
				r.Get("/", h.ListEvolutionReviewSubmissions)
				r.Get("/{submissionId}", h.GetEvolutionReviewSubmission)
				r.Post("/{submissionId}/rerun", h.RerunEvolutionCandidate)
				r.Post("/{submissionId}/promote", h.PromoteEvolutionReviewSubmission)
				r.Post("/{submissionId}/reject", h.RejectEvolutionReviewSubmission)
				r.Put("/{submissionId}/source-skill", h.SetEvolutionSourceSkillAssignment)
			})

			// Labels
			r.Route("/api/labels", func(r chi.Router) {
				r.Get("/", h.ListLabels)
				r.Post("/", h.CreateLabel)
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", h.GetLabel)
					r.Put("/", h.UpdateLabel)
					r.Delete("/", h.DeleteLabel)
				})
			})

			// Projects
			r.Route("/api/projects", func(r chi.Router) {
				r.Get("/search", h.SearchProjects)
				r.Get("/", h.ListProjects)
				r.Post("/", h.CreateProject)
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", h.GetProject)
					r.Put("/", h.UpdateProject)
					r.Delete("/", h.DeleteProject)
					r.Get("/channels", h.ListProjectChannels)
					r.Get("/resources", h.ListProjectResources)
					r.Post("/resources", h.CreateProjectResource)
					r.Put("/resources/{resourceId}", h.UpdateProjectResource)
					r.Delete("/resources/{resourceId}", h.DeleteProjectResource)
				})
			})

			// Research Fleet
			r.Route("/api/research", func(r chi.Router) {
				r.Get("/fleet", h.GetResearchFleet)
				r.Post("/fleet/ensure", h.EnsureResearchFleet)
				r.Post("/fleet/members", h.HireResearchFleetMember)
				r.Post("/fleet/members/{memberId}/optimize", h.OptimizeResearchFleetMember)
				r.Post("/fleet/members/{memberId}/archive", h.ArchiveResearchFleetMemberHandler)
				r.Get("/sessions", h.ListResearchSessions)
				r.Post("/sessions", h.CreateResearchSession)
				r.Route("/sessions/{id}", func(r chi.Router) {
					r.Get("/", h.GetResearchSessionSnapshot)
					r.Get("/presence", h.GetResearchPresence)
					r.Delete("/", h.DeleteResearchSession)
					r.Post("/messages", h.PostResearchMessage)
					r.Put("/messages/{messageId}/match-decision", h.PutResearchMessageMatchDecision)
					r.Post("/steer", h.SteerResearchRun)
					r.Post("/stop", h.StopResearchSession)
					r.Post("/graph/nodes", h.AppendResearchGraphNode)
					r.Post("/nodes/{nodeId}/commands", h.PostResearchNodeCommand)
					r.Post("/sources", h.UpsertResearchSourceHandler)
					r.Post("/report", h.PatchResearchReport)
					r.Post("/presence", h.PostResearchPresence)
					r.Post("/stage-eval", h.RequestResearchStageEval)
					r.Get("/product-rounds", h.ListResearchProductRoundCards)
					r.Get("/product-rounds/{round}", h.GetResearchProductRoundCard)
					r.Post("/product-rounds/judgment", h.SubmitResearchProductRoundJudgment)
					r.Post("/confirm", h.ConfirmResearchSession)
					r.Post("/archive", h.ArchiveResearchSession)
					r.Post("/single-line", h.ConfirmResearchSingleLine)
					r.Post("/handoff", h.ResearchSessionHandoff)
				})
			})

			// Pins
			r.Route("/api/pins", func(r chi.Router) {
				r.Get("/", h.ListPins)
				r.Post("/", h.CreatePin)
				r.Put("/reorder", h.ReorderPins)
				r.Delete("/{itemType}/{itemId}", h.DeletePin)
			})

			// Attachments
			r.Get("/api/attachments/{id}", h.GetAttachmentByID)
			// /api/attachments/{id}/download is registered in the
			// outer Auth-only group above so it can be loaded as a
			// native <img>/<video> src without workspace headers
			// (MUL-3130). The handler self-resolves the workspace
			// from the attachment row.
			r.Get("/api/attachments/{id}/content", h.GetAttachmentContent)
			r.Delete("/api/attachments/{id}", h.DeleteAttachment)

			// Comments
			r.Route("/api/comments/{commentId}", func(r chi.Router) {
				r.Put("/", h.UpdateComment)
				r.Delete("/", h.DeleteComment)
				r.Post("/resolve", h.ResolveComment)
				r.Delete("/resolve", h.UnresolveComment)
				r.Post("/reactions", h.AddReaction)
				r.Delete("/reactions", h.RemoveReaction)
			})

			// Agents
			r.Route("/api/agents", func(r chi.Router) {
				r.Get("/", h.ListAgents)
				r.Get("/fleet-rankings", h.GetAgentFleetRankings)
				r.Get("/fleet-rank/rules", h.GetAgentFleetRankRules)
				r.Get("/honor/rules", h.GetAgentHonorRules)
				r.Put("/honor/rules", h.PutAgentHonorRules)
				r.Get("/honor/audit", h.GetAgentHonorAdminAudit)
				r.Post("/", h.CreateAgent)
				r.Post("/windy", h.EnsureWindy)
				r.Post("/drafts", h.CreateAgentDraft)
				r.Get("/drafts/{draftId}", h.GetAgentDraft)
				// Agent templates: pre-configured instructions + skill refs.
				// Picking a template imports the referenced skills into the
				// workspace (find-or-create by name) and creates the agent
				// with the template's instructions in one transaction.
				r.Post("/from-template", h.CreateAgentFromTemplate)
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", h.GetAgent)
					r.Get("/fleet-rank", h.GetAgentFleetRank)
					r.Get("/honor", h.GetAgentHonor)
					r.Patch("/honor", h.PatchAgentHonorShowcase)
					r.Post("/honor/grants", h.PostAgentHonorGrant)
					r.Delete("/honor/achievements/{achievementId}", h.DeleteAgentHonorAchievement)
					r.Put("/", h.UpdateAgent)
					r.Post("/archive", h.ArchiveAgent)
					r.Post("/restore", h.RestoreAgent)
					r.Post("/cancel-tasks", h.CancelAgentTasks)
					r.Get("/health", h.GetAgentHealth)
					r.Get("/lifecycle", h.GetAgentLifecycle)
					r.Post("/lifecycle", h.CreateAgentLifecycleOperation)
					r.Get("/lifecycle/{operationId}", h.GetAgentLifecycleOperation)
					r.Get("/activity", h.ListAgentActivity)
					r.Get("/activity/events", h.ListAgentActivityEvents)
					r.Get("/activity/{activityId}", h.GetAgentActivity)
					r.Get("/activity/{activityId}/steps", h.ListAgentActivitySteps)
					r.Get("/activity/{activityId}/diagnostic", h.GetAgentActivityDiagnostic)
					r.Get("/tasks", h.ListAgentTasks)
					r.Get("/reminders", h.ListAgentReminders)
					r.Get("/skills", h.ListAgentSkills)
					r.Put("/skills", h.SetAgentSkills)
					r.Get("/skill-suggestions", h.ListAgentSkillSuggestions)
					r.Post("/skill-suggestions/{suggestionId}/decision", h.DecideAgentSkillSuggestion)
					r.Get("/memories", h.ListAgentMemories)
					r.Get("/memory-curation/status", h.GetAgentMemoryCurationStatus)
					r.Get("/files", h.ListAgentFiles)
					r.Get("/files/content", h.GetAgentFileContent)
					r.Put("/files/content", h.UpdateAgentFileContent)
					r.Post("/skills/add", h.AddAgentSkills)
					// Dedicated env-management endpoint. Owner/admin only;
					// agent actors are denied. Every reveal / write is
					// audited to activity_log. See MUL-2600 and
					// internal/handler/agent_env.go.
					r.Get("/env", h.GetAgentEnv)
					r.Put("/env", h.UpdateAgentEnv)
					r.Post("/credentials", h.CreateAgentCredential)
				})
			})

			// Agent templates catalog (browse + detail). The Create flow
			// lives under /api/agents/from-template above; this route is for
			// the picker UI to list available templates.
			r.Route("/api/agent-templates", func(r chi.Router) {
				r.Get("/", h.ListAgentTemplates)
				r.Get("/{slug}", h.GetAgentTemplate)
			})

			// Skills
			r.Route("/api/skills", func(r chi.Router) {
				r.Get("/", h.ListSkills)
				r.Post("/", h.CreateSkill)
				r.Get("/search", h.SearchSkills)
				r.Get("/platform", h.ListPlatformSkills)
				r.Post("/platform/{name}/install", h.InstallPlatformSkill)
				r.Post("/import", h.ImportSkill)
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", h.GetSkill)
					r.Put("/", h.UpdateSkill)
					r.Delete("/", h.DeleteSkill)
					r.Post("/promote", h.PromoteSkill)
					r.Get("/promotions", h.ListSkillPromotions)
					r.Get("/files", h.ListSkillFiles)
					r.Put("/files", h.UpsertSkillFile)
					r.Delete("/files/{fileId}", h.DeleteSkillFile)
				})
			})

			// Dashboard — workspace-wide token + run-time rollups for the
			// "/{slug}/dashboard" page. Optional ?project_id filter scopes
			// the rollup to a single project.
			r.Route("/api/dashboard", func(r chi.Router) {
				r.Get("/usage/daily", h.GetDashboardUsageDaily)
				r.Get("/usage/by-agent", h.GetDashboardUsageByAgent)
				r.Get("/agent-runtime", h.GetDashboardAgentRunTime)
				r.Get("/runtime/daily", h.GetDashboardRunTimeDaily)
			})

			// Runtimes
			r.Route("/api/runtimes", func(r chi.Router) {
				r.Get("/", h.ListAgentRuntimes)
				// Computer / host one-click delete (LRM-438). Must be
				// registered before /{runtimeId} so "by-daemon" is not
				// captured as a runtime UUID.
				r.Delete("/by-daemon/{daemonId}", h.DeleteRuntimesByDaemon)
				r.Post("/by-daemon/{daemonId}/remove-agents", h.RemoveAgentsByDaemon)
				r.Route("/{runtimeId}", func(r chi.Router) {
					r.Patch("/", h.UpdateAgentRuntime)
					r.Get("/usage", h.GetRuntimeUsage)
					r.Get("/usage/by-agent", h.GetRuntimeUsageByAgent)
					r.Get("/usage/by-hour", h.GetRuntimeUsageByHour)
					r.Get("/activity", h.GetRuntimeTaskActivity)
					r.Get("/agent-workspaces", h.ListRuntimeAgentWorkspaces)
					r.Delete("/agent-workspaces/{dirName}", h.DeleteRuntimeAgentWorkspace)
					// Installed clients still use these runtime-scoped paths. They
					// delegate to the daemon-scoped Machine Upgrade record and must
					// never recreate a runtime-owned update lineage.
					r.Post("/update", h.InitiateUpdate)
					r.Get("/update/{updateId}", h.GetUpdate)
					r.Delete("/update-intent", h.CancelUpdateIntent)
					r.Post("/restart", h.InitiateRestart)
					r.Get("/restart/{restartId}", h.GetRestart)
					r.Post("/models", h.InitiateListModels)
					r.Get("/models/{requestId}", h.GetModelListRequest)
					r.Post("/local-skills", h.InitiateListLocalSkills)
					r.Get("/local-skills/{requestId}", h.GetLocalSkillListRequest)
					r.Post("/local-skills/import", h.InitiateImportLocalSkill)
					r.Get("/local-skills/import/{requestId}", h.GetLocalSkillImportRequest)
					r.Delete("/", h.DeleteAgentRuntime)
					// Cascade variant of DELETE: archive every active agent
					// bound to this runtime, cancel their tasks, then delete
					// the runtime — all in one transaction. Used by the
					// DeleteRuntimeDialog when the strict DELETE refused with
					// `runtime_has_active_agents` and the user confirmed the
					// cascade plan.
					r.Post("/archive-agents-and-delete", h.ArchiveAgentsAndDeleteRuntime)
				})
			})

			// Canonical daemon-identity Machine Upgrade lifecycle. Runtime-scoped
			// update routes remain compatibility adapters for installed clients.
			r.Route("/api/daemons/{daemonId}/upgrades", func(r chi.Router) {
				r.Post("/", h.CreateMachineUpgrade)
				r.Get("/{upgradeId}", h.GetMachineUpgrade)
				r.Delete("/{upgradeId}", h.CancelMachineUpgrade)
			})

			// Cloud Runtime fleet proxy. The remote service URL is configured
			// on SaaS API nodes only; self-hosted deployments return 503.
			r.Route("/api/cloud-runtime", func(r chi.Router) {
				r.Get("/", h.GetCloudRuntimeService)
				r.Get("/healthz", h.GetCloudRuntimeHealth)
				r.Get("/readyz", h.GetCloudRuntimeReady)
				r.Get("/nodes", h.ListCloudRuntimeNodes)
				r.Post("/nodes", h.CreateCloudRuntimeNode)
				r.Delete("/nodes", h.DeleteCloudRuntimeNode)
				r.Post("/nodes/start", h.StartCloudRuntimeNode)
				r.Post("/nodes/stop", h.StopCloudRuntimeNode)
				r.Post("/nodes/reboot", h.RebootCloudRuntimeNode)
				r.Post("/nodes/status", h.GetCloudRuntimeNodeStatus)
				r.Post("/nodes/exec", h.ExecCloudRuntimeNode)
				r.Post("/sandboxes/{sandboxID}/snapshot", h.SnapshotCloudRuntimeSandbox)
				r.Post("/sandboxes/fork", h.ForkCloudRuntimeSandbox)
			})

			// Unified env-dispatch API (spec §6). Replaces the SWE-Lego-only
			// route group with the four endpoints backing the env-state model.
			r.Post("/api/v1/env", h.CreateEnv)
			r.Delete("/api/v1/env/{envID}", h.DeleteEnv)
			r.Post("/api/v1/source-tasks", h.CreateSourceTask)
			r.Get("/api/v1/source-tasks/{sourceTaskID}", h.GetSourceTask)
			// Manual SWE-Lego task-template warm-up: build the materialized Cube
			// template ahead of the first scratch swe_lego dispatch.
			r.Post("/api/v1/source-tasks/{sourceTaskID}/materialize", h.MaterializeSourceTaskTemplate)
			r.Post("/api/v1/env-dispatch", h.EnvDispatch)
			r.Delete("/api/v1/env-dispatch/{projectID}", h.DeleteEnvDispatchProject)
			r.Get("/api/v1/env-dispatch/{projectID}/dag", h.GetDag)
			r.Post("/api/v1/env-dispatch/{projectID}/diagnosis", h.DiagnoseEnvDispatchProject)
			// Human-facing latest-run poll for sandbox-mode diagnosis
			// (spec 005); lets operators/AReaL track runs without the
			// per-run capability token.
			r.Get("/api/v1/env-dispatch/{projectID}/diagnosis/latest", h.GetLatestEnvDispatchDiagnosis)
			// Channel-first facades for dispatch_type=message: resolve the bound
			// project internally. Project-first routes above remain available.
			r.Get("/api/v1/env-dispatch/channels/{channelID}/dag", h.GetEnvDispatchChannelDag)
			r.Post("/api/v1/env-dispatch/channels/{channelID}/diagnosis", h.DiagnoseEnvDispatchChannel)
			r.Get("/api/v1/env-dispatch/channels/{channelID}/diagnosis/latest", h.GetLatestEnvDispatchChannelDiagnosis)
			r.Delete("/api/v1/env-dispatch/channels/{channelID}", h.DeleteEnvDispatchChannel)
			r.Get("/api/v1/channels/{channelID}/env-checkpoints", h.ListChannelEnvCheckpoints)

			// Env-checkpoint APIs. Gated by ENV_CHECKPOINTS_ENABLED; handlers
			// return 404 when disabled so AReaL clients can detect the gate.
			r.Post("/api/v1/env-checkpoints", h.CreateEnvCheckpoint)
			r.Get("/api/v1/env-checkpoints/{checkpointID}", h.GetEnvCheckpoint)
			r.Post("/api/v1/env-checkpoints/{checkpointID}/resume", h.ResumeEnvCheckpoint)
			r.Get("/api/v1/projects/{projectID}/env-checkpoints", h.ListEnvCheckpoints)

			// Tasks (user-facing, with ownership check)
			r.Post("/api/tasks/{taskId}/cancel", h.CancelTaskByUser)

			// Workspace-wide agent task snapshot for presence derivation:
			// every active task + each agent's most recent terminal task.
			r.Get("/api/agent-task-snapshot", h.ListWorkspaceAgentTaskSnapshot)
			r.Get("/api/agent-tasks", h.ListAgentTaskFeed)
			r.Get("/api/agent-task-stats", h.GetAgentTaskStats)

			// Workspace-wide daily agent activity (last 30d, anchored on
			// completed_at). Backs the Agents-list sparkline (trailing 7d
			// slice) AND the agent detail "Last 30 days" panel.
			r.Get("/api/agent-activity-30d", h.GetWorkspaceAgentActivity30d)
			r.Get("/api/work-graphs/{graphId}", h.GetWorkGraph)

			// Workspace-wide 30-day run counts per agent for the Agents-list RUNS column.
			r.Get("/api/agent-run-counts", h.GetWorkspaceAgentRunCounts)

			r.Route("/api/chat/sessions", func(r chi.Router) {
				r.Post("/", h.CreateChatSession)
				r.Get("/", h.ListChatSessions)
				r.Route("/{sessionId}", func(r chi.Router) {
					r.Get("/", h.GetChatSession)
					r.Patch("/", h.UpdateChatSession)
					r.Delete("/", h.DeleteChatSession)
					r.Post("/messages", h.SendChatMessage)
					r.Get("/messages", h.ListChatMessages)
					r.Get("/messages/page", h.ListChatMessagesPage)
					r.Get("/pending-task", h.GetPendingChatTask)
					r.Post("/agent-inbox/events/{eventId}/cancel", h.CancelChatAgentInboxEvent)
					r.Get("/agent-inbox-events/{eventId}/timeline", h.ListChatAgentInboxEventTimeline)
					r.Post("/read", h.MarkChatSessionRead)
				})
			})
			r.Get("/api/chat/pending-tasks", h.ListPendingChatTasks)
			// Agent task-token chat transport. These routes intentionally live
			// on the regular Auth API so task tokens use the same workspace and
			// permission chain as other channel operations.
			// Agent data-plane API (#801). RequireAgentPrincipal is enforced in
			// handlers via context; routes are agent-only contracts.
			r.Route("/api/agent", func(r chi.Router) {
				r.Use(middleware.RequireAgentPrincipal)
				r.Get("/channels", h.ListAgentChannels)
				r.Post("/channels", h.CreateAgentCoordinationChannel)
				r.Post("/channels/{channelId}/archive", h.ArchiveAgentCoordinationChannel)
				r.Get("/channels/{channelId}/members", h.ListAgentChannelMembers)
				r.Get("/channels/{channelId}/goal", h.GetAgentChannelGoal)
				r.Post("/channels/{channelId}/goal", h.CreateAgentChannelGoal)
				r.Patch("/channels/{channelId}/goal", h.UpdateAgentChannelGoal)
				r.Post("/channels/{channelId}/goal/checkpoint", h.CheckpointAgentChannelGoal)
				r.Get("/channels/{channelId}/goal/subgoals", h.ListAgentChannelGoalSubgoals)
				r.Post("/channels/{channelId}/goal/subgoals", h.CreateAgentChannelGoalSubgoal)
				r.Post("/work-graphs", h.CreateAgentWorkGraph)
				r.Get("/work-graphs/{graphId}", h.GetAgentWorkGraph)
				r.Post("/work-graphs/{graphId}/reconcile", h.ReconcileAgentWorkGraph)
				r.Post("/work-graphs/{graphId}/nodes/{nodeId}/invalidate", h.InvalidateAgentWorkGraphNode)
				r.Patch("/work-graphs/{graphId}/nodes/{nodeId}", h.UpdateAgentWorkGraphNode)
				r.Post("/work-graphs/{graphId}/artifacts", h.AddAgentWorkGraphArtifact)
				r.Post("/work-graphs/{graphId}/verifications", h.AddAgentWorkGraphVerification)
				r.Post("/work-graphs/{graphId}/revisions", h.ReviseAgentWorkGraph)
				r.Get("/channels/{channelId}/goal/process", h.ListAgentChannelGoalProcesses)
				r.Put("/channels/{channelId}/goal/process", h.PutAgentChannelGoalProcess)
				r.Get("/channels/{channelId}/goal/process/{agentId}", h.GetAgentChannelGoalProcess)
				r.Put("/channels/{channelId}/goal/process/{agentId}", h.PutAgentChannelGoalProcess)
				r.Get("/channels/{channelId}/member-management-capabilities", h.GetAgentChannelMemberManagementCapabilities)
				r.Post("/channels/{channelId}/members", h.AddAgentChannelMember)
				r.Post("/channels/{channelId}/members/batch", h.AddAgentChannelMembers)
				r.Delete("/channels/{channelId}/members/{memberType}/{memberId}", h.RemoveAgentChannelMember)
				r.Put("/channels/{channelId}/mute", h.MuteAgentChannel)
				r.Delete("/channels/{channelId}/mute", h.UnmuteAgentChannel)
				r.Get("/attachments/{id}", h.GetAgentAttachment)
				r.Get("/attachments/{id}/download", h.DownloadAgentAttachment)
				r.Get("/attachments/{id}/content", h.GetAgentAttachmentContent)
				r.Get("/attachment-upload-capabilities", h.AgentAttachmentUploadCapabilities)
				r.Post("/attachment-upload-sessions", h.CreateAgentAttachmentUploadSession)
				r.Get("/attachment-upload-sessions/{sessionId}", h.GetAgentAttachmentUploadSessionStatus)
				r.Put("/attachment-upload-sessions/{sessionId}/object", h.UploadAgentAttachmentSessionObject)
				r.Post("/attachment-upload-sessions/{sessionId}/retry", h.RetryAgentAttachmentUploadSession)
				r.Post("/attachment-upload-sessions/{sessionId}/cancel", h.CancelAgentAttachmentUploadSession)
				r.Post("/attachment-upload-sessions/{sessionId}/complete", h.CompleteAgentAttachmentUploadSession)
				r.Get("/channels/{channelId}/attachments", h.ListAgentChannelAttachments)
				// Issues / projects / workspace / squad (necessary batch)
				r.Get("/issues", h.ListAgentIssues)
				r.Get("/issues/search", h.SearchAgentIssues)
				r.Get("/issues/{id}", h.GetAgentIssue)
				r.Post("/issues", h.CreateAgentIssue)
				r.Put("/issues/{id}", h.UpdateAgentIssue)
				r.Get("/issues/{id}/comments", h.ListAgentIssueComments)
				r.Post("/issues/{id}/comments", h.CreateAgentIssueComment)
				r.Get("/issues/{id}/metadata", h.ListAgentIssueMetadata)
				r.Put("/issues/{id}/metadata/{key}", h.SetAgentIssueMetadataKey)
				r.Delete("/issues/{id}/metadata/{key}", h.DeleteAgentIssueMetadataKey)
				r.Get("/issues/{id}/labels", h.ListAgentIssueLabels)
				r.Post("/issues/{id}/labels", h.AttachAgentIssueLabel)
				r.Delete("/issues/{id}/labels/{labelId}", h.DetachAgentIssueLabel)
				r.Get("/issues/{id}/subscribers", h.ListAgentIssueSubscribers)
				r.Post("/issues/{id}/subscribe", h.SubscribeAgentToIssue)
				r.Post("/issues/{id}/unsubscribe", h.UnsubscribeAgentFromIssue)
				r.Get("/issues/{id}/task-runs", h.ListAgentIssueTaskRuns)
				r.Get("/issues/{id}/pull-requests", h.ListAgentIssuePullRequests)
				r.Get("/issues/{id}/attachments", h.ListAgentIssueAttachments)
				r.Post("/issues/{id}/rerun", h.RerunAgentIssue)
				r.Put("/issues/{id}/channel", h.SetAgentIssueSourceChannel)
				r.Get("/tasks/{taskId}/messages", h.ListAgentTaskMessages)
				r.Post("/tasks/{taskId}/cancel", h.CancelAgentTask)
				r.Get("/projects/{id}/resources", h.ListAgentProjectResources)
				r.Get("/workspace", h.GetAgentWorkspace)
				r.Get("/workspaces/{id}", h.GetAgentWorkspaceByID)
				r.Get("/agents", h.ListAgentDirectoryAgents)
				// Squad retired (Frank 2026-07-28): no /api/agent/squads*.
				// Research Fleet (LRM-904 / #801): mat_* must not hit /api/research/*.
				r.Route("/research", func(r chi.Router) {
					r.Get("/fleet", h.GetAgentResearchFleet)
					r.Post("/fleet/members", h.HireAgentResearchFleetMember)
					r.Post("/fleet/members/{memberId}/optimize", h.OptimizeAgentResearchFleetMember)
					r.Post("/fleet/members/{memberId}/archive", h.ArchiveAgentResearchFleetMember)
					r.Route("/sessions/{id}", func(r chi.Router) {
						r.Get("/", h.GetAgentResearchSessionSnapshot)
						r.Post("/messages", h.PostAgentResearchMessage)
						r.Put("/messages/{messageId}/match-decision", h.PutAgentResearchMessageMatchDecision)
						r.Post("/graph/nodes", h.AppendAgentResearchGraphNode)
						r.Post("/nodes/{nodeId}/commands", h.PostAgentResearchNodeCommand)
						r.Post("/sources", h.UpsertAgentResearchSource)
						r.Post("/report", h.PatchAgentResearchReport)
						r.Post("/presence", h.PostAgentResearchPresence)
						r.Post("/stage-eval", h.RequestAgentResearchStageEval)
						r.Post("/tasks/{taskId}/attempts/{attemptId}/result", h.SubmitAgentResearchTaskResult)
					})
				})
			})
			r.Post("/api/agent/messages/send", h.AgentTransportSendMessage)
			r.Post("/api/agent/messages/target", h.AgentTransportResolveMessageTarget)
			r.Post("/api/agent/messages/react", h.AgentTransportReactMessage)
			r.Post("/api/agent/messages/read", h.AgentTransportReadMessages)
			r.Post("/api/agent/messages/search", h.AgentTransportSearchMessages)
			r.Post("/api/agent/messages/resolve", h.AgentTransportResolveMessage)
			r.Post("/api/agent/threads/unfollow", h.AgentTransportUnfollowThread)
			r.Post("/api/agent/reminders/schedule", h.AgentTransportScheduleReminder)
			r.Post("/api/agent/reminders/list", h.AgentTransportListReminders)
			r.Post("/api/agent/reminders/snooze", h.AgentTransportSnoozeReminder)
			r.Post("/api/agent/reminders/update", h.AgentTransportUpdateReminder)
			r.Post("/api/agent/reminders/cancel", h.AgentTransportCancelReminder)
			r.Post("/api/agent/reminders/log", h.AgentTransportReminderLog)
			r.Post("/api/agent/actions/prepare", h.AgentTransportPrepareAction)

			// Unified Messages read model. Group-channel and DM mutations/details
			// intentionally remain on their domain-specific routes below.
			r.Get("/api/conversations", h.ListConversations)

			// Unified 1-on-1 DM list (kind='dm' channels ∪ legacy unbound chat
			// sessions) plus idempotent create-or-find. Sole data source for the
			// DM section; group channels stay on /api/channels.
			r.Get("/api/dm", h.ListDirectMessages)
			r.Post("/api/dm", h.CreateOrFindDirectMessage)
			r.Put("/api/dm/channels/{channelId}/pin", h.PinDMChannel)
			r.Delete("/api/dm/channels/{channelId}/pin", h.UnpinDMChannel)
			r.Put("/api/dm/channels/{channelId}/mute", h.MuteDMChannel)
			r.Delete("/api/dm/channels/{channelId}/mute", h.UnmuteDMChannel)
			r.Post("/api/dm/channels/{channelId}/unread", h.MarkDMChannelUnread)
			r.Delete("/api/dm/channels/{channelId}", h.CloseDMChannel)
			r.Put("/api/dm/sessions/{sessionId}/pin", h.PinDMSession)
			r.Delete("/api/dm/sessions/{sessionId}/pin", h.UnpinDMSession)
			r.Put("/api/dm/sessions/{sessionId}/mute", h.MuteDMSession)
			r.Delete("/api/dm/sessions/{sessionId}/mute", h.UnmuteDMSession)
			r.Post("/api/dm/sessions/{sessionId}/unread", h.MarkDMSessionUnread)
			r.Delete("/api/dm/sessions/{sessionId}", h.CloseDMSession)

			r.Route("/api/channels", func(r chi.Router) {
				r.Get("/", h.ListChannels)
				r.Post("/", h.CreateChannel)
				r.Post("/lark/messages", h.ImportLarkChannelMessage)
				r.Route("/{channelId}", func(r chi.Router) {
					r.Patch("/", h.UpdateChannel)
					r.Delete("/", h.DeleteChannel)
					r.Post("/archive", h.ArchiveChannel)
					r.Post("/restore", h.RestoreChannel)
					r.Put("/pin", h.PinChannel)
					r.Delete("/pin", h.UnpinChannel)
					r.Put("/mute", h.MuteChannel)
					r.Delete("/mute", h.UnmuteChannel)
					r.Put("/notify-preference", h.SetChannelNotifyPreference)
					r.Put("/agent-mute", h.MuteChannelAgent)
					r.Delete("/agent-mute", h.UnmuteChannelAgent)
					r.Post("/unread", h.MarkChannelUnread)
					r.Get("/project", h.GetChannelProject)
					r.Put("/project", h.SetChannelProject)
					r.Get("/project-files", h.ListChannelProjectFiles)
					r.Get("/project-files/content", h.GetChannelProjectFile)
					r.Get("/active-tasks", h.ListChannelActiveTasks)
					r.Get("/goal", h.GetChannelGoal)
					r.Post("/goal", h.CreateChannelGoal)
					r.Patch("/goal", h.UpdateChannelGoal)
					r.Get("/goal/subgoals", h.ListChannelGoalSubgoals)
					r.Post("/goal/subgoals", h.CreateChannelGoalSubgoal)
					r.Post("/goal/subgoals/batch", h.BatchCreateChannelGoalSubgoals)
					r.Patch("/goal/subgoals/{subgoalId}", h.UpdateChannelGoalSubgoal)
					r.Post("/goal/subgoals/{subgoalId}/resolve", h.ResolveChannelGoalSubgoal)
					r.Post("/goal/subgoals/{subgoalId}/waiting-on/clear", h.ClearChannelGoalSubgoalWaitingOn)
					r.Get("/goal/process", h.ListChannelGoalProcesses)
					r.Get("/goal/process/{agentId}", h.GetChannelGoalProcess)
					r.Put("/goal/process/{agentId}", h.PutChannelGoalProcess)
					r.Get("/issues", h.ListChannelSourceIssues)
					r.Get("/members", h.ListChannelMembers)
					r.Get("/member-management-capabilities", h.GetChannelMemberManagementCapabilities)
					r.Get("/invite-candidates", h.ListChannelInviteCandidates)
					r.Post("/members", h.AddChannelMember)
					r.Post("/members/batch", h.AddChannelMembers)
					r.Delete("/members/{memberType}/{memberId}", h.RemoveChannelMember)
					r.Patch("/members/{memberType}/{memberId}", h.UpdateChannelMemberRole)
					r.Post("/members/{memberType}/{memberId}/transfer-ownership", h.TransferChannelOwnership)
					r.Post("/collaboration-sessions", h.CreateCollaborationSession)
					r.Get("/messages", h.ListChannelMessages)
					r.Get("/messages/search", h.SearchChannelMessages)
					r.Post("/messages", h.SendChannelMessage)
					// LRM-425: channel Stop uses inbox_event_id (active-tasks), not /api/tasks/{id}/cancel.
					r.Post("/agent-inbox/events/{eventId}/cancel", h.CancelChannelAgentInboxEvent)
					r.Post("/agent-inbox/cancel-active", h.CancelChannelActiveAgentInboxEvents)
					r.Post("/agent-inbox/events/{eventId}/retry", h.RetryChannelAgentInboxEvent)
					r.Patch("/messages/{messageId}", h.UpdateChannelMessage)
					r.Delete("/messages/{messageId}", h.DeleteChannelMessage)
					r.Get("/messages/{messageId}/thread", h.ListChannelMessageThread)
					r.Post("/messages/{messageId}/reactions", h.AddChannelMessageReaction)
					r.Delete("/messages/{messageId}/reactions", h.RemoveChannelMessageReaction)
					r.Post("/messages/{messageId}/choice", h.ChooseChannelMessageOption)
					r.Post("/messages/{messageId}/thread", h.SendChannelMessageThreadReply)
					r.Post("/messages/{messageId}/thread/read", h.MarkChannelThreadRead)
					r.Put("/messages/{messageId}/thread/follow", h.FollowChannelThread)
					r.Delete("/messages/{messageId}/thread/follow", h.UnfollowChannelThread)
					r.Get("/attachments", h.ListChannelAttachments)
					r.Get("/stats", h.GetChannelStats)
					r.Post("/read", h.MarkChannelRead)
					r.Post("/typing", h.SetChannelTyping)
				})
			})

			// Activity (member feed: related threads + inbox)
			r.Route("/api/activity", func(r chi.Router) {
				r.Get("/", h.ListUserActivity)
				r.Post("/mark-all-read", h.MarkAllUserActivityRead)
			})

			// Inbox
			r.Route("/api/inbox", func(r chi.Router) {
				r.Get("/", h.ListInbox)
				r.Get("/unread-count", h.CountUnreadInbox)
				r.Post("/mark-all-read", h.MarkAllInboxRead)
				r.Post("/archive-all", h.ArchiveAllInbox)
				r.Post("/archive-all-read", h.ArchiveAllReadInbox)
				r.Post("/archive-completed", h.ArchiveCompletedInbox)
				r.Post("/{id}/read", h.MarkInboxRead)
				r.Post("/{id}/archive", h.ArchiveInboxItem)
			})

			// Notification preferences
			r.Route("/api/notification-preferences", func(r chi.Router) {
				r.Get("/", h.GetNotificationPreferences)
				r.Put("/", h.UpdateNotificationPreferences)
			})

			// Web Push device binding. POST is workspace-scoped so the subscription
			// captures the current workspace while the route still enforces membership.
			r.Post("/api/web-push/subscriptions", h.UpsertWebPushSubscription)
			// LRM-755: settings self-test — real VAPID push to the caller's devices.
			r.Post("/api/web-push/test", h.SendTestWebPush)
		})
	})

	return r, h
}

// buildLarkConnectorFactory wires the real WS long-conn connector
// that talks to /callback/ws/endpoint directly with app_id/app_secret.
// The connector wraps every read with a ctx-cancel watchdog so lease
// loss / shutdown breaks the blocking ReadMessage in bounded time —
// the invariant §4.4 leans on.
//
// If the endpoint fetcher fails to initialize (typically a malformed
// MULTICA_LARK_CALLBACK_BASE_URL), we log and fall back to the
// NoopConnector so the lease / supervisor lifecycle still exercises
// against real DB rows. Inbound messages are silently dropped until
// the config is fixed; the boot log labels the mode "noop" so the
// degraded state is visible.
//
// Returns the factory plus a short label for the boot log: "ws" in
// the healthy case, "noop" in the fallback case.
func buildLarkConnectorFactory(installSvc *lark.InstallationService, apiClient lark.APIClient) (lark.ConnectorFactory, string) {
	endpointFetcher, err := lark.NewHTTPConnectionTokenFetcher(lark.HTTPConnectionTokenConfig{
		BaseURL: strings.TrimSpace(os.Getenv("MULTICA_LARK_CALLBACK_BASE_URL")),
		Logger:  slog.Default(),
	})
	if err != nil {
		slog.Error("lark ws: endpoint fetcher init failed; falling back to noop", "error", err)
		return lark.NoopConnectorFactory(slog.Default()), "noop"
	}
	decoder := lark.NewLarkJSONFrameDecoder()
	dialer := lark.NewGorillaDialer()
	credsProvider := lark.CredentialsProviderFunc(func(ctx context.Context, inst db.LarkInstallation) (lark.InstallationCredentials, error) {
		secret, err := installSvc.DecryptAppSecret(inst)
		if err != nil {
			return lark.InstallationCredentials{}, err
		}
		creds := lark.InstallationCredentials{
			AppID:     inst.AppID,
			AppSecret: secret,
			Region:    lark.RegionOrDefault(inst.Region),
		}
		if inst.TenantKey.Valid {
			creds.TenantKey = inst.TenantKey.String
		}
		return creds, nil
	})
	// Inbound enricher: expands quoted replies / forwarded bundles AND
	// prefetches a window of surrounding group history (MUL-3084) into the
	// agent's body via the IM API before dispatch. It shares the
	// connector's resolved credentials and runs under the connector's
	// EnrichTimeout so it cannot overrun the Lark long-conn ACK budget.
	enricher := lark.NewInboundEnricher(apiClient, lark.InboundEnricherConfig{
		RecentContextSize: lark.DefaultRecentContextSize,
		Logger:            slog.Default(),
	})
	conn, err := lark.NewWSLongConnConnector(lark.WSConnectorConfig{
		Dialer:              dialer,
		EndpointFetcher:     endpointFetcher,
		FrameDecoder:        decoder,
		Enricher:            enricher,
		CredentialsProvider: credsProvider,
		Logger:              slog.Default(),
	})
	if err != nil {
		slog.Error("lark ws: connector init failed; falling back to noop", "error", err)
		return lark.NoopConnectorFactory(slog.Default()), "noop"
	}
	return func(_ db.LarkInstallation) (lark.EventConnector, error) {
		return conn, nil
	}, "ws-long-conn"
}

// membershipChecker implements realtime.MembershipChecker using database queries.
type membershipChecker struct {
	queries *db.Queries
}

func (mc *membershipChecker) IsMember(ctx context.Context, userID, workspaceID string) bool {
	_, err := mc.queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      parseUUID(userID),
		WorkspaceID: parseUUID(workspaceID),
	})
	return err == nil
}

// patResolver implements realtime.PATResolver using database queries.
// patCache is shared with the Auth and DaemonAuth middlewares so a token
// revoke through any path invalidates the cache for all of them. Nil
// cache is supported and degrades to direct DB lookups.
type patResolver struct {
	queries *db.Queries
	cache   *auth.PATCache
}

func (pr *patResolver) ResolveToken(ctx context.Context, token string) (string, bool) {
	hash := auth.HashToken(token)

	if userID, ok := pr.cache.Get(ctx, hash); ok {
		return userID, true
	}

	pat, err := pr.queries.GetPersonalAccessTokenByHash(ctx, hash)
	if err != nil {
		return "", false
	}

	userID := util.UUIDToString(pat.UserID)

	var expiresAt time.Time
	if pat.ExpiresAt.Valid {
		expiresAt = pat.ExpiresAt.Time
	}
	pr.cache.Set(ctx, hash, userID, auth.TTLForExpiry(time.Now(), expiresAt))

	// Cache miss = first WS auth in this TTL window. Refresh last_used_at;
	// subsequent connects within the window skip the write.
	go pr.queries.UpdatePersonalAccessTokenLastUsed(context.Background(), pat.ID)

	return userID, true
}

// parseUUID is a thin alias for util.MustParseUUID. Call sites here are all
// internal round-trips of DB-sourced UUIDs (e.g. issue.ID, e.ActorID), so an
// invalid value indicates a programming error and should panic loudly.
func parseUUID(s string) pgtype.UUID {
	return util.MustParseUUID(s)
}

// optionalUUID returns a NULL pgtype.UUID for an empty string and otherwise
// behaves like parseUUID. Use this for actor IDs on events where the producer
// may legitimately be a "system" actor with no member/agent attribution
// (e.g. GitHub webhook auto-status sync) — the activity_log and inbox_item
// tables both allow actor_id to be NULL.
func optionalUUID(s string) pgtype.UUID {
	if s == "" {
		return pgtype.UUID{}
	}
	return util.MustParseUUID(s)
}

func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	res := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			res = append(res, trimmed)
		}
	}
	return res
}

func cloudRuntimeFleetURLFromEnv() string {
	if url := strings.TrimSpace(os.Getenv("MULTICA_CLOUD_FLEET_URL")); url != "" {
		return url
	}
	return strings.TrimSpace(os.Getenv("MULTICA_FLEET_URL"))
}

// defaultSelfPlayTemplateFromEnv returns the sandbox template env-dispatch uses
// when auto-creating a workspace's default self_play base env. Defaults to
// "default" when MULTICA_DEFAULT_SELF_PLAY_TEMPLATE is unset/blank; a request
// may still override it per-dispatch.
func defaultSelfPlayTemplateFromEnv() string {
	if t := strings.TrimSpace(os.Getenv("MULTICA_DEFAULT_SELF_PLAY_TEMPLATE")); t != "" {
		return t
	}
	return "default"
}

// diagnosisRunLoaderAdapter adapts the diagnosis state store to the
// middleware.DiagnosisRunLoader surface (the middleware package cannot import
// internal/service without an import cycle).
type diagnosisRunLoaderAdapter struct {
	store *service.DiagnosisStateStore
}

func (a diagnosisRunLoaderAdapter) GetRun(ctx context.Context, runID string) (middleware.DiagnosisRun, error) {
	ckpt, err := a.store.GetRun(ctx, runID)
	if err != nil {
		if errors.Is(err, service.ErrDiagnosisRunNotFound) {
			return middleware.DiagnosisRun{}, middleware.ErrDiagnosisRunNotFound
		}
		return middleware.DiagnosisRun{}, err
	}
	return middleware.DiagnosisRun{
		RunID:               ckpt.RunID,
		ProjectID:           ckpt.ProjectID,
		TaskID:              ckpt.TaskID,
		TopologyHash:        ckpt.TopologyHash,
		OrderedSegmentIDs:   ckpt.OrderedSegmentIDs,
		Status:              string(ckpt.Status),
		CapabilityTokenHash: ckpt.CapabilityTokenHash,
		ExecutionMode:       ckpt.ExecutionMode,
		SandboxInstanceID:   ckpt.SandboxInstanceID,
	}, nil
}
