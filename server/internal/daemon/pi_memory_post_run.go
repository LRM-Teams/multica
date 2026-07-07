package daemon

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

const (
	piMemoryPostRunModeAuto   = "auto"
	piMemoryPostRunModeOff    = "off"
	piMemoryPostRunModeAlways = "always"

	piMemoryPostRunIncludeModelAuto = "auto"
	piMemoryPostRunIncludeModelOff  = "0"
	piMemoryPostRunIncludeModelOn   = "1"
)

type piMemoryPostRunDecision struct {
	Run          bool
	IncludeModel bool
	Reason       string
}

func piMemoryPostRunModeFromEnv(name, fallback string) (string, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		v = fallback
	}
	v = strings.ToLower(v)
	switch v {
	case piMemoryPostRunModeAuto, piMemoryPostRunModeOff, piMemoryPostRunModeAlways:
		return v, nil
	default:
		return "", fmt.Errorf("%s must be auto, off, or always", name)
	}
}

func piMemoryPostRunIncludeModelFromEnv(name, fallback string) (string, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		v = fallback
	}
	v = strings.ToLower(v)
	switch v {
	case piMemoryPostRunIncludeModelAuto, piMemoryPostRunIncludeModelOff, piMemoryPostRunIncludeModelOn:
		return v, nil
	case "false", "off", "no":
		return piMemoryPostRunIncludeModelOff, nil
	case "true", "on", "yes":
		return piMemoryPostRunIncludeModelOn, nil
	default:
		return "", fmt.Errorf("%s must be auto, 0, or 1", name)
	}
}

func shouldRunPiMemoryPostRun(task Task, result TaskResult, provider string, tools int32, cfg Config) piMemoryPostRunDecision {
	if strings.ToLower(strings.TrimSpace(provider)) != "pi" {
		return piMemoryPostRunDecision{Reason: "non_pi_provider"}
	}
	mode := piMemoryPostRunMode(cfg)
	if mode == piMemoryPostRunModeOff {
		return piMemoryPostRunDecision{Reason: "disabled"}
	}
	if strings.TrimSpace(result.SessionID) == "" {
		return piMemoryPostRunDecision{Reason: "no_session"}
	}
	if mode == piMemoryPostRunModeAlways {
		return piMemoryPostRunDecision{Run: true, IncludeModel: piMemoryPostRunShouldIncludeModel(task, result, cfg), Reason: "always"}
	}

	status := strings.ToLower(strings.TrimSpace(result.Status))
	if (status == "cancelled" || status == "aborted") && tools == 0 {
		return piMemoryPostRunDecision{Reason: "cancelled_no_tools"}
	}
	if status != "completed" && isPiMemoryProviderFailure(result) {
		return piMemoryPostRunDecision{Reason: "provider_failure"}
	}

	outputLen := len(result.Comment)
	minTools := piMemoryPostRunMinTools(cfg)
	if minTools > 0 && int(tools) >= minTools {
		return piMemoryPostRunDecision{Run: true, IncludeModel: piMemoryPostRunShouldIncludeModel(task, result, cfg), Reason: "tools"}
	}
	if outputLen >= piMemoryPostRunMinOutputChars(cfg) {
		return piMemoryPostRunDecision{Run: true, IncludeModel: piMemoryPostRunShouldIncludeModel(task, result, cfg), Reason: "long_output"}
	}
	if containsPiMemoryLearningKeyword(piMemoryPostRunText(task, result)) {
		return piMemoryPostRunDecision{Run: true, IncludeModel: piMemoryPostRunShouldIncludeModel(task, result, cfg), Reason: "learning_keyword"}
	}
	if tools == 0 && outputLen < piMemoryPostRunShortOutputChars(cfg) {
		return piMemoryPostRunDecision{Reason: "short_no_tools"}
	}
	return piMemoryPostRunDecision{Reason: "no_learning_signal"}
}

