package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

var sandboxdCmd = &cobra.Command{
	Use:   "sandboxd",
	Short: "Run the shared sandbox node connector",
	Long:  "Connects a Cube sandbox cluster to Multica using an outbound WSS/HTTPS control channel.",
	RunE:  runSandboxd,
}

type sandboxdConfig struct {
	ServerURL     string        `json:"server_url"`
	NodeToken     string        `json:"node_token"`
	NodeKey       string        `json:"node_key"`
	Name          string        `json:"name"`
	OwnerUserID   string        `json:"owner_user_id"`
	SandboxServer string        `json:"sandbox_server"`
	CubeProxyHTTP string        `json:"cube_proxy_http"`
	CubeDomain    string        `json:"cube_domain"`
	TemplateID    string        `json:"cube_template_id"`
	Concurrency   int           `json:"concurrency"`
	PollInterval  time.Duration `json:"-"`
	PollIntervalS string        `json:"poll_interval"`
}

type sandboxdClient struct {
	cfg        sandboxdConfig
	configPath string
	http       *http.Client
	logger     *slog.Logger

	templatesMu        sync.Mutex
	cachedTemplates    []cubeTemplateSummary
	templatesFetchedAt time.Time
}

type cubeTemplateSummary struct {
	TemplateID   string `json:"templateID"`
	Status       string `json:"status"`
	CreatedAt    string `json:"createdAt,omitempty"`
	ImageInfo    string `json:"imageInfo,omitempty"`
	InstanceType string `json:"instanceType,omitempty"`
	LastError    string `json:"lastError,omitempty"`
	Version      string `json:"version,omitempty"`
	JobID        string `json:"jobID,omitempty"`
}

type sandboxClaimResponse struct {
	Jobs []sandboxJob `json:"jobs"`
}

type sandboxJob struct {
	ID              string          `json:"id"`
	WorkspaceID     string          `json:"workspace_id"`
	InitiatorUserID string          `json:"initiator_user_id"`
	NodeID          string          `json:"node_id"`
	InstanceID      string          `json:"instance_id"`
	Type            string          `json:"type"`
	Payload         json.RawMessage `json:"payload"`
	TaskToken       string          `json:"task_token"`
}

type sandboxJobPayload struct {
	Template   string            `json:"template"`
	Name       string            `json:"name"`
	Limits     json.RawMessage   `json:"limits"`
	Metadata   json.RawMessage   `json:"metadata"`
	Runtime    map[string]string `json:"runtime"`
	RuntimeEnv map[string]string `json:"runtime_env"`
	InstanceID string            `json:"instance_id"`
	LocalRef   string            `json:"local_ref"`
}

type cubeSandbox struct {
	SandboxID  string         `json:"sandboxID"`
	TemplateID string         `json:"templateID"`
	State      string         `json:"state"`
	Domain     string         `json:"domain"`
	Raw        map[string]any `json:"-"`
}

func init() {
	f := sandboxdCmd.Flags()
	f.String("config", "", "sandboxd config file (default: ./.multica/sandboxd.json, then ~/.multica/sandboxd.json)")
	f.String("server-url", "", "Multica server URL (overrides config)")
	f.String("node-token", "", "Sandbox node token, msn_... (overrides config)")
	f.String("node-key", "", "Stable sandbox node key (overrides config)")
	f.String("name", "", "Human-readable sandbox node name (overrides config)")
	f.String("owner-user-id", "", "Multica user id that owns this sandbox node (overrides config)")
	f.String("sandbox-server", "", "Cube API URL (overrides config)")
	f.String("cube-proxy-http", "", "Cube proxy HTTP URL for /execute (overrides config)")
	f.String("cube-domain", "", "Cube sandbox domain (overrides config)")
	f.String("cube-template-id", "", "Default Cube template id (overrides config)")
	f.Int("concurrency", 1, "Max jobs claimed per poll")
	f.Duration("poll-interval", 5*time.Second, "Fallback job poll interval")
}

