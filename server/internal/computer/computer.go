package computer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

// Lifecycle is the single owner of the local Computer's resident process and
// state. Cobra and Desktop-facing adapters construct one from (mostly)
// flag-derived values and call its methods; they do not compute PID/log
// paths, spawn processes, or decide singleton rules themselves.
//
// The Probe, Sleep and Executable fields are the replaceable local
// dependencies the behaviour is tested through — tests substitute fakes that
// let them exercise lifecycle decisions without reaching into real processes
// or ports.
type Lifecycle struct {
	// Probe reports resident health; defaults to ProbeHealth.
	Probe HealthProbe
	// ControlPort selects the resident's loopback lifecycle port. Zero uses the
	// machine-wide default; tests may bind an ephemeral port.
	ControlPort int
	// Sleep blocks between readiness polls; defaults to time.Sleep.
	Sleep func(time.Duration)
	// Executable resolves the binary to exec for a fresh resident process;
	// defaults to computer.LaunchBinary.
	Executable func() (string, error)
	// Stderr receives progress/diagnostic output; defaults to os.Stderr.
	Stderr *os.File
}

// procHandle is the minimal handle the Lifecycle needs over a spawned
// resident process. The real implementation wraps *exec.Cmd; tests use a fake
// so start-path readiness can be exercised without a real OS process.
type procHandle interface {
	Start() error
	Pid() int
	Release() error
}

// requestShutdown is the graceful-shutdown transport. It exists as a package
// var so tests can substitute a fake and exercise Stop's decision path without
// reaching a real loopback endpoint.
var requestShutdown = RequestShutdown

// spawnResident starts a detached resident process writing to log. It is a
// field so tests can substitute a fake handle; the default is
// startResidentProcess.
var spawnResident = startResidentProcess

type startView struct {
	health   int
	logPath  string
	pidPath  string
	probe    HealthProbe
	sleep    func(time.Duration)
	stderr   *os.File
	executor func() (string, error)
}

func (l *Lifecycle) view() *startView {
	probe := l.Probe
	if probe == nil {
		probe = ProbeHealth
	}
	sleep := l.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	executor := l.Executable
	if executor == nil {
		executor = LaunchBinary
	}
	stderr := l.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	healthPort := l.ControlPort
	if healthPort <= 0 {
		healthPort = HealthPort("")
	}
	return &startView{
		health:   healthPort,
		logPath:  LogPath(""),
		pidPath:  PIDPath(""),
		probe:    probe,
		sleep:    sleep,
		stderr:   stderr,
		executor: executor,
	}
}

// StartupTimeout is how long StartBackground waits for a freshly launched
// resident to report ready. The resident binds the health port almost
// immediately but reports status:"starting" until preflight finishes (PAT
// renew + initial workspace sync, which exec's every configured agent for
// version detection and can take ~20s on a cold cache).
const StartupTimeout = 45 * time.Second

// StartResult is the outcome of a background start.
type StartResult struct {
	Pid        int
	LogPath    string
	Started    bool // reached health status "running"
	LastStatus string
}

// StartOptions is configuration, not process wiring. Lifecycle owns the
// actual compatibility command used for the resident child so Cobra/Desktop
// adapters cannot accidentally create a profile-specific or second resident.
type StartOptions struct {
	Generation                     int64
	DaemonID                       string
	DeviceName                     string
	RuntimeName                    string
	PollInterval                   time.Duration
	HeartbeatInterval              time.Duration
	AgentTimeout                   time.Duration
	AgentTimeoutSet                bool
	CodexSemanticInactivityTimeout time.Duration
}

const (
	// ResidentCommand is the public cobra command that owns the Computer.
	ResidentCommand = "computer"
	// ResidentServiceArg is the hidden argv that marks a spawned resident.
	// Callers never type this; Lifecycle and the supervisor assemble it.
	ResidentServiceArg = "__service"
)

// ResidentServicePrefix is the Computer-owned process contract. Workspace
// Runner inbound (agent:deliver / agent:start) is not assembled here.
func ResidentServicePrefix() []string {
	return []string{ResidentCommand, ResidentServiceArg}
}