func (d *Daemon) maybeStartPiMemoryPostRun(task Task, result TaskResult, rt Runtime, log *slog.Logger) {
	decision := shouldRunPiMemoryPostRun(task, result, rt.Provider, result.ToolCount, d.cfg)
	if !decision.Run {
		log.Debug("pi memory post-run: skipped", "reason", decision.Reason)
		return
	}

	ctx := d.rootCtx
	if ctx == nil {
		ctx = context.Background()
	}
	go d.runPiMemoryPostRun(ctx, task, result, rt, decision, log)
}

func (d *Daemon) runPiMemoryPostRun(parent context.Context, task Task, result TaskResult, rt Runtime, decision piMemoryPostRunDecision, log *slog.Logger) {
	workspaceID := strings.TrimSpace(task.WorkspaceID)
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(rt.WorkspaceID)
	}
	agentID := strings.TrimSpace(resolvedTaskAgentID(task))
	sessionFile := strings.TrimSpace(result.SessionID)
	if workspaceID == "" || agentID == "" {
		log.Debug("pi memory post-run: skipped", "reason", "missing_scope", "workspace_id", workspaceID != "", "agent_id", agentID != "")
		return
	}
	if info, err := os.Stat(sessionFile); err != nil {
		log.Debug("pi memory post-run: skipped", "reason", "session_missing", "session", sessionFile, "error", err)
		return
	} else if info.IsDir() {
		log.Debug("pi memory post-run: skipped", "reason", "session_is_dir", "session", sessionFile)
		return
	}

	agentRoot := piAgentRoot(d.cfg, workspaceID, agentID)
	if err := ensurePiAgentRoot(agentRoot); err != nil {
		log.Warn("pi memory post-run: failed", "error", fmt.Errorf("ensure agent root: %w", err))
		return
	}

	args := []string{
		"session-history-backfill", sessionFile,
		"--memory-dir", filepath.Join(agentRoot, "memory"),
		"--limit", "1",
		"--force",
		"--json",
	}
	if decision.IncludeModel {
		args = append(args, "--include-model")
	}
	env := map[string]string{
		"PI_AGENT_ROOT":           agentRoot,
		"PI_MEMORY_DIR":           filepath.Join(agentRoot, "memory"),
		"PI_SKILL_DRAFTS_DIR":     piAgentSkillDraftsDir(agentRoot),
		"PI_AGENT_SYNC_QUEUE_DIR": piAgentSyncQueueDir(agentRoot),
		"MULTICA_SERVER_URL":      d.cfg.ServerBaseURL,
		"MULTICA_WORKSPACE_ID":    workspaceID,
		"MULTICA_AGENT_ID":        agentID,
		"MULTICA_TASK_ID":         task.ID,
		"MULTICA_RUN_ID":          task.ID,
		"MULTICA_WORKSPACES_ROOT": d.cfg.WorkspacesRoot,
	}

	ctx, cancel := context.WithTimeout(parent, piMemoryPostRunTimeout(d.cfg))
	defer cancel()
	log.Info("pi memory post-run: starting",
		"provider", rt.Provider,
		"task_id", task.ID,
		"agent_id", agentID,
		"reason", decision.Reason,
		"include_model", decision.IncludeModel,
		"session", sessionFile,
	)

	runner := d.piMemoryPostRunExec
	if runner == nil {
		runner = defaultPiMemoryPostRunExec
	}
	out, err := runner(ctx, "jhp-pi-memory-curator", args, env)
	if err != nil {
		log.Warn("pi memory post-run: failed", "error", err, "output", tailString(string(out), 4096))
		return
	}
	if len(bytes.TrimSpace(out)) > 0 {
		log.Debug("pi memory post-run: completed", "output", tailString(string(out), 4096))
	} else {
		log.Debug("pi memory post-run: completed")
	}

	syncFn := d.piMemoryPostRunSync
	if syncFn == nil {
		syncFn = d.syncEvolutionSubmissionsForRuntime
	}
	if err := syncFn(ctx, rt); err != nil {
		log.Warn("pi memory post-run: evolution sync failed", "error", err)
		return
	}
	log.Info("pi memory post-run: evolution sync completed", "runtime_id", rt.ID, "workspace_id", rt.WorkspaceID)
}