func runSandboxd(cmd *cobra.Command, _ []string) error {
	cfg, configPath, err := loadSandboxdConfig(flagString(cmd, "config"))
	if err != nil {
		return err
	}
	overrideStringFlag(cmd, "server-url", &cfg.ServerURL)
	overrideStringFlag(cmd, "node-token", &cfg.NodeToken)
	overrideStringFlag(cmd, "node-key", &cfg.NodeKey)
	overrideStringFlag(cmd, "name", &cfg.Name)
	overrideStringFlag(cmd, "owner-user-id", &cfg.OwnerUserID)
	overrideStringFlag(cmd, "sandbox-server", &cfg.SandboxServer)
	overrideStringFlag(cmd, "cube-proxy-http", &cfg.CubeProxyHTTP)
	overrideStringFlag(cmd, "cube-domain", &cfg.CubeDomain)
	overrideStringFlag(cmd, "cube-template-id", &cfg.TemplateID)
	if cmd.Flags().Changed("concurrency") {
		cfg.Concurrency = sandboxFlagInt(cmd, "concurrency")
	}
	if cmd.Flags().Changed("poll-interval") {
		cfg.PollInterval = sandboxFlagDuration(cmd, "poll-interval")
	}
	if cfg.PollInterval <= 0 && strings.TrimSpace(cfg.PollIntervalS) != "" {
		parsed, err := time.ParseDuration(strings.TrimSpace(cfg.PollIntervalS))
		if err != nil {
			return fmt.Errorf("invalid poll_interval: %w", err)
		}
		cfg.PollInterval = parsed
	}
	if cfg.CubeProxyHTTP == "" {
		cfg.CubeProxyHTTP = "http://127.0.0.1"
	}
	if cfg.CubeDomain == "" {
		cfg.CubeDomain = "cube.app"
	}
	if cfg.ServerURL == "" || cfg.NodeToken == "" || cfg.NodeKey == "" || cfg.OwnerUserID == "" || cfg.SandboxServer == "" {
		return fmt.Errorf("server_url, node_token, node_key, owner_user_id, and sandbox_server are required in sandboxd config")
	}
	if cfg.Name == "" {
		cfg.Name = cfg.NodeKey
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c := &sandboxdClient{
		cfg:        cfg,
		configPath: configPath,
		http:       &http.Client{Timeout: 120 * time.Second},
		logger:     slog.Default(),
	}
	if err := c.register(ctx); err != nil {
		return err
	}
	go c.wsLoop(ctx)
	// Template listing hits the local Cube API and can be slow. Keep it off the
	// claim/heartbeat path so a sluggish /templates call cannot starve liveness
	// updates (frontend/server treat >30s without last_seen as offline).
	go c.templateRefreshLoop(ctx)
	return c.pollLoop(ctx)
}

func (c *sandboxdClient) register(ctx context.Context) error {
	// Best-effort: don't block node registration on a slow Cube /templates.
	refreshCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	_ = c.refreshTemplates(refreshCtx, true)
	cancel()
	return c.postJSON(ctx, "/api/sandbox/node/register", c.cfg.NodeToken, map[string]any{
		"node_key":        c.cfg.NodeKey,
		"name":            c.cfg.Name,
		"owner_user_id":   c.cfg.OwnerUserID,
		"max_concurrency": c.cfg.Concurrency,
		"capabilities":    []string{"create", "stop", "resume", "delete", "reconfigure", "create_template", "delete_template"},
		"metadata":        c.nodeMetadata(),
	}, nil)
}

func (c *sandboxdClient) pollLoop(ctx context.Context) error {
	ticker := time.NewTicker(c.cfg.PollInterval)
	defer ticker.Stop()
	// Keep heartbeat comfortably inside the 30s online window even if a claim
	// tick is delayed. Claim itself also touches liveness every poll.
	heartbeatEvery := c.cfg.PollInterval * 3
	if heartbeatEvery < 10*time.Second {
		heartbeatEvery = 10 * time.Second
	}
	if heartbeatEvery > 15*time.Second {
		heartbeatEvery = 15 * time.Second
	}
	lastHeartbeat := time.Time{}
	for {
		if err := c.claimAndRun(ctx); err != nil && ctx.Err() == nil {
			c.logger.Warn("sandboxd claim failed", "error", err)
		}
		if time.Since(lastHeartbeat) >= heartbeatEvery {
			if err := c.heartbeat(ctx); err != nil && ctx.Err() == nil {
				c.logger.Warn("sandboxd heartbeat failed", "error", err)
			} else {
				lastHeartbeat = time.Now()
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (c *sandboxdClient) heartbeat(ctx context.Context) error {
	// Fast path only: publish whatever templates are already cached. Refreshing
	// Cube here previously blocked claimAndRun and made nodes flap offline.
	var resp struct {
		Metadata json.RawMessage `json:"metadata"`
	}
	if err := c.postJSON(ctx, "/api/sandbox/node/heartbeat", c.cfg.NodeToken, map[string]any{
		"metadata": c.nodeMetadata(),
	}, &resp); err != nil {
		return err
	}
	// Control-plane default template wins: owners can change it in the UI;
	// sandboxd applies it in-memory and persists to the local config file.
	if desired := stringFromRawObject(resp.Metadata, "cube_template_id"); desired != "" && desired != c.cfg.TemplateID {
		previous := c.cfg.TemplateID
		c.cfg.TemplateID = desired
		if err := c.persistCubeTemplateID(desired); err != nil {
			c.logger.Warn("failed to persist default cube template to config", "error", err, "path", c.configPath, "cube_template_id", desired)
		} else {
			c.logger.Info("applied default cube template from control plane", "cube_template_id", desired, "previous", previous, "path", c.configPath)
		}
	}
	return nil
}

// persistCubeTemplateID updates only cube_template_id in the on-disk config so
// restarts keep the control-plane choice. Other fields are preserved as-is.
func (c *sandboxdClient) persistCubeTemplateID(templateID string) error {
	if strings.TrimSpace(c.configPath) == "" {
		return fmt.Errorf("sandboxd config path is empty")
	}
	raw, err := os.ReadFile(c.configPath)
	if err != nil {
		return err
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("parse sandboxd config %s: %w", c.configPath, err)
	}
	if obj == nil {
		obj = map[string]any{}
	}
	obj["cube_template_id"] = templateID
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	tmp := c.configPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, c.configPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (c *sandboxdClient) templateRefreshLoop(ctx context.Context) {
	_ = c.refreshTemplates(ctx, true)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = c.refreshTemplates(ctx, true)
		}
	}
}

func (c *sandboxdClient) nodeMetadata() map[string]any {
	c.templatesMu.Lock()
	templates := append([]cubeTemplateSummary(nil), c.cachedTemplates...)
	syncedAt := c.templatesFetchedAt
	c.templatesMu.Unlock()

	meta := map[string]any{
		"cube_api_url":     c.cfg.SandboxServer,
		"cube_proxy_http":  c.cfg.CubeProxyHTTP,
		"cube_domain":      c.cfg.CubeDomain,
		"cube_template_id": c.cfg.TemplateID,
		"templates":        templates,
	}
	if !syncedAt.IsZero() {
		meta["templates_synced_at"] = syncedAt.UTC().Format(time.RFC3339)
	}
	return meta
}

// refreshTemplates loads Cube GET /templates into the in-memory cache.
// force=true bypasses the refresh interval (used on register / background loop).
func (c *sandboxdClient) refreshTemplates(ctx context.Context, force bool) error {
	const refreshInterval = 30 * time.Second
	c.templatesMu.Lock()
	if !force && !c.templatesFetchedAt.IsZero() && time.Since(c.templatesFetchedAt) < refreshInterval {
		c.templatesMu.Unlock()
		return nil
	}
	c.templatesMu.Unlock()

	// Bound Cube listing so a hung templates endpoint cannot pin a goroutine
	// forever (the shared HTTP client timeout is 120s for sandbox jobs).
	listCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	templates, err := c.listCubeTemplates(listCtx)
	if err != nil {
		c.logger.Warn("list cube templates failed", "error", err)
		return err
	}
	c.templatesMu.Lock()
	c.cachedTemplates = templates
	c.templatesFetchedAt = time.Now()
	c.templatesMu.Unlock()
	return nil
}

func (c *sandboxdClient) listCubeTemplates(ctx context.Context) ([]cubeTemplateSummary, error) {
	var raw []map[string]any
	if err := c.cubeJSON(ctx, http.MethodGet, "/templates", nil, "", &raw); err != nil {
		return nil, err
	}
	out := make([]cubeTemplateSummary, 0, len(raw))
	for _, item := range raw {
		id := firstNonEmpty(
			stringFromMap(item, "templateID"),
			stringFromMap(item, "template_id"),
			stringFromMap(item, "id"),
		)
		if id == "" {
			continue
		}
		out = append(out, cubeTemplateSummary{
			TemplateID:   id,
			Status:       firstNonEmpty(stringFromMap(item, "status"), "unknown"),
			CreatedAt:    stringFromMap(item, "createdAt"),
			ImageInfo:    stringFromMap(item, "imageInfo"),
			InstanceType: stringFromMap(item, "instanceType"),
			LastError:    stringFromMap(item, "lastError"),
			Version:      stringFromMap(item, "version"),
			JobID:        firstNonEmpty(stringFromMap(item, "jobID"), stringFromMap(item, "job_id")),
		})
	}
	return out, nil
}

func stringFromMap(obj map[string]any, key string) string {
	if obj == nil {
		return ""
	}
	if s, ok := obj[key].(string); ok {
		return s
	}
	return ""
}

func (c *sandboxdClient) wsLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := c.runWS(ctx); err != nil && ctx.Err() == nil {
			c.logger.Warn("sandboxd websocket disconnected", "error", err, "retry_in", backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (c *sandboxdClient) runWS(ctx context.Context) error {
	u, err := url.Parse(strings.TrimRight(c.cfg.ServerURL, "/"))
	if err != nil {
		return err
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/sandbox/node/ws"
	headers := http.Header{"Authorization": []string{"Bearer " + c.cfg.NodeToken}}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), headers)
	if err != nil {
		return err
	}
	defer conn.Close()
	c.logger.Info("sandboxd websocket connected")
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var msg protocol.Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		if msg.Type == protocol.EventSandboxJobAvailable {
			go func() {
				if err := c.claimAndRun(context.Background()); err != nil {
					c.logger.Warn("sandboxd wakeup claim failed", "error", err)
				}
			}()
		}
	}
}

func (c *sandboxdClient) claimAndRun(ctx context.Context) error {
	var resp sandboxClaimResponse
	if err := c.postJSON(ctx, "/api/sandbox/node/jobs/claim", c.cfg.NodeToken, map[string]any{"capacity": c.cfg.Concurrency}, &resp); err != nil {
		return err
	}
	for _, job := range resp.Jobs {
		job := job
		go c.handleJob(context.Background(), job)
	}
	return nil
}

func (c *sandboxdClient) handleJob(ctx context.Context, job sandboxJob) {
	log := c.logger.With("job_id", job.ID, "type", job.Type, "instance_id", job.InstanceID)
	if err := c.postJSON(ctx, "/api/sandbox/jobs/"+job.ID+"/start", job.TaskToken, map[string]any{}, nil); err != nil {
		log.Warn("mark sandbox job start failed", "error", err)
	}
	result, err := c.callCube(ctx, job)
	if err != nil {
		_ = c.postJSON(ctx, "/api/sandbox/jobs/"+job.ID+"/fail", job.TaskToken, map[string]any{"error": err.Error()}, nil)
		log.Warn("sandbox job failed", "error", err)
		return
	}
	if err := c.postJSON(ctx, "/api/sandbox/jobs/"+job.ID+"/complete", job.TaskToken, result, nil); err != nil {
		log.Warn("complete sandbox job failed", "error", err)
		return
	}
	log.Info("sandbox job completed")
}

func (c *sandboxdClient) callCube(ctx context.Context, job sandboxJob) (map[string]any, error) {
	payload := parseSandboxJobPayload(job.Payload)
	sandboxID := firstNonEmpty(payload.LocalRef, stringFromRawObject(payload.Metadata, "local_ref"), stringFromRawObject(job.Payload, "local_ref"))
	switch job.Type {
	case "create":
		return c.createCubeSandbox(ctx, job, payload)
	case "stop":
		return c.cubeLifecycle(ctx, sandboxID, "/pause", true)
	case "resume":
		return c.resumeCubeSandbox(ctx, sandboxID, payload)
	case "reconfigure":
		return c.reconfigureCubeSandbox(ctx, sandboxID, payload)
	case "delete":
		return c.deleteCubeSandbox(ctx, sandboxID)
	case "create_template":
		return c.createCubeSnapshotTemplate(ctx, sandboxID, payload)
	case "delete_template":
		return c.deleteCubeSnapshotTemplate(ctx, payload)
	default:
		return nil, fmt.Errorf("unsupported sandbox job type %q", job.Type)
	}
}

func (c *sandboxdClient) createCubeSandbox(ctx context.Context, job sandboxJob, payload sandboxJobPayload) (map[string]any, error) {
	// Explicit job template wins over the node default (cube_template_id).
	// Only empty / "default" fall back to the configured TemplateID.
	templateID := strings.TrimSpace(payload.Template)
	if templateID == "" || templateID == "default" {
		templateID = c.cfg.TemplateID
	}
	if templateID == "" {
		return nil, fmt.Errorf("cube template id is required")
	}
	timeout := intValueFromRaw(payload.Limits, "timeout", 3600)
	var cube cubeSandbox
	if err := c.cubeJSON(ctx, http.MethodPost, "/sandboxes", map[string]any{"templateID": templateID, "timeout": timeout}, "", &cube); err != nil {
		return nil, err
	}
	if cube.SandboxID == "" {
		return nil, fmt.Errorf("cube create response missing sandboxID")
	}
	runtimeEnv := mergeRuntimeEnv(payload.RuntimeEnv, payload.Runtime)
	if err := c.startRuntimeInCube(ctx, cube.SandboxID, runtimeEnv); err != nil {
		return nil, err
	}
	endpoint := map[string]any{
		"kind":       "cube",
		"sandbox_id": cube.SandboxID,
		"novnc_url":  fmt.Sprintf("http://6080-%s.%s/vnc.html?autoconnect=1&encrypt=0", cube.SandboxID, c.cfg.CubeDomain),
		"code_url":   fmt.Sprintf("http://49999-%s.%s", cube.SandboxID, c.cfg.CubeDomain),
		"proxy":      c.cfg.CubeProxyHTTP,
	}
	return map[string]any{
		"result": map[string]any{
			"cube":          cube.Raw,
			"runtime_env":   redactedRuntimeEnv(runtimeEnv),
			"instance_id":   job.InstanceID,
			"workspace_id":  job.WorkspaceID,
			"endpoint_info": endpoint,
		},
		"local_ref":     cube.SandboxID,
		"endpoint_info": endpoint,
	}, nil
}

func (c *sandboxdClient) resumeCubeSandbox(ctx context.Context, sandboxID string, payload sandboxJobPayload) (map[string]any, error) {
	result, err := c.cubeLifecycle(ctx, sandboxID, "/resume", false)
	if err != nil {
		return nil, err
	}
	runtimeEnv := mergeRuntimeEnv(payload.RuntimeEnv, payload.Runtime)
	if len(runtimeEnv) > 0 && runtimeEnv["MULTICA_TOKEN"] != "" && hasRuntimeModelConfig(payload.Runtime) {
		if err := c.stopRuntimeInCube(ctx, sandboxID); err != nil {
			return nil, err
		}
		if err := c.startRuntimeInCube(ctx, sandboxID, runtimeEnv); err != nil {
			return nil, err
		}
		result["result"] = map[string]any{"resumed": true, "runtime_restarted": true}
	}
	return result, nil
}

// createCubeSnapshotTemplate calls Cube POST /sandboxes/{id}/snapshots.
// The resulting snapshotID is a reusable Cube template for future creates.
func (c *sandboxdClient) createCubeSnapshotTemplate(ctx context.Context, sandboxID string, payload sandboxJobPayload) (map[string]any, error) {
	if sandboxID == "" {
		return nil, fmt.Errorf("cube sandbox id is required")
	}
	body := map[string]any{}
	if name := strings.TrimSpace(payload.Name); name != "" {
		body["name"] = name
	}
	var raw map[string]any
	// Snapshots can take several minutes while Cube pauses the sandbox.
	if err := c.cubeJSONWithTimeout(ctx, 10*time.Minute, http.MethodPost, "/sandboxes/"+url.PathEscape(sandboxID)+"/snapshots", body, "", &raw); err != nil {
		return nil, err
	}
	snapshotID := firstNonEmpty(
		stringFromMap(raw, "snapshotID"),
		stringFromMap(raw, "snapshot_id"),
		stringFromMap(raw, "templateID"),
		stringFromMap(raw, "template_id"),
	)
	if snapshotID == "" {
		return nil, fmt.Errorf("cube snapshot response missing snapshotID")
	}
	// Snapshot may leave the sandbox paused; resume so Multica runtime stays usable.
	_ = c.cubeJSON(ctx, http.MethodPost, "/sandboxes/"+url.PathEscape(sandboxID)+"/resume", map[string]any{}, "", nil)
	refreshCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	_ = c.refreshTemplates(refreshCtx, true)
	cancel()
	return map[string]any{
		"local_ref": sandboxID,
		"result": map[string]any{
			"snapshot_id": snapshotID,
			"template_id": snapshotID,
			"names":       raw["names"],
			"cube":        raw,
		},
	}, nil
}

// deleteCubeSnapshotTemplate removes a Cube snapshot template
// (DELETE /templates/{snapshotID}). Snapshots are stored as templates.
func (c *sandboxdClient) deleteCubeSnapshotTemplate(ctx context.Context, payload sandboxJobPayload) (map[string]any, error) {
	templateID := strings.TrimSpace(payload.LocalRef)
	if templateID == "" {
		return nil, fmt.Errorf("cube snapshot id is required")
	}
	if err := c.cubeJSON(ctx, http.MethodDelete, "/templates/"+url.PathEscape(templateID), nil, "", nil); err != nil {
		// Already gone on Cube — treat as success so Multica can drop its row.
		if strings.Contains(err.Error(), " returned 404") {
			c.logger.Info("cube snapshot already absent; treating delete as success", "template_id", templateID)
		} else {
			return nil, err
		}
	}
	refreshCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	_ = c.refreshTemplates(refreshCtx, true)
	cancel()
	return map[string]any{
		"local_ref": templateID,
		"result": map[string]any{
			"deleted":     true,
			"snapshot_id": templateID,
			"template_id": templateID,
		},
	}, nil
}

func (c *sandboxdClient) reconfigureCubeSandbox(ctx context.Context, sandboxID string, payload sandboxJobPayload) (map[string]any, error) {
	if sandboxID == "" {
		return nil, fmt.Errorf("cube sandbox id is required")
	}
	runtimeEnv := mergeRuntimeEnv(payload.RuntimeEnv, payload.Runtime)
	if runtimeEnv["MULTICA_TOKEN"] == "" {
		return nil, fmt.Errorf("runtime_env missing MULTICA_TOKEN")
	}
	// Best-effort resume so /execute works when the cube sandbox was paused.
	_ = c.cubeJSON(ctx, http.MethodPost, "/sandboxes/"+url.PathEscape(sandboxID)+"/resume", map[string]any{}, "", nil)
	if err := c.stopRuntimeInCube(ctx, sandboxID); err != nil {
		return nil, err
	}
	if err := c.startRuntimeInCube(ctx, sandboxID, runtimeEnv); err != nil {
		return nil, err
	}
	return map[string]any{
		"local_ref": sandboxID,
		"result": map[string]any{
			"reconfigured": true,
			"runtime_env":  redactedRuntimeEnv(runtimeEnv),
		},
	}, nil
}

func hasRuntimeModelConfig(runtime map[string]string) bool {
	for _, key := range []string{"api_key", "base_url", "model", "TEAM_API_KEY", "TEAM_BASE_URL", "TEAM_MODEL"} {
		if strings.TrimSpace(runtime[key]) != "" {
			return true
		}
	}
	return false
}

func (c *sandboxdClient) stopRuntimeInCube(ctx context.Context, sandboxID string) error {
	code := `import subprocess, time
subprocess.run(["bash", "-lc", "pkill -f 'multica daemon' || pkill -f 'multica-daemon' || true"], check=False)
time.sleep(2)
print("runtime stopped")
`
	return c.cubeJSON(ctx, http.MethodPost, "/execute", map[string]any{"code": code, "language": "python"}, fmt.Sprintf("49999-%s.%s", sandboxID, c.cfg.CubeDomain), nil)
}

func (c *sandboxdClient) cubeLifecycle(ctx context.Context, sandboxID, suffix string, stopped bool) (map[string]any, error) {
	if sandboxID == "" {
		return nil, fmt.Errorf("cube sandbox id is required")
	}
	if err := c.cubeJSON(ctx, http.MethodPost, "/sandboxes/"+url.PathEscape(sandboxID)+suffix, map[string]any{}, "", nil); err != nil {
		msg := err.Error()
		if suffix == "/pause" && strings.Contains(msg, "already paused") {
			return map[string]any{"local_ref": sandboxID, "result": map[string]any{"idempotent": true}, "endpoint_info": nil}, nil
		}
		if suffix == "/resume" && (strings.Contains(msg, "not paused") || strings.Contains(msg, "already") || strings.Contains(msg, "running")) {
			return map[string]any{"local_ref": sandboxID, "result": map[string]any{"idempotent": true}, "endpoint_info": nil}, nil
		}
		return nil, err
	}
	return map[string]any{"local_ref": sandboxID, "result": map[string]any{"stopped": stopped, "resumed": !stopped}, "endpoint_info": nil}, nil
}

func (c *sandboxdClient) deleteCubeSandbox(ctx context.Context, sandboxID string) (map[string]any, error) {
	if sandboxID == "" {
		return map[string]any{"result": map[string]any{"deleted": true, "idempotent": true}}, nil
	}
	if err := c.cubeJSON(ctx, http.MethodDelete, "/sandboxes/"+url.PathEscape(sandboxID), nil, "", nil); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "not found") || strings.Contains(msg, "404") {
			return map[string]any{"local_ref": sandboxID, "result": map[string]any{"deleted": true, "idempotent": true}}, nil
		}
		if strings.Contains(msg, "sandbox not in normal state") {
			_ = c.cubeJSON(ctx, http.MethodPost, "/sandboxes/"+url.PathEscape(sandboxID)+"/resume", map[string]any{}, "", nil)
			for i := 0; i < 5; i++ {
				time.Sleep(2 * time.Second)
				if err := c.cubeJSON(ctx, http.MethodDelete, "/sandboxes/"+url.PathEscape(sandboxID), nil, "", nil); err == nil || strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "404") {
					return map[string]any{"local_ref": sandboxID, "result": map[string]any{"deleted": true}}, nil
				}
			}
		}
		return nil, err
	}
	return map[string]any{"local_ref": sandboxID, "result": map[string]any{"deleted": true}}, nil
}

func (c *sandboxdClient) startRuntimeInCube(ctx context.Context, sandboxID string, runtimeEnv map[string]string) error {
	if len(runtimeEnv) == 0 || runtimeEnv["MULTICA_TOKEN"] == "" {
		return fmt.Errorf("runtime_env missing MULTICA_TOKEN")
	}
	code := fmt.Sprintf(`import json, os, subprocess
runtime_env = json.loads(%q)
env = os.environ.copy()
env.update(runtime_env)
env["PATH"] = "/home/user/.npm-global/bin:/home/user/.bun/bin:/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin"
proc = subprocess.run(["bash", "-lc", "/usr/local/bin/start-multica-runtime.sh"], text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=60, env=env)
print(proc.stdout)
if proc.returncode != 0:
    raise SystemExit(proc.returncode)
`, mustJSON(runtimeEnv))
	return c.cubeJSON(ctx, http.MethodPost, "/execute", map[string]any{"code": code, "language": "python"}, fmt.Sprintf("49999-%s.%s", sandboxID, c.cfg.CubeDomain), nil)
}

func (c *sandboxdClient) cubeJSON(ctx context.Context, method, path string, body any, host string, out any) error {
	return c.cubeJSONWithClient(ctx, c.http, method, path, body, host, out)
}

func (c *sandboxdClient) cubeJSONWithTimeout(ctx context.Context, timeout time.Duration, method, path string, body any, host string, out any) error {
	client := &http.Client{Timeout: timeout}
	return c.cubeJSONWithClient(ctx, client, method, path, body, host, out)
}

func (c *sandboxdClient) cubeJSONWithClient(ctx context.Context, client *http.Client, method, path string, body any, host string, out any) error {
	if client == nil {
		client = c.http
	}
	var data io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		data = bytes.NewReader(raw)
	}
	base := c.cfg.SandboxServer
	if host != "" {
		base = c.cfg.CubeProxyHTTP
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, data)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if host != "" {
		req.Host = host
		req.Header.Set("Host", host)
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	resBody, _ := io.ReadAll(res.Body)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("cube %s %s returned %d: %s", method, path, res.StatusCode, strings.TrimSpace(string(resBody)))
	}
	if out != nil && len(resBody) > 0 {
		if cs, ok := out.(*cubeSandbox); ok {
			var raw map[string]any
			if err := json.Unmarshal(resBody, &raw); err != nil {
				return err
			}
			cs.Raw = raw
			if v, _ := raw["sandboxID"].(string); v != "" {
				cs.SandboxID = v
			}
			if v, _ := raw["templateID"].(string); v != "" {
				cs.TemplateID = v
			}
			if v, _ := raw["state"].(string); v != "" {
				cs.State = v
			}
			if v, _ := raw["domain"].(string); v != "" {
				cs.Domain = v
			}
			return nil
		}
		return json.Unmarshal(resBody, out)
	}
	return nil
}

