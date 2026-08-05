package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
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
	ServerURL        string        `json:"server_url"`
	NodeToken        string        `json:"node_token"`
	NodeKey          string        `json:"node_key"`
	Name             string        `json:"name"`
	OwnerUserID      string        `json:"owner_user_id"`
	SandboxServer    string        `json:"sandbox_server"`
	CubeProxyHTTP    string        `json:"cube_proxy_http"`
	CubeDomain       string        `json:"cube_domain"`
	TemplateID       string        `json:"cube_template_id"`
	DockerPublicHost string        `json:"docker_public_host"`
	Concurrency      int           `json:"concurrency"`
	PollInterval     time.Duration `json:"-"`
	PollIntervalS    string        `json:"poll_interval"`
}

type sandboxdClient struct {
	cfg        sandboxdConfig
	configPath string
	http       *http.Client
	logger     *slog.Logger

	templatesMu           sync.Mutex
	cachedTemplates       []cubeTemplateSummary
	templatesFetchedAt    time.Time
	dockerImagesMu        sync.Mutex
	cachedDockerImages    []dockerImageSummary
	dockerImagesFetchedAt time.Time
	dockerImagesError     string
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

type dockerImageSummary struct {
	ImageRef     string `json:"image_ref"`
	Repository   string `json:"repository"`
	Tag          string `json:"tag"`
	ID           string `json:"id"`
	Digest       string `json:"digest,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	CreatedSince string `json:"created_since,omitempty"`
	Size         string `json:"size,omitempty"`
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
	Template         string            `json:"template"`
	Name             string            `json:"name"`
	Limits           json.RawMessage   `json:"limits"`
	Metadata         json.RawMessage   `json:"metadata"`
	Runtime          json.RawMessage   `json:"runtime"`
	RuntimeEnv       map[string]string `json:"runtime_env"`
	InstanceID       string            `json:"instance_id"`
	LocalRef         string            `json:"local_ref"`
	DockerImage      string            `json:"docker_image"`
	EndpointInfo     json.RawMessage   `json:"endpoint_info"`
	SourceExternalID string            `json:"source_external_id"`
	CreatePayload    json.RawMessage   `json:"create_payload"`
	Code             string            `json:"code"`
	Language         string            `json:"language"`
	TimeoutSeconds   int               `json:"timeout_seconds"`
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
	f.String("docker-public-host", "", "Host/IP used in Docker sandbox service URLs (overrides config docker_public_host)")
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
	overrideStringFlag(cmd, "docker-public-host", &cfg.DockerPublicHost)
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
	// Resource listing can be slow. Keep it off the claim/heartbeat path so a
	// sluggish Cube or Docker call cannot starve liveness updates.
	go c.resourceRefreshLoop(ctx)
	// Heartbeat must not share a loop with claim: a hung claim (shared HTTP
	// client timeout is 120s for Cube jobs) previously delayed last_seen and
	// made healthy nodes flap offline in the UI.
	go c.heartbeatLoop(ctx)
	return c.pollLoop(ctx)
}

func (c *sandboxdClient) register(ctx context.Context) error {
	// Best-effort: don't block node registration on a slow Cube /templates.
	refreshCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	_ = c.refreshTemplates(refreshCtx, true)
	_ = c.refreshDockerImages(refreshCtx, true)
	cancel()
	return c.postJSON(ctx, "/api/sandbox/node/register", c.cfg.NodeToken, map[string]any{
		"node_key":        c.cfg.NodeKey,
		"name":            c.cfg.Name,
		"owner_user_id":   c.cfg.OwnerUserID,
		"max_concurrency": c.cfg.Concurrency,
		"capabilities":    []string{"create", "docker_create", "stop", "resume", "delete", "reconfigure", "clone", "create_template", "delete_template", "exec"},
		"metadata":        c.nodeMetadata(),
	}, nil)
}

func (c *sandboxdClient) pollLoop(ctx context.Context) error {
	ticker := time.NewTicker(c.cfg.PollInterval)
	defer ticker.Stop()
	for {
		// Bound claim so a stuck control-plane call cannot pin this loop.
		// Job work runs in goroutines with the shared long-timeout client.
		claimCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := c.claimAndRun(claimCtx)
		cancel()
		if err != nil && ctx.Err() == nil {
			c.logger.Warn("sandboxd claim failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (c *sandboxdClient) heartbeatLoop(ctx context.Context) {
	const interval = 10 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		hbCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := c.heartbeat(hbCtx)
		cancel()
		if err != nil && ctx.Err() == nil {
			c.logger.Warn("sandboxd heartbeat failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
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

func (c *sandboxdClient) resourceRefreshLoop(ctx context.Context) {
	_ = c.refreshTemplates(ctx, true)
	_ = c.refreshDockerImages(ctx, true)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = c.refreshTemplates(ctx, true)
			_ = c.refreshDockerImages(ctx, true)
		}
	}
}

func (c *sandboxdClient) nodeMetadata() map[string]any {
	c.templatesMu.Lock()
	templates := append([]cubeTemplateSummary(nil), c.cachedTemplates...)
	templatesSyncedAt := c.templatesFetchedAt
	c.templatesMu.Unlock()

	c.dockerImagesMu.Lock()
	dockerImages := append([]dockerImageSummary(nil), c.cachedDockerImages...)
	dockerImagesSyncedAt := c.dockerImagesFetchedAt
	dockerImagesError := c.dockerImagesError
	c.dockerImagesMu.Unlock()

	meta := map[string]any{
		"cube_api_url":     c.cfg.SandboxServer,
		"cube_proxy_http":  c.cfg.CubeProxyHTTP,
		"cube_domain":      c.cfg.CubeDomain,
		"cube_template_id": c.cfg.TemplateID,
		"templates":        templates,
		"docker_images":    dockerImages,
	}
	if !templatesSyncedAt.IsZero() {
		meta["templates_synced_at"] = templatesSyncedAt.UTC().Format(time.RFC3339)
	}
	if !dockerImagesSyncedAt.IsZero() {
		meta["docker_images_synced_at"] = dockerImagesSyncedAt.UTC().Format(time.RFC3339)
	}
	if dockerImagesError != "" {
		meta["docker_images_error"] = dockerImagesError
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

func (c *sandboxdClient) refreshDockerImages(ctx context.Context, force bool) error {
	const refreshInterval = 30 * time.Second
	c.dockerImagesMu.Lock()
	if !force && !c.dockerImagesFetchedAt.IsZero() && time.Since(c.dockerImagesFetchedAt) < refreshInterval {
		c.dockerImagesMu.Unlock()
		return nil
	}
	c.dockerImagesMu.Unlock()

	listCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	images, err := c.listDockerImages(listCtx)
	c.dockerImagesMu.Lock()
	defer c.dockerImagesMu.Unlock()
	if err != nil {
		c.dockerImagesError = err.Error()
		c.logger.Warn("list docker images failed", "error", err)
		return err
	}
	c.cachedDockerImages = images
	c.dockerImagesFetchedAt = time.Now()
	c.dockerImagesError = ""
	return nil
}

func dockerCommand(ctx context.Context, args ...string) *exec.Cmd {
	if path, err := exec.LookPath("docker"); err == nil {
		return exec.CommandContext(ctx, path, args...)
	}
	for _, path := range []string{"/usr/bin/docker", "/usr/local/bin/docker"} {
		if _, err := os.Stat(path); err == nil {
			return exec.CommandContext(ctx, path, args...)
		}
	}
	return exec.CommandContext(ctx, "docker", args...)
}

func (c *sandboxdClient) listDockerImages(ctx context.Context) ([]dockerImageSummary, error) {
	cmd := dockerCommand(ctx, "image", "ls", "--format", "{{json .}}")
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("docker image ls: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	images := make([]dockerImageSummary, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		repo := strings.TrimSpace(stringFromMap(raw, "Repository"))
		tag := strings.TrimSpace(stringFromMap(raw, "Tag"))
		if repo == "" || repo == "<none>" || tag == "" || tag == "<none>" {
			continue
		}
		images = append(images, dockerImageSummary{
			ImageRef:     repo + ":" + tag,
			Repository:   repo,
			Tag:          tag,
			ID:           stringFromMap(raw, "ID"),
			Digest:       stringFromMap(raw, "Digest"),
			CreatedAt:    stringFromMap(raw, "CreatedAt"),
			CreatedSince: stringFromMap(raw, "CreatedSince"),
			Size:         stringFromMap(raw, "Size"),
		})
	}
	return images, nil
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
	dockerMode := payload.DockerImage != "" || endpointKind(payload.EndpointInfo) == "docker"
	switch job.Type {
	case "create":
		if dockerMode {
			return c.createDockerContainer(ctx, job, payload)
		}
		return c.createCubeSandbox(ctx, job, payload)
	case "clone":
		return c.cloneCubeSandbox(ctx, job, payload)
	case "stop":
		if dockerMode {
			return c.dockerLifecycle(ctx, sandboxID, "stop")
		}
		return c.cubeLifecycle(ctx, sandboxID, "/pause", true)
	case "resume":
		if dockerMode {
			return c.resumeDockerContainer(ctx, sandboxID, payload)
		}
		return c.resumeCubeSandbox(ctx, sandboxID, payload)
	case "reconfigure":
		if dockerMode {
			return c.reconfigureDockerContainer(ctx, job, sandboxID, payload)
		}
		return c.reconfigureCubeSandbox(ctx, sandboxID, payload)
	case "delete":
		if dockerMode {
			return c.deleteDockerContainer(ctx, sandboxID)
		}
		return c.deleteCubeSandbox(ctx, sandboxID)
	case "create_template":
		return c.createCubeSnapshotTemplate(ctx, sandboxID, payload)
	case "delete_template":
		return c.deleteCubeSnapshotTemplate(ctx, payload)
	case "exec":
		return c.execCubeSandbox(ctx, sandboxID, payload)
	default:
		return nil, fmt.Errorf("unsupported sandbox job type %q", job.Type)
	}
}

func endpointKind(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil {
		return ""
	}
	return strings.TrimSpace(stringFromAny(obj["kind"]))
}

func dockerContainerName(instanceID string) string {
	clean := strings.NewReplacer("-", "").Replace(strings.TrimSpace(instanceID))
	if clean == "" {
		clean = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return "multica-" + clean
}

func (c *sandboxdClient) createDockerContainer(ctx context.Context, job sandboxJob, payload sandboxJobPayload) (map[string]any, error) {
	image := strings.TrimSpace(payload.DockerImage)
	if image == "" {
		return nil, fmt.Errorf("docker image is required")
	}
	runtimeEnv := mergeRuntimeEnv(payload.RuntimeEnv, payload.Runtime)
	if len(runtimeEnv) == 0 || runtimeEnvToken(runtimeEnv) == "" {
		return nil, fmt.Errorf("runtime_env missing MULTICA_TOKEN")
	}
	runtimeEnv["PATH"] = firstNonEmpty(runtimeEnv["PATH"], "/root/.local/bin:/root/.npm-global/bin:/root/.bun/bin:/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin")
	ensureDockerDesktopEnv(runtimeEnv)
	name := dockerContainerName(job.InstanceID)
	if existing, ok := c.findDockerContainerByInstance(ctx, job.InstanceID); ok {
		endpoint := c.dockerEndpointInfo(ctx, existing, name, image)
		return map[string]any{"local_ref": existing, "endpoint_info": endpoint, "result": map[string]any{"container_id": existing, "container_name": name, "image": image, "reused": true}}, nil
	}
	args := []string{
		"run", "-d",
		"--name", name,
		"--restart", "unless-stopped",
		"--shm-size", "2g",
		"--label", "multica.sandbox_instance_id=" + job.InstanceID,
		"--label", "multica.workspace_id=" + job.WorkspaceID,
		// Dynamic host ports so multiple containers on one node do not collide.
		"-p", "0:6079",
		"-p", "0:6080",
		"--entrypoint", "/bin/sh",
	}
	for k, v := range runtimeEnv {
		if strings.TrimSpace(k) == "" {
			continue
		}
		args = append(args, "--env", k+"="+v)
	}
	args = append(args, image, "-lc", dockerRuntimeEntrypointScript())
	cmd := dockerCommand(ctx, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker run: %s", strings.TrimSpace(string(out)))
	}
	containerID := strings.TrimSpace(string(out))
	if containerID == "" {
		return nil, fmt.Errorf("docker run returned empty container id")
	}
	endpoint := c.dockerEndpointInfo(ctx, containerID, name, image)
	return map[string]any{
		"local_ref":     containerID,
		"endpoint_info": endpoint,
		"result": map[string]any{
			"container_id":   containerID,
			"container_name": name,
			"image":          image,
			"runtime_env":    redactedRuntimeEnv(runtimeEnv),
			"instance_id":    job.InstanceID,
			"workspace_id":   job.WorkspaceID,
			"endpoint_info":  endpoint,
		},
	}, nil
}

func dockerRuntimeEntrypointScript() string {
	// Start VNC/noVNC + Pi web (incl. /term) before the Multica daemon, then
	// keep PID 1 alive so reconfigure can restart the daemon in-place via
	// docker exec. Docker container env is immutable after create; updated
	// Pi/model config is applied by restartRuntimeInDocker, not docker rm/run.
	return `/etc/cont-init.d/99-browser-vnc || true
/usr/local/bin/start-multica-runtime.sh
exec tail -f /dev/null`
}

func ensureDockerDesktopEnv(runtimeEnv map[string]string) {
	if runtimeEnv == nil {
		return
	}
	if strings.TrimSpace(runtimeEnv["DISPLAY"]) == "" {
		runtimeEnv["DISPLAY"] = ":0"
	}
	if strings.TrimSpace(runtimeEnv["PI_WEB_HOST"]) == "" {
		runtimeEnv["PI_WEB_HOST"] = "0.0.0.0"
	}
	if strings.TrimSpace(runtimeEnv["PI_WEB_PORT"]) == "" {
		runtimeEnv["PI_WEB_PORT"] = "6079"
	}
	if strings.TrimSpace(runtimeEnv["PI_WEB_WORKSPACE"]) == "" {
		runtimeEnv["PI_WEB_WORKSPACE"] = "/workspace"
	}
	if strings.TrimSpace(runtimeEnv["NOVNC_PORT"]) == "" {
		runtimeEnv["NOVNC_PORT"] = "6080"
	}
	if strings.TrimSpace(runtimeEnv["VNC_PORT"]) == "" {
		runtimeEnv["VNC_PORT"] = "5901"
	}
}

func (c *sandboxdClient) dockerEndpointInfo(ctx context.Context, containerID, name, image string) map[string]any {
	publicHost := resolveDockerPublicHost(c.cfg.DockerPublicHost)
	ports := c.inspectDockerPublishedPorts(ctx, containerID, "6079", "6080")
	return buildDockerEndpointInfo(containerID, name, image, publicHost, ports)
}

func buildDockerEndpointInfo(containerID, name, image, publicHost string, ports map[string]string) map[string]any {
	endpoint := map[string]any{
		"kind":           "docker",
		"container_id":   containerID,
		"container_name": name,
		"image":          image,
	}
	if publicHost != "" {
		endpoint["public_host"] = publicHost
	}
	if hostPort := strings.TrimSpace(ports["6079"]); hostPort != "" {
		endpoint["pi_web_port"] = hostPort
		if publicHost != "" {
			base := fmt.Sprintf("http://%s:%s", publicHost, hostPort)
			endpoint["pi_web_url"] = base + "/"
			endpoint["term_url"] = base + "/term"
		}
	}
	if hostPort := strings.TrimSpace(ports["6080"]); hostPort != "" {
		endpoint["novnc_port"] = hostPort
		if publicHost != "" {
			endpoint["novnc_url"] = fmt.Sprintf("http://%s:%s/", publicHost, hostPort)
		}
	}
	return endpoint
}

func (c *sandboxdClient) inspectDockerPublishedPorts(ctx context.Context, containerID string, containerPorts ...string) map[string]string {
	out := make(map[string]string, len(containerPorts))
	for _, containerPort := range containerPorts {
		cmd := dockerCommand(ctx, "port", containerID, containerPort)
		raw, err := cmd.CombinedOutput()
		if err != nil {
			continue
		}
		if hostPort := parseDockerPublishedPort(string(raw)); hostPort != "" {
			out[containerPort] = hostPort
		}
	}
	return out
}

// parseDockerPublishedPort extracts the host port from `docker port` output.
// Examples: "0.0.0.0:32768", "[::]:32768".
func parseDockerPublishedPort(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if idx := strings.LastIndex(line, "]:"); idx >= 0 {
				port := strings.TrimSpace(line[idx+2:])
				if port != "" {
					return port
				}
			}
			continue
		}
		if idx := strings.LastIndex(line, ":"); idx >= 0 {
			port := strings.TrimSpace(line[idx+1:])
			if port != "" {
				return port
			}
		}
	}
	return ""
}

func resolveDockerPublicHost(configured string) string {
	if host := strings.TrimSpace(configured); host != "" {
		return host
	}
	if host := strings.TrimSpace(os.Getenv("SANDBOXD_DOCKER_PUBLIC_HOST")); host != "" {
		return host
	}
	if host := primaryNonLoopbackIPv4(); host != "" {
		return host
	}
	return "127.0.0.1"
}

func primaryNonLoopbackIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP == nil || ipNet.IP.IsLoopback() {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil {
			continue
		}
		return ip.String()
	}
	return ""
}

func (c *sandboxdClient) findDockerContainerByInstance(ctx context.Context, instanceID string) (string, bool) {
	if strings.TrimSpace(instanceID) == "" {
		return "", false
	}
	cmd := dockerCommand(ctx, "ps", "-aq", "--filter", "label=multica.sandbox_instance_id="+instanceID)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return "", false
	}
	return ids[0], true
}

func (c *sandboxdClient) dockerLifecycle(ctx context.Context, containerID, action string) (map[string]any, error) {
	if strings.TrimSpace(containerID) == "" {
		return nil, fmt.Errorf("docker container id is required")
	}
	cmd := dockerCommand(ctx, action, containerID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker %s: %s", action, strings.TrimSpace(string(out)))
	}
	return map[string]any{"local_ref": containerID, "result": map[string]any{"action": action, "container_id": containerID}, "endpoint_info": nil}, nil
}

func (c *sandboxdClient) dockerExec(ctx context.Context, containerID string, args ...string) (string, error) {
	if strings.TrimSpace(containerID) == "" {
		return "", fmt.Errorf("docker container id is required")
	}
	cmdArgs := append([]string{"exec", containerID}, args...)
	cmd := dockerCommand(ctx, cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker exec: %s", strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// dockerEntrypointKeepaliveCmdline matches the legacy entrypoint pgrep
// (`multica .*daemon start`) so older containers that still exit when the
// real daemon dies stay alive during in-place reconfigure. It must NOT match
// `pkill -f 'multica daemon'` used by buildStartRuntimeInCubeCode.
const dockerEntrypointKeepaliveCmdline = "multica keepAlive daemon start"

func (c *sandboxdClient) startDockerEntrypointKeepalive(ctx context.Context, containerID string) {
	script := "pkill -f 'multica keepAlive daemon start' >/dev/null 2>&1 || true; " +
		"exec -a '" + dockerEntrypointKeepaliveCmdline + "' sleep 7200"
	cmd := dockerCommand(ctx, "exec", "-d", containerID, "bash", "-lc", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		c.logger.Warn("docker entrypoint keepalive start failed", "container_id", containerID, "error", strings.TrimSpace(string(out)))
	}
}

func (c *sandboxdClient) stopDockerEntrypointKeepalive(ctx context.Context, containerID string) {
	_, _ = c.dockerExec(ctx, containerID, "bash", "-lc", "pkill -f 'multica keepAlive daemon start' >/dev/null 2>&1 || true")
}

func (c *sandboxdClient) restartRuntimeInDocker(ctx context.Context, containerID string, runtimeEnv map[string]string) error {
	if len(runtimeEnv) == 0 || runtimeEnvToken(runtimeEnv) == "" {
		return fmt.Errorf("runtime_env missing MULTICA_TOKEN")
	}
	code := buildStartRuntimeInCubeCode(runtimeEnv)
	if _, err := c.dockerExec(ctx, containerID, "python3", "-c", code); err == nil {
		return nil
	} else if out, err2 := c.dockerExec(ctx, containerID, "python", "-c", code); err2 != nil {
		msg := strings.TrimSpace(out)
		if msg == "" {
			msg = err2.Error()
		}
		return fmt.Errorf("restart runtime in docker: %s", msg)
	}
	return nil
}

func (c *sandboxdClient) resumeDockerContainer(ctx context.Context, containerID string, payload sandboxJobPayload) (map[string]any, error) {
	result, err := c.dockerLifecycle(ctx, containerID, "start")
	if err != nil {
		return nil, err
	}
	runtimeEnv := mergeRuntimeEnv(payload.RuntimeEnv, payload.Runtime)
	if len(runtimeEnv) > 0 && runtimeEnvToken(runtimeEnv) != "" && hasRuntimeModelConfig(payload.Runtime) {
		c.startDockerEntrypointKeepalive(ctx, containerID)
		defer c.stopDockerEntrypointKeepalive(ctx, containerID)
		if err := c.restartRuntimeInDocker(ctx, containerID, runtimeEnv); err != nil {
			return nil, err
		}
		result["result"] = map[string]any{"resumed": true, "runtime_restarted": true, "container_id": containerID}
	}
	return result, nil
}

func (c *sandboxdClient) reconfigureDockerContainer(ctx context.Context, job sandboxJob, containerID string, payload sandboxJobPayload) (map[string]any, error) {
	id := strings.TrimSpace(containerID)
	if id == "" {
		if found, ok := c.findDockerContainerByInstance(ctx, job.InstanceID); ok {
			id = found
		}
	}
	if id == "" {
		return nil, fmt.Errorf("docker container id is required")
	}
	if _, err := c.dockerLifecycle(ctx, id, "start"); err != nil {
		return nil, err
	}
	runtimeEnv := mergeRuntimeEnv(payload.RuntimeEnv, payload.Runtime)
	if len(runtimeEnv) == 0 || runtimeEnvToken(runtimeEnv) == "" {
		return nil, fmt.Errorf("runtime_env missing MULTICA_TOKEN")
	}
	// Keep legacy entrypoint watchers satisfied while the real daemon is swapped.
	c.startDockerEntrypointKeepalive(ctx, id)
	defer c.stopDockerEntrypointKeepalive(ctx, id)
	if err := c.restartRuntimeInDocker(ctx, id, runtimeEnv); err != nil {
		return nil, err
	}
	image := strings.TrimSpace(payload.DockerImage)
	if image == "" {
		image = stringFromRawObject(payload.EndpointInfo, "image")
	}
	name := firstNonEmpty(stringFromRawObject(payload.EndpointInfo, "container_name"), dockerContainerName(job.InstanceID))
	endpoint := c.dockerEndpointInfo(ctx, id, name, image)
	return map[string]any{
		"local_ref":     id,
		"endpoint_info": endpoint,
		"result": map[string]any{
			"reconfigured":   true,
			"container_id":   id,
			"container_name": name,
			"image":          image,
			"runtime_env":    redactedRuntimeEnv(runtimeEnv),
		},
	}, nil
}

func (c *sandboxdClient) deleteDockerContainer(ctx context.Context, containerID string) (map[string]any, error) {
	if strings.TrimSpace(containerID) == "" {
		return map[string]any{"result": map[string]any{"deleted": true, "idempotent": true}}, nil
	}
	cmd := dockerCommand(ctx, "rm", "-f", containerID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(strings.ToLower(msg), "no such container") {
			return map[string]any{"local_ref": containerID, "result": map[string]any{"deleted": true, "idempotent": true}}, nil
		}
		return nil, fmt.Errorf("docker rm: %s", msg)
	}
	return map[string]any{"local_ref": containerID, "result": map[string]any{"deleted": true, "container_id": containerID}}, nil
}

func (c *sandboxdClient) cloneCubeSandbox(ctx context.Context, job sandboxJob, payload sandboxJobPayload) (map[string]any, error) {
	if payload.SourceExternalID == "" {
		return nil, fmt.Errorf("clone job missing source_external_id")
	}
	var snapshot map[string]any
	if err := c.cubeJSON(ctx, http.MethodPost, "/sandboxes/"+url.PathEscape(payload.SourceExternalID)+"/snapshots", map[string]any{}, "", &snapshot); err != nil {
		return nil, err
	}
	snapshotID := firstNonEmpty(stringAny(snapshot["templateID"]), stringAny(snapshot["id"]), stringAny(snapshot["snapshotID"]))
	if snapshotID == "" {
		return nil, fmt.Errorf("cube snapshot response missing template id")
	}
	defer func() {
		_ = c.cubeJSON(context.WithoutCancel(ctx), http.MethodDelete, "/templates/"+url.PathEscape(snapshotID), nil, "", nil)
	}()
	create := parseSandboxJobPayload(payload.CreatePayload)
	create.Template = snapshotID
	return c.createCubeSandbox(ctx, job, create)
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
		"pi_web_url": fmt.Sprintf("http://6079-%s.%s/", cube.SandboxID, c.cfg.CubeDomain),
		"term_url":   fmt.Sprintf("http://6079-%s.%s/term", cube.SandboxID, c.cfg.CubeDomain),
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
	if len(runtimeEnv) > 0 && runtimeEnvToken(runtimeEnv) != "" && hasRuntimeModelConfig(payload.Runtime) {
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
func (c *sandboxdClient) execCubeSandbox(ctx context.Context, sandboxID string, payload sandboxJobPayload) (map[string]any, error) {
	if strings.TrimSpace(sandboxID) == "" {
		return nil, fmt.Errorf("exec job missing local_ref")
	}
	if payload.Language != "python" {
		return nil, fmt.Errorf("exec job language must be python")
	}
	if len(payload.Code) == 0 || len(payload.Code) > 32<<10 {
		return nil, fmt.Errorf("exec job code must be 1..32768 bytes")
	}
	timeout := time.Duration(payload.TimeoutSeconds) * time.Second
	if timeout <= 0 || timeout > 5*time.Minute {
		return nil, fmt.Errorf("exec job timeout_seconds must be in [1, 300]")
	}
	var result map[string]any
	if err := c.cubeJSONWithTimeout(ctx, timeout, http.MethodPost, "/execute", map[string]any{"code": payload.Code, "language": payload.Language}, fmt.Sprintf("49999-%s.%s", sandboxID, c.cfg.CubeDomain), &result); err != nil {
		return nil, err
	}
	return map[string]any{"local_ref": sandboxID, "result": result}, nil
}

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
	if runtimeEnvToken(runtimeEnv) == "" {
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

func hasRuntimeModelConfig(runtime json.RawMessage) bool {
	if len(runtime) == 0 || string(runtime) == "null" {
		return false
	}
	var raw map[string]any
	if json.Unmarshal(runtime, &raw) != nil || len(raw) == 0 {
		return false
	}
	if providers, ok := raw["providers"].([]any); ok && len(providers) > 0 {
		return true
	}
	for _, key := range []string{"api_key", "base_url", "model", "provider", "TEAM_API_KEY", "TEAM_BASE_URL", "TEAM_MODEL", "TEAM_PROVIDER"} {
		if s, ok := raw[key].(string); ok && strings.TrimSpace(s) != "" {
			return true
		}
	}
	return false
}

type teamPiProviderConfig struct {
	Name    string `json:"name"`
	APIKey  string `json:"apiKey,omitempty"`
	BaseURL string `json:"baseUrl,omitempty"`
	Model   string `json:"model,omitempty"`
}

type teamPiConfig struct {
	Providers       []teamPiProviderConfig `json:"providers"`
	DefaultProvider string                 `json:"defaultProvider,omitempty"`
	DefaultModel    string                 `json:"defaultModel,omitempty"`
}

func stringFromAny(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func defaultPiModel(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return "gpt-5.5"
	case "anthropic":
		return "claude-sonnet-4.6"
	case "google":
		return "gemini-3.1-pro"
	default:
		return ""
	}
}

func parseTeamPiConfig(runtime json.RawMessage) (teamPiConfig, bool) {
	var cfg teamPiConfig
	if len(runtime) == 0 || string(runtime) == "null" {
		return cfg, false
	}
	var raw map[string]any
	if json.Unmarshal(runtime, &raw) != nil || len(raw) == 0 {
		return cfg, false
	}

	if providers, ok := raw["providers"].([]any); ok {
		for _, item := range providers {
			rec, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name := stringFromAny(rec["provider"])
			if name == "" {
				name = stringFromAny(rec["name"])
			}
			if name == "" {
				name = "openai"
			}
			p := teamPiProviderConfig{
				Name:    name,
				APIKey:  stringFromAny(rec["api_key"]),
				BaseURL: stringFromAny(rec["base_url"]),
				Model:   stringFromAny(rec["model"]),
			}
			if p.APIKey == "" {
				p.APIKey = stringFromAny(rec["apiKey"])
			}
			if p.BaseURL == "" {
				p.BaseURL = stringFromAny(rec["baseUrl"])
			}
			if p.APIKey == "" && p.BaseURL == "" && p.Model == "" && stringFromAny(rec["provider"]) == "" && stringFromAny(rec["name"]) == "" {
				continue
			}
			cfg.Providers = append(cfg.Providers, p)
		}
	}

	if len(cfg.Providers) == 0 {
		legacy := teamPiProviderConfig{
			Name:    stringFromAny(raw["provider"]),
			APIKey:  firstNonEmpty(stringFromAny(raw["api_key"]), stringFromAny(raw["TEAM_API_KEY"]), stringFromAny(raw["team_api_key"])),
			BaseURL: firstNonEmpty(stringFromAny(raw["base_url"]), stringFromAny(raw["TEAM_BASE_URL"]), stringFromAny(raw["team_base_url"])),
			Model:   firstNonEmpty(stringFromAny(raw["model"]), stringFromAny(raw["TEAM_MODEL"]), stringFromAny(raw["team_model"])),
		}
		if legacy.Name == "" {
			legacy.Name = firstNonEmpty(stringFromAny(raw["TEAM_PROVIDER"]), "openai")
		}
		if legacy.APIKey != "" || legacy.BaseURL != "" || legacy.Model != "" {
			cfg.Providers = append(cfg.Providers, legacy)
		}
	}

	if len(cfg.Providers) == 0 {
		return cfg, false
	}

	cfg.DefaultProvider = firstNonEmpty(stringFromAny(raw["default_provider"]), stringFromAny(raw["provider"]), cfg.Providers[0].Name)
	cfg.DefaultModel = firstNonEmpty(stringFromAny(raw["default_model"]), stringFromAny(raw["model"]))
	if cfg.DefaultModel == "" {
		for _, p := range cfg.Providers {
			if p.Name == cfg.DefaultProvider && p.Model != "" {
				cfg.DefaultModel = p.Model
				break
			}
		}
	}
	if cfg.DefaultModel == "" {
		cfg.DefaultModel = cfg.Providers[0].Model
	}
	if cfg.DefaultModel == "" {
		cfg.DefaultModel = defaultPiModel(cfg.DefaultProvider)
	}
	if cfg.DefaultModel != "" {
		for i := range cfg.Providers {
			if cfg.Providers[i].Name == cfg.DefaultProvider && cfg.Providers[i].Model == "" {
				cfg.Providers[i].Model = cfg.DefaultModel
				break
			}
		}
	}
	return cfg, true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func mergeRuntimeEnv(base map[string]string, runtime json.RawMessage) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		if strings.TrimSpace(v) != "" {
			out[k] = v
		}
	}

	cfg, ok := parseTeamPiConfig(runtime)
	if ok {
		raw, err := json.Marshal(cfg)
		if err == nil {
			out["TEAM_PI_CONFIG"] = string(raw)
		}
		var def *teamPiProviderConfig
		for i := range cfg.Providers {
			if cfg.Providers[i].Name == cfg.DefaultProvider {
				def = &cfg.Providers[i]
				break
			}
		}
		if def == nil {
			def = &cfg.Providers[0]
		}
		if def.APIKey != "" {
			out["TEAM_API_KEY"] = def.APIKey
		}
		if def.BaseURL != "" {
			out["TEAM_BASE_URL"] = def.BaseURL
		}
		if cfg.DefaultModel != "" {
			out["TEAM_MODEL"] = cfg.DefaultModel
		} else if def.Model != "" {
			out["TEAM_MODEL"] = def.Model
		}
		if cfg.DefaultProvider != "" {
			out["TEAM_PROVIDER"] = cfg.DefaultProvider
		} else if def.Name != "" {
			out["TEAM_PROVIDER"] = def.Name
		}
	} else if len(runtime) > 0 && string(runtime) != "null" {
		// Flat string map fallback (older job payloads).
		var flat map[string]string
		if json.Unmarshal(runtime, &flat) == nil {
			aliases := map[string][]string{
				"TEAM_API_KEY":  {"TEAM_API_KEY", "team_api_key", "api_key"},
				"TEAM_BASE_URL": {"TEAM_BASE_URL", "team_base_url", "base_url"},
				"TEAM_MODEL":    {"TEAM_MODEL", "team_model", "model"},
				"TEAM_PROVIDER": {"TEAM_PROVIDER", "team_provider", "provider"},
			}
			for target, keys := range aliases {
				for _, key := range keys {
					if v := strings.TrimSpace(flat[key]); v != "" {
						out[target] = v
						break
					}
				}
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
		lower := strings.ToLower(k)
		if k == "TEAM_PI_CONFIG" {
			if v != "" {
				out[k] = "***"
			}
			continue
		}
		if strings.Contains(lower, "token") || strings.Contains(lower, "key") {
			if v != "" {
				out[k] = "***"
			}
			continue
		}
		out[k] = v
	}
	return out
}

func (c *sandboxdClient) stopRuntimeInCube(ctx context.Context, sandboxID string) error {
	// Use the [m]ultica trick so `python -c` /execute payloads that embed this
	// snippet cannot match and kill themselves via pkill -f.
	code := `import subprocess, time
subprocess.run(["bash", "-lc", "pkill -f '[m]ultica daemon' || pkill -f '[m]ultica-daemon' || true"], check=False)
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
	if len(runtimeEnv) == 0 || runtimeEnvToken(runtimeEnv) == "" {
		return fmt.Errorf("runtime_env missing MULTICA_TOKEN")
	}
	code := buildStartRuntimeInCubeCode(runtimeEnv)
	return c.cubeJSON(ctx, http.MethodPost, "/execute", map[string]any{"code": code, "language": "python"}, fmt.Sprintf("49999-%s.%s", sandboxID, c.cfg.CubeDomain), nil)
}

// buildStartRuntimeInCubeCode stops any snapshot-restored daemon, rewrites
// Multica identity files to the minted MULTICA_DAEMON_ID, then starts the
// runtime. Snapshot templates freeze ~/.multica/daemon.id (and possibly a live
// daemon in memory); without this reset the restored process can briefly
// re-register as the source sandbox's runtime before the new env takes effect,
// and leftover profile-scoped daemon.id files can trigger legacy runtime merge
// that steals the source row.
func buildStartRuntimeInCubeCode(runtimeEnv map[string]string) string {
	// pkill patterns use the [m]ultica trick: a `python3 -c '…pkill…'` process
	// embeds this source in argv, so a literal `multica daemon` pattern would
	// match and SIGTERM itself (exit 143) before configure-pi can write
	// ~/.pi/agent/models.json — which is exactly how Docker reconfigure failed.
	return fmt.Sprintf(`import json, os, pathlib, subprocess, time
runtime_env = json.loads(%q)
subprocess.run(["bash", "-lc", "pkill -f '[m]ultica daemon' || pkill -f '[m]ultica-daemon' || true"], check=False)
time.sleep(1)
daemon_id = (runtime_env.get("MULTICA_DAEMON_ID") or "").strip()
multica = pathlib.Path.home() / ".multica"
multica.mkdir(parents=True, exist_ok=True)
daemon_file = multica / "daemon.id"
if daemon_id:
    daemon_file.write_text(daemon_id + "\n")
elif daemon_file.exists():
    daemon_file.unlink()
profiles = multica / "profiles"
if profiles.is_dir():
    for path in profiles.glob("*/daemon.id"):
        try:
            if daemon_id:
                path.write_text(daemon_id + "\n")
            else:
                path.unlink()
        except OSError:
            pass
env = os.environ.copy()
env.update(runtime_env)
env["PATH"] = "/home/user/.npm-global/bin:/home/user/.bun/bin:/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin"
proc = subprocess.run(["bash", "-lc", "/usr/local/bin/start-multica-runtime.sh"], text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=60, env=env)
print(proc.stdout)
if proc.returncode != 0:
    raise SystemExit(proc.returncode)
`, mustJSON(runtimeEnv))
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

func stringAny(v any) string {
	s, _ := v.(string)
	return s
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