func defaultPiMemoryPostRunExec(ctx context.Context, name string, args []string, env map[string]string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = mergeEnvMap(os.Environ(), env)
	return cmd.CombinedOutput()
}

func mergeEnvMap(base []string, overrides map[string]string) []string {
	out := make([]string, 0, len(base)+len(overrides))
	seen := make(map[string]struct{}, len(overrides))
	for key := range overrides {
		seen[key] = struct{}{}
	}
	for _, kv := range base {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if _, ok := seen[key]; ok {
			continue
		}
		out = append(out, kv)
	}
	for key, value := range overrides {
		out = append(out, key+"="+value)
	}
	return out
}

func piMemoryPostRunMode(cfg Config) string {
	if cfg.PiMemoryPostRun == "" {
		return DefaultPiMemoryPostRun
	}
	return strings.ToLower(cfg.PiMemoryPostRun)
}

func piMemoryPostRunIncludeModelMode(cfg Config) string {
	if cfg.PiMemoryPostRunIncludeModel == "" {
		return DefaultPiMemoryPostRunIncludeModel
	}
	return strings.ToLower(cfg.PiMemoryPostRunIncludeModel)
}

func piMemoryPostRunMinOutputChars(cfg Config) int {
	if cfg.PiMemoryPostRunMinOutputChars <= 0 {
		return DefaultPiMemoryPostRunMinOutputChars
	}
	return cfg.PiMemoryPostRunMinOutputChars
}

func piMemoryPostRunShortOutputChars(cfg Config) int {
	if cfg.PiMemoryPostRunShortOutputChars <= 0 {
		return DefaultPiMemoryPostRunShortOutputChars
	}
	return cfg.PiMemoryPostRunShortOutputChars
}

func piMemoryPostRunMinTools(cfg Config) int {
	if cfg.PiMemoryPostRunMinTools <= 0 {
		return DefaultPiMemoryPostRunMinTools
	}
	return cfg.PiMemoryPostRunMinTools
}

func piMemoryPostRunTimeout(cfg Config) time.Duration {
	if cfg.PiMemoryPostRunTimeout <= 0 {
		return DefaultPiMemoryPostRunTimeout
	}
	return cfg.PiMemoryPostRunTimeout
}

func piMemoryPostRunShouldIncludeModel(task Task, result TaskResult, cfg Config) bool {
	switch piMemoryPostRunIncludeModelMode(cfg) {
	case piMemoryPostRunIncludeModelOn:
		return true
	case piMemoryPostRunIncludeModelOff:
		return false
	default:
		return len(result.Comment) >= piMemoryPostRunMinOutputChars(cfg) || containsPiMemoryLearningKeyword(piMemoryPostRunText(task, result))
	}
}

func isPiMemoryProviderFailure(result TaskResult) bool {
	reason := strings.TrimSpace(result.FailureReason)
	if reason == "" && strings.TrimSpace(result.Comment) != "" {
		reason = taskfailure.Classify(result.Comment).String()
	}
	switch reason {
	case string(taskfailure.ReasonAgentProviderQuotaLimit),
		string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
		string(taskfailure.ReasonAgentProviderNetwork):
		return true
	default:
		return false
	}
}

func piMemoryPostRunText(task Task, result TaskResult) string {
	return strings.Join([]string{
		result.Comment,
		task.ChatMessage,
		task.QuickCreatePrompt,
		task.TriggerCommentContent,
	}, "\n")
}

func containsPiMemoryLearningKeyword(text string) bool {
	text = strings.ToLower(text)
	for _, kw := range []string{
		"remember", "decision", "convention",
		"记住", "以后", "偏好", "约定", "踩坑", "经验", "教训", "修复", "验证",
	} {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

func tailString(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}