func (c *sandboxdClient) postJSON(ctx context.Context, path, token string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.cfg.ServerURL, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	resBody, _ := io.ReadAll(res.Body)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("POST %s returned %d: %s", path, res.StatusCode, strings.TrimSpace(string(resBody)))
	}
	if out != nil && len(resBody) > 0 {
		return json.Unmarshal(resBody, out)
	}
	return nil
}

func parseSandboxJobPayload(raw json.RawMessage) sandboxJobPayload {
	var p sandboxJobPayload
	_ = json.Unmarshal(raw, &p)
	return p
}

func mergeRuntimeEnv(base map[string]string, runtime map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		if strings.TrimSpace(v) != "" {
			out[k] = v
		}
	}
	aliases := map[string][]string{
		"TEAM_API_KEY":  {"TEAM_API_KEY", "team_api_key", "api_key"},
		"TEAM_BASE_URL": {"TEAM_BASE_URL", "team_base_url", "base_url"},
		"TEAM_MODEL":    {"TEAM_MODEL", "team_model", "model"},
	}
	for target, keys := range aliases {
		for _, key := range keys {
			if v := strings.TrimSpace(runtime[key]); v != "" {
				out[target] = v
				break
			}
		}
	}
	if out["MULTICA_DAEMON_ENABLED"] == "" {
		out["MULTICA_DAEMON_ENABLED"] = "1"
	}
	return out
}