// ResidentArgs is the one internal process contract for the detached
// Computer. Callers never assemble the prefix; they pass StartOptions only.
func ResidentArgs(options StartOptions) []string {
	args := ResidentServicePrefix()
	appendString := func(name, value string) {
		if strings.TrimSpace(value) != "" {
			args = append(args, name, value)
		}
	}
	appendDuration := func(name string, value time.Duration) {
		if value > 0 {
			args = append(args, name, value.String())
		}
	}
	appendString("--daemon-id", options.DaemonID)
	appendString("--device-name", options.DeviceName)
	appendString("--runtime-name", options.RuntimeName)
	if options.Generation > 0 {
		args = append(args, "--computer-generation", strconv.FormatInt(options.Generation, 10))
	}
	appendDuration("--poll-interval", options.PollInterval)
	appendDuration("--heartbeat-interval", options.HeartbeatInterval)
	if options.AgentTimeoutSet {
		args = append(args, "--agent-timeout", options.AgentTimeout.String())
	}
	appendDuration("--codex-semantic-inactivity-timeout", options.CodexSemanticInactivityTimeout)
	return args
}

// startResidentProcess spawns the resident binary (exe) with args, redirecting
// stdout/stderr to log. It retries without Job-breakaway on Windows
// ERROR_ACCESS_DENIED (a no-op on Unix) and detaches the child so it outlives
// the invoking shell. Returns a handle whose Start has already succeeded.
func startResidentProcess(exe string, args []string, log *os.File) (procHandle, error) {
	spawn := func(breakaway bool) (*exec.Cmd, error) {
		child := exec.Command(exe, args...)
		child.Stdout = log
		child.Stderr = log
		child.SysProcAttr = SysProcAttr(breakaway)
		if err := child.Start(); err != nil {
			return nil, err
		}
		return child, nil
	}
	child, err := spawn(true)
	if err != nil {
		if !IsAccessDeniedSpawnErr(err) {
			return nil, err
		}
		child, err = spawn(false)
		if err != nil {
			return nil, err
		}
	}
	return &execProc{cmd: child}, nil
}

// execProc adapts *exec.Cmd to procHandle.
type execProc struct {
	cmd *exec.Cmd
}

func (p *execProc) Start() error { return p.cmd.Start() }

func (p *execProc) Pid() int { return p.cmd.Process.Pid }

func (p *execProc) Release() error {
	// Detach: the resident process runs independently; we do not Wait on it.
	return p.cmd.Process.Release()
}

