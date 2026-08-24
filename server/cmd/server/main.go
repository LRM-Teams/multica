package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/arealrl"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/memorygraph"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/internal/scheduler"
	"github.com/multica-ai/multica/server/internal/service"
	agentpkg "github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/redis/go-redis/v9"
)

var (
	version = "dev"
	commit  = "unknown"
)

// graphMemoryDiveModelBackend keeps the process Explore-model default for
// empty Dive overrides while allowing an approved Dive model to win per run.
type graphMemoryDiveModelBackend struct {
	memorygraph.AgentBackend
	defaultModel string
}

func (b graphMemoryDiveModelBackend) Execute(ctx context.Context, prompt string, opts agentpkg.ExecOptions) (*agentpkg.Session, error) {
	if opts.Model == "" {
		opts.Model = b.defaultModel
	}
	return b.AgentBackend.Execute(ctx, prompt, opts)
}

func newNamedRedisClient(base *redis.Options, suffix string) *redis.Client {
	opts := *base
	opts.ClientName = redisClientName(opts.ClientName, suffix)
	return redis.NewClient(&opts)
}

func redisClientName(existing, suffix string) string {
	if suffix == "" {
		return existing
	}
	if existing != "" {
		return existing + ":" + suffix
	}
	return "multica-api:" + suffix
}

func closeRedisClient(label string, client *redis.Client) {
	if client == nil {
		return
	}
	if err := client.Close(); err != nil {
		slog.Warn("redis client close failed", "client", label, "error", err)
	}
}

func shardedRelayConfigFromEnv() realtime.ShardedStreamRelayConfig {
	cfg := realtime.DefaultShardedStreamRelayConfig()
	cfg.Shards = envPositiveInt("REALTIME_RELAY_SHARDS", cfg.Shards)
	cfg.StreamMaxLen = envPositiveInt64("REALTIME_RELAY_STREAM_MAXLEN", cfg.StreamMaxLen)
	cfg.ReadCount = envPositiveInt64("REALTIME_RELAY_XREAD_COUNT", cfg.ReadCount)
	cfg.ReadBlock = envDuration("REALTIME_RELAY_XREAD_BLOCK", cfg.ReadBlock)
	return cfg
}

func realtimeRelayModeFromEnv() string {
	const defaultMode = "sharded"
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("REALTIME_RELAY_MODE")))
	if raw == "" {
		return defaultMode
	}
	switch raw {
	case "sharded", "dual", "legacy":
		return raw
	default:
		slog.Warn("invalid env var, using default", "name", "REALTIME_RELAY_MODE", "value", raw, "default", defaultMode)
		return defaultMode
	}
}

func envPositiveInt(name string, def int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		slog.Warn("invalid env var, using default", "name", name, "value", raw, "default", def, "error", err)
		return def
	}
	return v
}

func envPositiveInt64(name string, def int64) int64 {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		slog.Warn("invalid env var, using default", "name", name, "value", raw, "default", def, "error", err)
		return def
	}
	return v
}

func envDuration(name string, def time.Duration) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := time.ParseDuration(raw)
	if err != nil || v <= 0 {
		slog.Warn("invalid env var, using default", "name", name, "value", raw, "default", def.String(), "error", err)
		return def
	}
	return v
}