func redactedRuntimeEnv(env map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range env {
		if strings.Contains(strings.ToLower(k), "token") || strings.Contains(strings.ToLower(k), "key") {
			if v != "" {
				out[k] = "***"
			}
			continue
		}
		out[k] = v
	}
	return out
}

func mustJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func stringFromRawObject(raw json.RawMessage, key string) string {
	var obj map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &obj) != nil {
		return ""
	}
	if s, ok := obj[key].(string); ok {
		return s
	}
	return ""
}

func intValueFromRaw(raw json.RawMessage, key string, fallback int) int {
	var obj map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &obj) != nil {
		return fallback
	}
	switch v := obj[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		var parsed int
		if _, err := fmt.Sscanf(v, "%d", &parsed); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

func loadSandboxdConfig(path string) (sandboxdConfig, string, error) {
	candidates := []string{}
	if strings.TrimSpace(path) != "" {
		candidates = append(candidates, strings.TrimSpace(path))
	} else {
		candidates = append(candidates, filepath.Join(".multica", "sandboxd.json"))
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			candidates = append(candidates, filepath.Join(home, ".multica", "sandboxd.json"))
		}
	}
	for _, candidate := range candidates {
		raw, err := os.ReadFile(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return sandboxdConfig{}, "", err
		}
		var cfg sandboxdConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return sandboxdConfig{}, "", fmt.Errorf("parse sandboxd config %s: %w", candidate, err)
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			abs = candidate
		}
		return cfg, abs, nil
	}
	return sandboxdConfig{}, "", fmt.Errorf("sandboxd config not found; create .multica/sandboxd.json or pass --config")
}

func overrideStringFlag(cmd *cobra.Command, name string, target *string) {
	if cmd.Flags().Changed(name) {
		*target = strings.TrimSpace(flagString(cmd, name))
	}
}

func sandboxFlagInt(cmd *cobra.Command, name string) int { v, _ := cmd.Flags().GetInt(name); return v }
func sandboxFlagDuration(cmd *cobra.Command, name string) time.Duration {
	v, _ := cmd.Flags().GetDuration(name)
	return v
}
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