// StartBackground launches the resident process detached, publishes its PID,
// and waits for it to report ready (or a bounded time). It returns enough for
// the adapter to render user-facing output. It implies an already-running
// guard: if a resident is already live on the port it returns a typed error
// the adapter can surface.
func (l *Lifecycle) StartBackground(options StartOptions) (StartResult, error) {
	v := l.view()
	startCtx, startCancel := context.WithTimeout(context.Background(), 2*time.Second)
	startLease, err := acquireStartLease(startCtx, RootDir(""))
	startCancel()
	if err != nil {
		return StartResult{}, fmt.Errorf("another Computer start is already in progress: %w", err)
	}
	defer startLease.Close()

	if options.Generation == 0 {
		generation, err := NewGenerationStore(RootDir("")).Next()
		if err != nil {
			return StartResult{}, fmt.Errorf("allocate Computer generation: %w", err)
		}
		options.Generation = generation
	}

	// Already-running guard.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	health := v.probe(ctx, v.health)
	if Alive(health) {
		pid, _ := health["pid"].(float64)
		return StartResult{}, fmt.Errorf("already running (pid %v)", int(pid))
	}
	if _, err := l.Identity(); err != nil {
		return StartResult{}, fmt.Errorf("resolve Computer identity before start: %w", err)
	}

	exePath, err := v.executor()
	if err != nil {
		return StartResult{}, fmt.Errorf("resolve executable path: %w", err)
	}

	// Open/append the log file.
	logPath := v.logPath
	dir := filepath.Dir(logPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return StartResult{}, fmt.Errorf("create daemon directory: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return StartResult{}, fmt.Errorf("open log file %s: %w", logPath, err)
	}

	child, err := spawnResident(exePath, ResidentArgs(options), logFile)
	logFile.Close()
	if err != nil {
		return StartResult{}, err
	}
	pid := child.Pid()
	// Write PID file.
	if err := os.WriteFile(v.pidPath, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		fmt.Fprintf(v.stderr, "Warning: could not write PID file: %v\n", err)
	}
	_ = child.Release()

	deadline := time.Now().Add(StartupTimeout)
	started := false
	lastStatus := ""
	for time.Now().Before(deadline) {
		v.sleep(500 * time.Millisecond)
		hctx, hcancel := context.WithTimeout(context.Background(), 2*time.Second)
		h := v.probe(hctx, v.health)
		hcancel()
		lastStatus, _ = h["status"].(string)
		if lastStatus == "running" {
			started = true
			break
		}
	}
	return StartResult{Pid: pid, LogPath: logPath, Started: started, LastStatus: lastStatus}, nil
}

// StopResult is the outcome of a stop.
type StopResult struct {
	Running        bool // a resident was live and was asked to stop
	Pid            int
	Stopped        bool // the port was fully released and the PID file cleared
	GracefulFailed bool // the /shutdown request failed and a forced kill was used
	Err            error
}

// Stop gracefully stops the resident process via its /shutdown endpoint,
// falling back to a forced kill, then waits for the port to be released and
// clears the PID file.
func (l *Lifecycle) Stop() StopResult {
	v := l.view()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	health := v.probe(ctx, v.health)
	if !Alive(health) {
		return StopResult{Running: false}
	}

	res := StopResult{Running: true, Pid: 0}
	pid, ok := health["pid"].(float64)
	if !ok || pid == 0 {
		res.Err = fmt.Errorf("could not determine PID from health endpoint")
		return res
	}
	res.Pid = int(pid)

	process, err := os.FindProcess(int(pid))
	if err != nil {
		res.Err = fmt.Errorf("find process %d: %w", int(pid), err)
		return res
	}

	// Request graceful shutdown via HTTP /shutdown rather than an OS signal.
	// On Windows the process is spawned with DETACHED_PROCESS so it shares no
	// console, meaning GenerateConsoleCtrlEvent can't reach it; HTTP works on
	// both platforms and triggers the same context-cancel path as self-restart.
	if err := requestShutdown(v.health); err != nil {
		res.GracefulFailed = true
		fmt.Fprintf(v.stderr, "Graceful shutdown request failed: %v — falling back to forced kill.\n", err)
		if kerr := process.Kill(); kerr != nil {
			res.Err = fmt.Errorf("kill daemon (pid %d): %w", int(pid), kerr)
			return res
		}
	}

	// Poll health until the process is gone; clear the PID file on success.
	for i := 0; i < 10; i++ {
		v.sleep(500 * time.Millisecond)
		ctx2, cancel2 := context.WithTimeout(context.Background(), 1*time.Second)
		h := v.probe(ctx2, v.health)
		cancel2()
		if !Alive(h) {
			_ = os.Remove(v.pidPath)
			res.Stopped = true
			return res
		}
	}
	return res
}

// Identity resolves the stable machine-wide Computer identity. The
// adapter calls through this one interface rather than reaching into daemon
// internals; the identity persistence seam itself is owned by this module.
func (l *Lifecycle) Identity() (string, error) {
	store := NewIdentityStore(RootDir(""))
	return store.MustID("")
}

// Status probes the resident and returns its health map (formatted by the
// adapter), augmented with the machine-wide identity as a strictly read-only
// projection that never mints or mutates state. A stopped Computer yields a
// status of "stopped".
func (l *Lifecycle) Status() map[string]any {
	v := l.view()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	raw := v.probe(ctx, v.health)
	health := map[string]any{
		"status":           "stopped",
		"connected":        false,
		"canonical_origin": CanonicalCloudOrigin,
		"health_port":      v.health,
		"log_path":         v.logPath,
		"pid_path":         v.pidPath,
	}
	for _, key := range []string{"status", "pid", "uptime", "cli_version", "connected", "server_url", "environment"} {
		if value, ok := raw[key]; ok {
			health[key] = value
		}
	}
	if value, ok := raw["server_url"]; ok {
		health["resident_service_origin"] = value
	}
	if value, ok := raw["environment"]; ok {
		health["resident_environment"] = value
	}
	if value, ok := raw["release_channel"]; ok {
		health["resident_package_source"] = packageSourceForReleaseChannel(fmt.Sprint(value))
	}
	store := NewIdentityStore(RootDir(""))
	for k, val := range store.Peek("") {
		health[k] = val
	}
	connections, err := NewBindingsStore(RootDir("")).AllActive()
	if err == nil {
		safe := make([]map[string]any, 0, len(connections))
		for _, connection := range connections {
			safe = append(safe, map[string]any{
				"environment":    connection.Environment,
				"origin":         connection.Origin,
				"workspace_id":   connection.WorkspaceID,
				"workspace_slug": connection.WorkspaceSlug,
				"active":         true,
			})
		}
		health["workspace_connections"] = safe
	}
	if session, ok := readSessionProjection(); ok {
		health["session_present"] = session.TokenPresent
		health["service_origin"] = session.Origin
		health["environment"] = session.Environment
		health["package_source"] = packageSourceForReleaseChannel(session.ReleaseChannel)
		if Alive(raw) {
			health["configuration_drift"] = strings.TrimRight(fmt.Sprint(raw["server_url"]), "/") != strings.TrimRight(session.Origin, "/") ||
				fmt.Sprint(raw["environment"]) != session.Environment ||
				fmt.Sprint(raw["release_channel"]) != session.ReleaseChannel
		}
	} else {
		health["session_present"] = false
	}
	return health
}

func packageSourceForReleaseChannel(channel string) string {
	if strings.EqualFold(strings.TrimSpace(channel), string(cli.ReleaseChannelAlpha)) {
		return "preview"
	}
	return "stable"
}

type sessionProjection struct {
	TokenPresent   bool
	Origin         string
	Environment    string
	ReleaseChannel string
}

// readSessionProjection deliberately parses only presence + origin. It never
// returns or logs the token bytes and keeps status inside the Computer module.
func readSessionProjection() (sessionProjection, bool) {
	cfg, err := cli.LoadCLIConfigForProfile("")
	if err != nil {
		return sessionProjection{}, false
	}
	target, err := cli.ResolveServiceTarget(cfg)
	if err != nil {
		return sessionProjection{}, false
	}
	channel, err := cli.ResolveReleaseChannel(cfg)
	if err != nil {
		return sessionProjection{}, false
	}
	return sessionProjection{
		TokenPresent:   strings.TrimSpace(cfg.Token) != "",
		Origin:         target.Origin,
		Environment:    string(target.Environment),
		ReleaseChannel: string(channel),
	}, true
}

// Logs tails the resident log file.
func (l *Lifecycle) Logs(lines int, follow bool) error {
	logPath := l.view().logPath
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		return fmt.Errorf("no log file found at %s\nThe daemon may not have been started in background mode", logPath)
	}
	return streamLog(logPath, lines, follow, "")
}

// LogsForWorkspace scopes the resident service log to records that explicitly
// carry one immutable Workspace id. It never switches the running Binding set.
func (l *Lifecycle) LogsForWorkspace(lines int, follow bool, workspaceID string) error {
	logPath := l.view().logPath
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		return fmt.Errorf("no log file found at %s\nThe Computer may not have been started in background mode", logPath)
	}
	return streamLog(logPath, lines, follow, workspaceID)
}

// PublishPID writes the current process's PID to the resident PID file and
// returns a cleanup func that removes it only if it still names this PID
// (so an incumbent's deferred cleanup never deletes a successor's fresh PID
// during a Machine Upgrade handoff).
func (l *Lifecycle) PublishPID() (func(), error) {
	v := l.view()
	if err := os.MkdirAll(filepath.Dir(v.pidPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(v.pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return nil, err
	}
	return func() { l.RemovePIDIfMatches(os.Getpid()) }, nil
}

// RemovePIDIfMatches removes the PID file only when it still names pid.
func (l *Lifecycle) RemovePIDIfMatches(pid int) {
	path := l.view().pidPath
	data, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) != strconv.Itoa(pid) {
		return
	}
	_ = os.Remove(path)
}