func main() {
	logger.Init()

	// Warn about missing configuration
	if os.Getenv("JWT_SECRET") == "" {
		slog.Warn("JWT_SECRET is not set — using insecure default. Set JWT_SECRET for production use.")
	}
	if os.Getenv("RESEND_API_KEY") == "" && strings.TrimSpace(os.Getenv("SMTP_HOST")) == "" {
		slog.Warn("no email backend configured (RESEND_API_KEY and SMTP_HOST both empty) — verification codes will be printed to the log instead of emailed.")
	}
	if strings.TrimSpace(os.Getenv("FRONTEND_ORIGIN")) == "" {
		slog.Warn("FRONTEND_ORIGIN is not set — invitation emails cannot be sent until it is configured.")
	}
	if os.Getenv("MULTICA_DEV_VERIFICATION_CODE") != "" {
		if strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production") {
			slog.Warn("MULTICA_DEV_VERIFICATION_CODE is set but ignored because APP_ENV=production.")
		} else {
			slog.Warn("MULTICA_DEV_VERIFICATION_CODE is enabled. Use it only for local development or private test instances.")
		}
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}

	// Connect to database
	ctx := context.Background()
	pool, err := newDBPool(ctx, dbURL)
	if err != nil {
		slog.Error("unable to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		slog.Error("unable to ping database", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to database")
	logPoolConfig(pool)

	bus := events.New()
	hub := realtime.NewHub()
	go hub.Run()
	daemonHub := daemonws.NewHub()
	// LRM-1571: online judgment is WS-authoritative for new daemons. The
	// handler's runtimeConnectivity read uses this presence source before
	// falling back to legacy heartbeat freshness.
	handler.SetRunnerPresence(daemonHub)
	var daemonWakeup service.TaskWakeupNotifier = daemonHub
	var reminderNotifier daemonws.ReminderNotifier = daemonHub
	var agentDeliveryNotifier daemonws.AgentDeliveryNotifier = daemonHub
	var agentRestartNotifier daemonws.AgentRestartNotifier = daemonHub

	// MUL-1138: when REDIS_URL is set, route fanout through a Redis relay so
	// multiple API nodes can deliver each other's events. Without it the hub
	// is the sole broadcaster and the server stays single-node (legacy).
	// Runtime local-skill stores and realtime relay traffic use separate Redis
	// clients so blocking stream consumers cannot starve request-path Redis
	// operations.
	relayCtx, relayCancel := context.WithCancel(context.Background())
	var broadcaster realtime.Broadcaster = hub
	var storeRedis *redis.Client
	var relayWriteRedis *redis.Client
	var relayReadRedis *redis.Client
	var shardedReadRedis *redis.Client
	var legacyReadRedis *redis.Client
	var relay realtime.ManagedRelay
	defer func() {
		if relay != nil {
			relay.Stop()
		}
		relayCancel()
		if relay != nil {
			relay.Wait()
		}
		closeRedisClient("realtime-read-legacy", legacyReadRedis)
		closeRedisClient("realtime-read-sharded", shardedReadRedis)
		closeRedisClient("realtime-read", relayReadRedis)
		closeRedisClient("realtime-write", relayWriteRedis)
		closeRedisClient("store", storeRedis)
	}()
	if redisURL := os.Getenv("REDIS_URL"); redisURL != "" {
		opts, err := redis.ParseURL(redisURL)
		if err != nil {
			slog.Error("invalid REDIS_URL — falling back to in-memory hub", "error", err)
		} else {
			storeRedis = newNamedRedisClient(opts, "store")
			relayWriteRedis = newNamedRedisClient(opts, "realtime-write")

			relayMode := realtimeRelayModeFromEnv()
			relayConfig := shardedRelayConfigFromEnv()
			switch relayMode {
			case "legacy":
				relayReadRedis = newNamedRedisClient(opts, "realtime-read")
				relay = realtime.NewRedisRelayWithClients(hub, relayWriteRedis, relayReadRedis)
				slog.Info("daemon websocket wakeup: Redis fanout disabled in legacy realtime relay mode")
			case "dual":
				shardedReadRedis = newNamedRedisClient(opts, "realtime-read-sharded")
				legacyReadRedis = newNamedRedisClient(opts, "realtime-read-legacy")
				sharded := realtime.NewShardedStreamRelay(hub, relayWriteRedis, shardedReadRedis, relayConfig)
				sharded.SetDaemonRuntimeDeliverer(daemonHub)
				legacy := realtime.NewRedisRelayWithClients(hub, relayWriteRedis, legacyReadRedis)
				relay = realtime.NewMirroredRelay(sharded, legacy)
				relayNotifier := daemonws.NewRelayNotifier(daemonHub, sharded)
				daemonWakeup = relayNotifier
				reminderNotifier = relayNotifier
				agentDeliveryNotifier = relayNotifier
				agentRestartNotifier = relayNotifier
			default:
				relayReadRedis = newNamedRedisClient(opts, "realtime-read")
				sharded := realtime.NewShardedStreamRelay(hub, relayWriteRedis, relayReadRedis, relayConfig)
				sharded.SetDaemonRuntimeDeliverer(daemonHub)
				relay = sharded
				relayNotifier := daemonws.NewRelayNotifier(daemonHub, sharded)
				daemonWakeup = relayNotifier
				reminderNotifier = relayNotifier
				agentDeliveryNotifier = relayNotifier
				agentRestartNotifier = relayNotifier
			}
			relay.Start(relayCtx)
			broadcaster = realtime.NewDualWriteBroadcaster(hub, relay)
			slog.Info(
				"realtime: Redis relay enabled",
				"node_id", relay.NodeID(),
				"mode", relayMode,
				"shards", relayConfig.Shards,
				"stream_max_len", relayConfig.StreamMaxLen,
				"xread_count", relayConfig.ReadCount,
				"xread_block", relayConfig.ReadBlock.String(),
				"store_pool_size", opts.PoolSize,
				"realtime_write_pool_size", opts.PoolSize,
				"realtime_read_pool_size", opts.PoolSize,
			)
		}
	} else {
		slog.Info("realtime: REDIS_URL not set — using in-memory hub (single-node mode)")
	}
	registerListeners(bus, broadcaster)

	analyticsClient := analytics.NewFromEnv()
	defer analyticsClient.Close()

	queries := db.New(pool)
	hub.SetAuthorizer(newScopeAuthorizer(queries))
	// Order matters: subscriber listeners must register BEFORE notification listeners.
	// The notification listener queries the subscriber table to determine recipients,
	// so subscribers must be written first within the same synchronous event dispatch.
	registerSubscriberListeners(bus, queries)
	registerActivityListeners(bus, queries)
	registerNotificationListeners(bus, queries)

	webPushConfig := handler.Config{
		PublicURL:              strings.TrimRight(strings.TrimSpace(os.Getenv("MULTICA_PUBLIC_URL")), "/"),
		WebPushVAPIDPublicKey:  strings.TrimSpace(os.Getenv("WEB_PUSH_VAPID_PUBLIC_KEY")),
		WebPushVAPIDPrivateKey: strings.TrimSpace(os.Getenv("WEB_PUSH_VAPID_PRIVATE_KEY")),
		WebPushVAPIDSubject:    strings.TrimSpace(os.Getenv("WEB_PUSH_VAPID_SUBJECT")),
		WebPushAppURL:          strings.TrimRight(strings.TrimSpace(os.Getenv("MULTICA_APP_URL")), "/"),
	}
	registerWebPushListeners(bus, queries, webPushConfig)

	metricsConfig := obsmetrics.ConfigFromEnv()
	var metricsServer *http.Server
	var httpMetrics *obsmetrics.HTTPMetrics
	var businessMetrics *obsmetrics.BusinessMetrics
	var httpSLOAlerter *obsmetrics.HTTPRequestSLOAlerter
	var platformHealthAlerter *obsmetrics.PlatformHealthAlerter
	var samplerPool *pgxpool.Pool
	sloCtx, sloCancel := context.WithCancel(context.Background())
	defer sloCancel()
	if metricsConfig.Enabled() {
		// Build a dedicated tiny pool for the BusinessSamplerCollector
		// so a stalled scrape can never starve business traffic. If the
		// pool fails to construct we log and continue without the
		// sampler — the rest of /metrics is still useful.
		var err error
		samplerPool, err = newSamplerDBPool(ctx, dbURL)
		if err != nil {
			slog.Warn("metrics: failed to build sampler pgxpool; sampler disabled", "error", err)
			samplerPool = nil
		}

		metricsRegistry := obsmetrics.NewRegistry(obsmetrics.RegistryOptions{
			Pool:     pool,
			Realtime: realtime.M,
			DaemonWS: daemonws.M,
			Version:  version,
			Commit:   commit,
			BusinessSampler: func() *obsmetrics.BusinessSamplerOptions {
				if samplerPool == nil {
					return nil
				}
				return &obsmetrics.BusinessSamplerOptions{Pool: samplerPool}
			}(),
		})
		httpMetrics = metricsRegistry.HTTP
		businessMetrics = metricsRegistry.Business
		// Forward inbound daemon WS frames into the per-kind counter so
		// dashboards can split heartbeat / unknown / invalid traffic.
		if daemonHub != nil {
			daemonHub.SetMessageKindRecorder(businessMetrics)
		}
		metricsServer = obsmetrics.NewServer(metricsConfig.Addr, metricsRegistry.Gatherer)
		if !obsmetrics.IsLoopbackAddr(metricsConfig.Addr) {
			slog.Warn(
				"metrics listener is not loopback-only; restrict access with private networking, allowlists, or proxy auth",
				"addr", metricsConfig.Addr,
			)
		}
		// In-process served p95 SLO (Frank: every API < 1s). Works without
		// Prometheus Operator — critical for docker/aliyun where scrape
		// rules may not exist.
		httpSLOAlerter = obsmetrics.NewHTTPRequestSLOAlerter(
			metricsRegistry.Gatherer,
			obsmetrics.HTTPRequestSLOConfigFromEnv(),
		)
		// Platform-ops aggregate alerter (task #73 Phase A): fires when
		// multica_reminder_scheduled_overdue >= 3. Silent if sampler is
		// disabled (series absent). Webhook: OPS_ALERT_WEBHOOK_URL, else
		// HTTP_SLO_ALERT_WEBHOOK_URL fallback, else slog only.
		platformHealthAlerter = obsmetrics.NewPlatformHealthAlerter(
			metricsRegistry.Gatherer,
			obsmetrics.PlatformHealthAlertConfigFromEnv(),
		)
	}
	if samplerPool != nil {
		defer samplerPool.Close()
	}

	// Construct the BatchedHeartbeatScheduler before the router so it can
	// be injected into the Handler. The Run goroutine starts below
	// alongside the sweeper, and Stop is called explicitly during graceful
	// shutdown so any pending bumps are flushed before we exit.
	heartbeatScheduler := handler.NewBatchedHeartbeatScheduler(queries, handler.DefaultHeartbeatBatchInterval)

	r, h := NewRouterWithOptions(pool, hub, bus, analyticsClient, storeRedis, RouterOptions{
		HTTPMetrics:        httpMetrics,
		BusinessMetrics:    businessMetrics,
		DaemonHub:          daemonHub,
		DaemonWakeup:       daemonWakeup,
		HeartbeatScheduler: heartbeatScheduler,
	})
	h.ReminderNotifier = reminderNotifier
	h.AgentDeliveryNotifier = agentDeliveryNotifier
	h.AgentRestartNotifier = agentRestartNotifier

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	// Start background workers.
	sweepCtx, sweepCancel := context.WithCancel(context.Background())
	taskSvc := h.TaskService
	taskSvc.Wakeup = daemonWakeup
	taskSvc.Analytics = analyticsClient
	taskSvc.Metrics = businessMetrics
	tc := service.LoadTrainingConfig()
	var rlSvc *service.GraphMemoryRLSessionService
	var rewardSink *arealrl.RewardSink
	if tc.BridgeStubURL != "" {
		arealClient := arealrl.New(tc.BridgeStubURL, tc.AdminAPIKey)
		rlSvc = service.NewGraphMemoryRLSessionService(pool, arealClient, arealClient)
		rewardSink = arealrl.NewRewardSink(rlSvc, arealClient)
		taskSvc.WithTraining(service.NewTrainingSessionDeps(tc, queries))
		slog.Info("training bridge configured", "stub_url", tc.BridgeStubURL)
	} else {
		slog.Info("training bridge not configured (AREAL_BRIDGE_STUB_URL unset) — training hooks disabled")
	}

	// The Dive backend follows the PI environment contract. An approved provider
	// selects its registered backend; the approved model wins per execution and
	// an empty override inherits MULTICA_PI_MODEL through the adapter above.
	diveWorker := service.NewGraphMemoryDiveWorker(
		pool, service.NewGraphMemoryDiveService(pool), rlSvc, service.LoadGraphMemoryLimits(os.Getenv), "",
		func(_ context.Context, model, provider string) (memorygraph.AgentBackend, error) {
			provider = strings.TrimSpace(provider)
			if provider == "" {
				provider = "pi"
			}
			cfg := agentpkg.Config{}
			if provider == "pi" {
				path := strings.TrimSpace(os.Getenv("MULTICA_PI_PATH"))
				if path == "" {
					path = "pi"
				}
				cfg.ExecutablePath = path
			}
			backend, err := agentpkg.New(provider, cfg)
			if err != nil {
				return nil, err
			}
			return graphMemoryDiveModelBackend{
				AgentBackend: backend, defaultModel: strings.TrimSpace(os.Getenv("MULTICA_PI_MODEL")),
			}, nil
		},
	)

	// Graph memory reviewer activation (design §5.1, review P0-1): the
	// ingest hook routes closed interaction-dag segments into the
	// per-workspace memory_graph staging area. Nil-safe per workspace (no
	// memory_graph dir -> skip).
	taskSvc.SetSegmentIngestHook(service.NewGraphMemoryIngestHook(queries, pool, "", businessMetrics))
	// Server-authoritative graph memory recall (spec §1/§3/§14): the daemon
	// recall endpoint resolves identity/scope/version/K server-side and
	// answers 202 only after the durable ledger commit. The env default
	// memory type stays empty (fail-safe): a workspace profile row in graph
	// mode is what enables recalls.
	h.GraphMemoryRecall = service.NewGraphMemoryRecallService(
		pool, service.LoadGraphMemoryLimits(os.Getenv), "", "", service.GraphMemoryHybridSeeder{})
	// Recall execution uses the same PI environment contract as the graph
	// scheduler. The model is also passed through to Explore audit records.
	h.GraphMemoryRecallExecutor = service.NewGraphMemoryRecallExecutor(
		pool, service.NewGraphMemoryDiveService(pool),
		func(context.Context, *service.GraphMemoryRecallPlan) (memorygraph.AgentBackend, error) {
			path := strings.TrimSpace(os.Getenv("MULTICA_PI_PATH"))
			if path == "" {
				path = "pi"
			}
			return agentpkg.New("pi", agentpkg.Config{ExecutablePath: path})
		},
		nil, nil, strings.TrimSpace(os.Getenv("MULTICA_PI_MODEL")),
	)
	// LRM-1049: Autopilot scheduler/listeners/failure-monitor are retired.
	// Reminder owns agent self-wake; API paths return 410.

	// Construct a LivenessStore that mirrors the one wired into the HTTP
	// handler. Both the heartbeat write path (handler) and the sweeper read
	// path (here) must agree on the same Redis-or-Noop choice; if they
	// disagree, online runtimes get falsely marked offline.
	var liveness handler.LivenessStore = handler.NewNoopLivenessStore()
	if storeRedis != nil {
		liveness = handler.NewRedisLivenessStore(storeRedis)
	}

	// Start background sweeper to mark stale runtimes as offline. The daemon
	// Hub is passed as the RunnerPresence source so WS-connected runtimes
	// (LRM-1571: heartbeat-retired daemons) are never swept despite stale
	// last_seen_at.
	var presence service.RunnerPresence = daemonHub
	go runRuntimeSweeper(sweepCtx, queries, liveness, presence, taskSvc, bus)
	// LRM-1571: while a Workspace Runner socket is connected, the server
	// keeps Redis liveness + DB last_seen_at fresh for it — the WS connection
	// state drives liveness for daemons that no longer send heartbeat frames.
	go runRunnerPresenceLivenessTicker(sweepCtx, queries, liveness, daemonHub)
	go runRunnerActivityReaper(sweepCtx, h)
	go runCollaborationTurnWorkers(sweepCtx, h)
	go runChannelOnboardingPublisher(sweepCtx, h)
	go heartbeatScheduler.Run(sweepCtx)
	go runDBStatsLogger(sweepCtx, pool)

	// Lark inbound supervisor: holds the §4.4 WS lease per installation
	// and runs the EventConnector for each. Nil when the Lark master
	// key is unset — self-host deployments that have not opted in to
	// Lark do not pay any goroutine cost. Lifecycle is bound to
	// sweepCtx so the Hub winds down alongside the other long-running
	// workers, AFTER the HTTP server has drained.
	if h.LarkHub != nil {
		go h.LarkHub.Run(sweepCtx)
	}

	// MUL-2957: DB-backed execution scheduler. The scheduler turns the
	// `sys_cron_executions` table into the distributed lease + audit
	// log for internal periodic jobs. The first job is
	// `rollup_agent_usage_hourly`, which replaces the previously
	// operator-registered `pg_cron` entry (still safe to run
	// concurrently — the SQL function holds advisory lock 4246).
	//
	// A failure to register the job is treated as fatal here only at
	// the registration step (a duplicate name is the only realistic
	// cause and indicates a code bug). Once running, the manager
	// surfaces transient errors — DB unreachable, sys_cron_executions
	// missing because of an unusual partial-migration state — by
	// logging them on the tick that fails and retrying on the next
	// cycle, so a temporary outage does not crash the server.
	schedulerMgr := scheduler.NewManager(pool, scheduler.Options{})
	schedulerRegistered := false
	if err := schedulerMgr.Register(scheduler.AgentUsageHourlyJob(pool)); err != nil {
		slog.Warn("scheduler: failed to register agent_usage_hourly rollup job", "error", err)
	} else {
		schedulerRegistered = true
	}
	for _, job := range scheduler.MemoryCurationJobs(pool) {
		if err := schedulerMgr.Register(job); err != nil {
			slog.Warn("scheduler: failed to register memory curation job", "job", job.Name, "error", err)
		} else {
			schedulerRegistered = true
		}
	}
	// Graph memory consolidation registers unconditionally (spec §10): the
	// per-workspace scoped_writer_ready gate (jobs_graph_memory.go) keeps it
	// inert until the scoped writer acceptance gates pass, so no
	// process-level switch is needed or permitted to activate it early.
	if err := schedulerMgr.Register(scheduler.GraphMemoryJobs(pool, businessMetrics)); err != nil {
		slog.Warn("scheduler: failed to register graph memory consolidation job", "error", err)
	} else {
		schedulerRegistered = true
	}
	for _, job := range scheduler.GraphMemoryDiveJobs(pool, diveWorker, rewardSink, rlSvc) {
		if err := schedulerMgr.Register(job); err != nil {
			slog.Warn("scheduler: failed to register graph memory Dive job", "job", job.Name, "error", err)
		} else {
			schedulerRegistered = true
		}
	}
	if err := schedulerMgr.Register(scheduler.ChannelVoiceTranscriptionJob(h)); err != nil {
		slog.Warn("scheduler: failed to register channel voice transcription job", "error", err)
	} else {
		schedulerRegistered = true
	}
	if err := schedulerMgr.Register(scheduler.ChannelVoiceSynthesisJob(h)); err != nil {
		slog.Warn("scheduler: failed to register channel voice synthesis job", "error", err)
	} else {
		schedulerRegistered = true
	}
	if err := schedulerMgr.Register(scheduler.ResearchNextStepJob(h)); err != nil {
		slog.Warn("scheduler: failed to register research nextstep job", "error", err)
	} else {
		schedulerRegistered = true
	}
	if err := schedulerMgr.Register(scheduler.ResearchRunReconcileJob(h)); err != nil {
		slog.Warn("scheduler: failed to register research run reconciliation job", "error", err)
	}
	if err := schedulerMgr.Register(scheduler.ResearchMonitorJob(h)); err != nil {
		slog.Warn("scheduler: failed to register research monitor job", "error", err)
	} else {
		schedulerRegistered = true
	}
	if err := schedulerMgr.Register(scheduler.ResearchProductionWindowJob(h)); err != nil {
		slog.Warn("scheduler: failed to register research production window job", "error", err)
	} else {
		schedulerRegistered = true
	}
	if err := schedulerMgr.Register(scheduler.EnvCheckpointLaneSweepJob(pool)); err != nil {
		slog.Warn("scheduler: failed to register env checkpoint lane sweep job", "error", err)
	} else {
		schedulerRegistered = true
	}
	if err := schedulerMgr.Register(scheduler.IssueExecutionReconcileJob(h.IssueExecution)); err != nil {
		slog.Warn("scheduler: failed to register issue execution reconciliation job", "error", err)
	} else {
		schedulerRegistered = true
	}
	if schedulerRegistered {
		go func() {
			_ = schedulerMgr.Run(sweepCtx)
		}()
	}

	if metricsServer != nil {
		go func() {
			slog.Info("metrics server starting", "addr", metricsConfig.Addr)
			if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("metrics server disabled after startup error", "error", err)
			}
		}()
	}
	if httpSLOAlerter != nil {
		go httpSLOAlerter.Run(sloCtx)
	}
	if platformHealthAlerter != nil {
		go platformHealthAlerter.Run(sloCtx)
	}

	go func() {
		slog.Info("server starting", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down server")

	// Order matters: drain in-flight HTTP first so any heartbeat handlers
	// finish calling Schedule() before we stop the scheduler. Otherwise a
	// late heartbeat could enqueue a pending ID after Run has already
	// drained and exited, and Stop() would not flush it.
	apiShutdownCtx, apiShutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := srv.Shutdown(apiShutdownCtx); err != nil {
		apiShutdownCancel()
		slog.Error("server forced to shutdown", "error", err)
		os.Exit(1)
	}
	apiShutdownCancel()

	// HTTP is fully drained — safe to stop the sweeper and flush the
	// final batch of queued heartbeat bumps.
	sweepCancel()
	heartbeatScheduler.Stop()

	// Join the Lark Hub's per-installation supervisor goroutines so the
	// lease renewer can issue a final release before process exit;
	// otherwise the next replica would have to wait the full LeaseTTL
	// before picking up the installation on the other side of the
	// redeploy. The wait is bounded — if a supervisor is wedged (DB
	// pool stalled, a future real EventConnector ignoring ctx, etc.)
	// the fallback is the natural LeaseTTL expiry on the other side,
	// which is strictly better than holding shutdown open forever.
	if h.LarkHub != nil {
		if !h.LarkHub.WaitWithTimeout(h.LarkHub.ShutdownTimeout()) {
			slog.Warn("lark hub: supervisors did not exit within shutdown timeout; proceeding",
				"timeout", h.LarkHub.ShutdownTimeout().String(),
			)
		}
	}

	sloCancel()
	if metricsServer != nil {
		metricsShutdownCtx, metricsShutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := metricsServer.Shutdown(metricsShutdownCtx); err != nil {
			slog.Error("metrics server forced to shutdown", "error", err)
		}
		metricsShutdownCancel()
	}
	slog.Info("server stopped")
}

// runCollaborationTurnWorkers periodically expires overdue Collaboration turn
// grants so a stuck/unresponsive participant cannot wedge a session forever.
func runCollaborationTurnWorkers(ctx context.Context, h *handler.Handler) {
	if h == nil {
		return
	}
	timeoutTicker := time.NewTicker(time.Second)
	defer timeoutTicker.Stop()
	h.SweepCollaborationTurnTimeouts(ctx, 64)
	for {
		select {
		case <-ctx.Done():
			return
		case <-timeoutTicker.C:
			h.SweepCollaborationTurnTimeouts(ctx, 64)
		}
	}
}
