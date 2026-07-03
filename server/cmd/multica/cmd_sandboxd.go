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
	cfg    sandboxdConfig
	http   *http.Client
	logger *slog.Logger
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
	cfg, err := loadSandboxdConfig(flagString(cmd, "config"))
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
	c := &sandboxdClient{cfg: cfg, http: &http.Client{Timeout: 120 * time.Second}, logger: slog.Default()}
	if err := c.register(ctx); err != nil {
		return err
	}
	go c.wsLoop(ctx)
	return c.pollLoop(ctx)
}

func (c *sandboxdClient) register(ctx context.Context) error {
	body := map[string]any{
		"node_key":        c.cfg.NodeKey,
		"name":            c.cfg.Name,
		"owner_user_id":   c.cfg.OwnerUserID,
		"max_concurrency": c.cfg.Concurrency,
		"capabilities":    []string{"create", "stop", "resume", "delete"},
		"metadata": map[string]any{
			"cube_api_url":     c.cfg.SandboxServer,
			"cube_proxy_http":  c.cfg.CubeProxyHTTP,
			"cube_domain":      c.cfg.CubeDomain,
			"cube_template_id": c.cfg.TemplateID,
		},
	}
	return c.postJSON(ctx, "/api/sandbox/node/register", c.cfg.NodeToken, body, nil)
}

func (c *sandboxdClient) pollLoop(ctx context.Context) error {
	ticker := time.NewTicker(c.cfg.PollInterval)
	defer ticker.Stop()
	for {
		if err := c.claimAndRun(ctx); err != nil && ctx.Err() == nil {
			c.logger.Warn("sandboxd claim failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
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
		return c.cubeLifecycle(ctx, sandboxID, "/resume", false)
	case "delete":
		return c.deleteCubeSandbox(ctx, sandboxID)
	default:
		return nil, fmt.Errorf("unsupported sandbox job type %q", job.Type)
	}
}

func (c *sandboxdClient) createCubeSandbox(ctx context.Context, job sandboxJob, payload sandboxJobPayload) (map[string]any, error) {
	templateID := payload.Template
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
	res, err := c.http.Do(req)
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

func loadSandboxdConfig(path string) (sandboxdConfig, error) {
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
			return sandboxdConfig{}, err
		}
		var cfg sandboxdConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return sandboxdConfig{}, fmt.Errorf("parse sandboxd config %s: %w", candidate, err)
		}
		return cfg, nil
	}
	return sandboxdConfig{}, fmt.Errorf("sandboxd config not found; create .multica/sandboxd.json or pass --config")
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
