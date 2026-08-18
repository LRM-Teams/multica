package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/computer"
)

var setupCmd = &cobra.Command{
	Use:   "setup /<workspace>",
	Short: "Connect this Computer to Multica Cloud (leagent.me)",
	Long: `Connects this machine to Multica Cloud (leagent.me), authenticates, and
starts the machine-wide resident Computer as a detached process that survives
terminal close. It does not install an OS service or a profile-scoped daemon.`,
	Args: requireWorkspacePath,
	RunE: runSetupCloud,
}

var setupCloudCmd = &cobra.Command{
	Use:   "cloud /<workspace>",
	Short: "Configure the CLI for Multica Cloud (leagent.me)",
	Long: `Explicitly configures the CLI to connect to Multica Cloud (leagent.me).

This is equivalent to running 'multica setup' without a subcommand.`,
	Args: requireWorkspacePath,
	RunE: runSetupCloud,
}

var setupSelfHostCmd = &cobra.Command{
	Use:   "self-host [/workspace]",
	Short: "Configure the CLI for a self-hosted Multica server",
	Long: `Configures the CLI to connect to a self-hosted Multica server.

By default, connects to http://localhost:8080 (backend) and http://localhost:3000 (frontend).
Use --server-url and --app-url to specify a custom server (e.g. an on-premise deployment).

If you run this command from a different machine than the server, also pass
--callback-host <FQDN-or-IP-the-browser-can-reach-back-to-this-machine-on> so
the OAuth login flow can return the token to the CLI.

Pass a workspace id or slug to set it as the default in this one step
(equivalent to --workspace).

Examples:
  multica setup self-host
  multica setup self-host /my-workspace
  multica setup self-host --server-url https://api.internal.co --app-url https://app.internal.co
  multica setup self-host --port 9090 --frontend-port 4000`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSetupSelfHost,
}

func init() {
	for _, command := range []*cobra.Command{setupCmd, setupCloudCmd} {
		command.Flags().String("environment", string(cli.ServiceEnvironmentProduction), "Service environment: production or test")
		command.Flags().String("server-url", "", "Test API, auth, and WebSocket origin (required with --environment test)")
		command.Flags().String("app-url", "", "Test Web app origin (required with --environment test)")
		command.Flags().Bool("yes", false, "Confirm an environment switch without prompting")
	}
	setupCmd.Flags().String(callbackHostFlag, "", "Host the OAuth callback URL points at (auto-detected when empty). Use this for Windows WSL / reverse-proxy setups.")
	setupCmd.Flags().String("workspace", "", "Set the workspace by id or slug (env: MULTICA_WORKSPACE).")
	setupCloudCmd.Flags().String(callbackHostFlag, "", "Host the OAuth callback URL points at (auto-detected when empty). Use this for Windows WSL / reverse-proxy setups.")
	setupCloudCmd.Flags().String("workspace", "", "Set the workspace by id or slug (env: MULTICA_WORKSPACE).")
	setupSelfHostCmd.Flags().String("server-url", "", "Backend server URL (e.g. https://api.internal.co) (env: MULTICA_SERVER_URL)")
	setupSelfHostCmd.Flags().String("app-url", "", "Frontend app URL (e.g. https://app.internal.co) (env: MULTICA_APP_URL)")
	setupSelfHostCmd.Flags().Int("port", 8080, "Backend server port (used when --server-url is not set)")
	setupSelfHostCmd.Flags().Int("frontend-port", 3000, "Frontend port (used when --app-url is not set)")
	setupSelfHostCmd.Flags().String(callbackHostFlag, "", "Host the OAuth callback URL points at (auto-detected when empty). Use this for Windows WSL / reverse-proxy setups.")
	setupSelfHostCmd.Flags().String("workspace", "", "Set the workspace by id or slug (env: MULTICA_WORKSPACE).")

	setupCmd.AddCommand(setupCloudCmd)
	// Keep the old spelling parseable for one compatibility cycle without
	// turning setup help back into a subcommand-oriented surface. The public
	// contract is `multica setup /<workspace>`.
	setupCloudCmd.Hidden = true
	// #2496: self-host is no longer a supported setup surface; the retired
	// command is unregistered + hidden (its helpers/tests remain for the
	// bounded legacy-migration path).
	setupSelfHostCmd.Hidden = true
	_ = setupCmd.Flags().MarkHidden("workspace")
	_ = setupCloudCmd.Flags().MarkHidden("workspace")
}

// printConfigLocation prints the config file path and profile name.
func printConfigLocation(profile string) {
	path, err := cli.CLIConfigPathForProfile(profile)
	if err != nil {
		return
	}
	if profile != "" {
		fmt.Fprintf(os.Stderr, "  profile:    %s\n", profile)
	}
	fmt.Fprintf(os.Stderr, "  config:     %s\n", path)
}

// confirmOverwrite checks for an existing config and prompts the user.
// Returns true if we should proceed, false if the user declined.
func confirmOverwrite(profile string) (bool, error) {
	cfg, err := cli.LoadCLIConfigForProfile(profile)
	if err != nil {
		return true, nil // can't load → treat as no config
	}
	if cfg.ServerURL == "" {
		return true, nil // no server configured → fresh config
	}

	fmt.Fprintln(os.Stderr, "Current configuration:")
	fmt.Fprintf(os.Stderr, "  server_url: %s\n", cfg.ServerURL)
	fmt.Fprintf(os.Stderr, "  app_url:    %s\n", cfg.AppURL)
	if cfg.WorkspaceID != "" {
		fmt.Fprintf(os.Stderr, "  workspace:  %s\n", cfg.WorkspaceID)
	}
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprint(os.Stderr, "This will reset your configuration. Continue? [y/N] ")

	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer != "y" && answer != "yes" {
		fmt.Fprintln(os.Stderr, "Aborted.")
		return false, nil
	}
	return true, nil
}

// applyWorkspacePositional wires `multica setup /<workspace>` onto the same
// --workspace flag configureSelectedWorkspace reads, so there is exactly
// one place that resolves the id-or-slug against the workspace list. Does
// nothing if the flag was already set explicitly (flag wins) or no
// positional was given.
//
// The leading "/" (task #32 follow-up: aligning the command shape with
// Raft's `raft-computer setup /<server-slug>`) is stripped if present, but
// not required — a bare slug/id still works unchanged, so this is additive
// and doesn't break any existing script or doc that already types
// `multica setup my-workspace` without the slash.
func applyWorkspacePositional(cmd *cobra.Command, args []string) error {
	if len(args) == 0 || cmd.Flags().Changed("workspace") {
		return nil
	}
	return cmd.Flags().Set("workspace", strings.TrimPrefix(args[0], "/"))
}

// requireWorkspacePath keeps setup scoped to exactly one workspace. The slash
// distinguishes this product command from the retired bare setup form.
func requireWorkspacePath(_ *cobra.Command, args []string) error {
	if len(args) != 1 || !strings.HasPrefix(args[0], "/") || len(args[0]) == 1 {
		return fmt.Errorf("setup requires one workspace path: multica setup /<workspace-slug>")
	}
	return nil
}

func runSetupCloud(cmd *cobra.Command, args []string) error {
	// Capture legacy config before canonical setup may update the default
	// Workspace/session fields. The snapshot is used only for verified,
	// fail-closed migration and is never printed.
	legacyEvidence := captureLegacyComputerEvidence()
	// Setup is a machine-wide Computer operation. Ignore the inherited legacy
	// --profile flag throughout login, workspace resolution, and persistence.
	previousComputerMode := computerMode
	computerMode = true
	defer func() { computerMode = previousComputerMode }()

	target, err := resolveSetupServiceTarget(cmd, args)
	if err != nil {
		return err
	}
	currentCfg, err := cli.LoadCLIConfigForProfile("")
	if err != nil {
		return fmt.Errorf("load current config: %w", err)
	}
	confirmed, err := confirmSetupEnvironmentSwitch(cmd, currentCfg, target)
	if err != nil {
		return err
	}
	if !confirmed {
		fmt.Fprintln(cmd.ErrOrStderr(), "Setup aborted; the active environment was not changed.")
		return nil
	}

	// Create or repair the selected environment without erasing the inactive
	// environment's session. PutServiceEnvironment projects only the target
	// session into the effective fields consumed by login/setup callers.
	cfg, _ := cli.LoadCLIConfigForProfile("")
	cfg.PutServiceEnvironment(target)
	if err := cli.SaveCLIConfigForProfile(cfg, ""); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	packageSource := "stable"
	if target.Environment == cli.ServiceEnvironmentTest {
		packageSource = "preview"
	}
	fmt.Fprintf(os.Stderr, "Configured %s environment (%s packages).\n", target.Environment, packageSource)
	fmt.Fprintf(os.Stderr, "  server_url: %s\n", cfg.ServerURL)
	fmt.Fprintf(os.Stderr, "  app_url:    %s\n", cfg.AppURL)
	printConfigLocation("")

	// Authenticate.
	fmt.Fprintln(os.Stderr, "")
	if err := runLogin(cmd, args); err != nil {
		return err
	}
	if err := adoptVerifiedLegacyComputer(cmd, legacyEvidence); err != nil {
		return fmt.Errorf("Setup incomplete (legacy Computer migration: %w)", err)
	}

	if err := startDaemonAfterSetup(cmd); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "\n✓ Setup complete! This Computer is connected to the Workspace.")

	return nil
}

// resolveSetupServiceTarget keeps the public setup flags and Workspace
// positional argument on the exact preflight path used by runSetupCloud.
// Keeping this phase pure makes contradictory flag validation testable before
// setup writes config, opens a browser, or starts the resident.
func resolveSetupServiceTarget(cmd *cobra.Command, args []string) (cli.ServiceTarget, error) {
	// setup owns --server-url as the explicit Test API origin. Only the
	// inherited legacy profile selector is retired on this command; lifecycle
	// commands still reject their unrelated legacy --server-url flag.
	if err := rejectRetiredComputerProfileFlag(cmd); err != nil {
		return cli.ServiceTarget{}, err
	}
	if err := applyWorkspacePositional(cmd, args); err != nil {
		return cli.ServiceTarget{}, err
	}
	environment, _ := cmd.Flags().GetString("environment")
	serverURL, _ := cmd.Flags().GetString("server-url")
	appURL, _ := cmd.Flags().GetString("app-url")
	if cli.ServiceEnvironment(strings.ToLower(strings.TrimSpace(environment))) == cli.ServiceEnvironmentTest && (serverURL == "" || appURL == "") {
		cfg, err := cli.LoadCLIConfigForProfile("")
		if err != nil {
			return cli.ServiceTarget{}, fmt.Errorf("load saved Test environment: %w", err)
		}
		if saved, ok := cfg.Environments[string(cli.ServiceEnvironmentTest)]; ok {
			if serverURL == "" {
				serverURL = saved.ServerURL
			}
			if appURL == "" {
				appURL = saved.AppURL
			}
		}
	}
	return cli.NewServiceTarget(environment, serverURL, appURL)
}

func confirmSetupEnvironmentSwitch(cmd *cobra.Command, current cli.CLIConfig, target cli.ServiceTarget) (bool, error) {
	currentEnvironment := cli.ServiceEnvironment(strings.ToLower(strings.TrimSpace(current.Environment)))
	if strings.TrimSpace(current.ServerURL) == "" || currentEnvironment == "" || currentEnvironment == target.Environment {
		return true, nil
	}
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		return true, nil
	}
	packageSource := "stable"
	if target.Environment == cli.ServiceEnvironmentTest {
		packageSource = "preview"
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s is currently active. Setup will switch this Computer to %s, use %s packages, and restart it. Continue? [y/N] ", currentEnvironment, target.Environment, packageSource)
	answer, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && strings.TrimSpace(answer) == "" {
		return false, fmt.Errorf("read environment switch confirmation: %w", err)
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}

func cloudCLIConfig() cli.CLIConfig {
	cfg := cli.CLIConfig{}
	target, _ := cli.NewServiceTarget(string(cli.ServiceEnvironmentProduction), "", "")
	cfg.PutServiceEnvironment(target)
	return cfg
}

func runSetupSelfHost(cmd *cobra.Command, args []string) error {
	if err := applyWorkspacePositional(cmd, args); err != nil {
		return err
	}
	profile := resolveProfile(cmd)

	ok, err := confirmOverwrite(profile)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	// Honor MULTICA_SERVER_URL / MULTICA_APP_URL when the matching flag is not
	// set — consistent with the rest of the CLI (resolveServerURL) and with the
	// env vars documented on the root --server-url flag and in `multica --help`.
	// Before this, setup self-host read only the flags, so a self-hoster who set
	// MULTICA_SERVER_URL still got the localhost default and an "unreachable"
	// error (GitHub #3912).
	serverURL, userProvidedServerURL := resolveSelfHostServerURL(cmd)
	appURL := cli.FlagOrEnv(cmd, "app-url", "MULTICA_APP_URL", "")
	frontendPort, _ := cmd.Flags().GetInt("frontend-port")

	if appURL == "" {
		if userProvidedServerURL && !serverHostIsLocal(serverURL) {
			// We can't guess the frontend URL for a remote server: api.x.co
			// and app.x.co, or an https-fronted deployment, would silently
			// produce a broken login URL. Ask the user instead.
			entered, err := promptAppURL(serverURL)
			if err != nil {
				return err
			}
			if entered == "" {
				return fmt.Errorf("--app-url is required when --server-url points at a remote host (e.g. --app-url https://app.internal.co)")
			}
			appURL = entered
		} else {
			appURL = fmt.Sprintf("http://localhost:%d", frontendPort)
		}
	}

	// Probe before persisting anything. A failed setup must never overwrite a
	// working config or wipe the saved token: persistSelfHostConfigIfReachable
	// writes only when the server answers, so an unreachable host leaves the
	// existing config untouched and the user stays logged in.
	if !probeApp(appURL) {
		fmt.Fprintf(os.Stderr, "\n⚠ App at %s is not reachable.\n", appURL)
		fmt.Fprintln(os.Stderr, "  Your existing configuration was left unchanged.")
		fmt.Fprintln(os.Stderr, "  Verify --app-url, then re-run 'multica setup self-host' once it's reachable.")
		return nil
	}
	reachable, err := persistSelfHostConfigIfReachable(serverURL, appURL, profile, probeServer)
	if err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	if !reachable {
		fmt.Fprintf(os.Stderr, "\n⚠ Server at %s is not reachable.\n", serverURL)
		fmt.Fprintln(os.Stderr, "  Your existing configuration was left unchanged.")
		fmt.Fprintln(os.Stderr, "  Verify the URL, then re-run 'multica setup self-host' once it's reachable.")
		return nil
	}

	fmt.Fprintln(os.Stderr, "Configured for self-hosted server.")
	fmt.Fprintf(os.Stderr, "  server_url: %s\n", serverURL)
	fmt.Fprintf(os.Stderr, "  app_url:    %s\n", appURL)
	printConfigLocation(profile)

	// Authenticate.
	fmt.Fprintln(os.Stderr, "")
	if err := runLogin(cmd, args); err != nil {
		return err
	}

	if err := startDaemonAfterSetup(cmd); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "\n✓ Setup complete! Your machine is now connected to Multica (daemon supervised).")

	return nil
}

// startResidentAfterSetup launches the machine-wide detached resident after a
// successful setup. It is a package var so tests can substitute a fake and
// assert the resident-start contract without spawning a real OS process.
var startResidentAfterSetup = startResidentAfterSetupReal
var establishWorkspaceBindingAfterSetup = establishWorkspaceBinding
var waitForWorkspaceBindingAcceptanceAfterSetup = waitForWorkspaceBindingAcceptance

func startResidentAfterSetupReal(cmd *cobra.Command) error {
	lc := &computer.Lifecycle{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	health := lc.Health(ctx)
	if computer.Alive(health) {
		cfg, err := cli.LoadCLIConfigForProfile("")
		if err != nil {
			return fmt.Errorf("read current environment: %w", err)
		}
		matches, err := residentMatchesSetupTarget(health, cfg)
		if err != nil {
			return err
		}
		if matches {
			return nil
		}
		restarted, err := lc.Restart(computer.StartOptions{})
		if err != nil {
			return err
		}
		if !restarted.Start.Started {
			return fmt.Errorf("resident did not become ready after environment switch (last status: %s)", restarted.Start.LastStatus)
		}
		return nil
	}
	result, err := lc.StartBackground(computer.StartOptions{})
	if err != nil {
		return err
	}
	if !result.Started {
		return fmt.Errorf("resident did not become ready (last status: %s)", result.LastStatus)
	}
	return nil
}

func residentMatchesSetupTarget(health map[string]any, cfg cli.CLIConfig) (bool, error) {
	target, err := cli.ResolveServiceTarget(cfg)
	if err != nil {
		return false, err
	}
	channel, err := cli.ResolveReleaseChannel(cfg)
	if err != nil {
		return false, err
	}
	return normalizeAPIBaseURL(fmt.Sprint(health["server_url"])) == normalizeAPIBaseURL(target.Origin) &&
		fmt.Sprint(health["environment"]) == string(target.Environment) &&
		fmt.Sprint(health["release_channel"]) == string(channel), nil
}

func startDaemonAfterSetup(cmd *cobra.Command) error {
	// Establish the Workspace Binding (#2489). A failure here must not read as
	// a successful setup.
	if err := establishWorkspaceBindingAfterSetup(cmd); err != nil {
		return fmt.Errorf("Setup incomplete (%w): the Computer identity is registered but this Workspace connection could not be established; re-run `multica setup /<ws>` to repair", err)
	}

	// #2487/#2496: the Computer runs as one machine-wide detached resident that
	// survives terminal close; setup does NOT install an OS supervisor
	// (LaunchAgent/systemd/Scheduled Task).
	fmt.Fprintln(os.Stderr, "\nStarting the resident Computer...")
	if err := startResidentAfterSetup(cmd); err != nil {
		return fmt.Errorf("Setup incomplete (start resident: %w)", err)
	}
	return waitForWorkspaceBindingAcceptanceAfterSetup(cmd)
}

// establishWorkspaceBinding records the selected Workspace as a Binding for
// this Computer (#2489): persisted locally (machine-wide, keyed by the
// immutable workspace_id) and registered with the server so the Web Computers
// projection reflects a connected Machine. Returns an error (Setup
// incomplete) so a partial setup never reads as successful.
func establishWorkspaceBinding(cmd *cobra.Command) error {
	cfg, err := cli.LoadCLIConfigForProfile("")
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	if cfg.WorkspaceID == "" {
		return nil // no workspace selected; nothing to bind yet
	}

	identity, err := (&computer.Lifecycle{}).Identity()
	if err != nil {
		return fmt.Errorf("resolve computer identity: %w", err)
	}
	if cfg.ServerURL == "" || cfg.Token == "" {
		return fmt.Errorf("machine-wide Cloud session is missing")
	}

	workspaceSlug, _ := cmd.Flags().GetString("workspace")
	return establishWorkspaceConnection(cfg, identity, cfg.WorkspaceID, strings.TrimPrefix(workspaceSlug, "/"))
}

func establishWorkspaceConnection(cfg cli.CLIConfig, identity, workspaceID, workspaceSlug string) error {
	target, err := cli.ResolveServiceTarget(cfg)
	if err != nil {
		return fmt.Errorf("resolve service environment: %w", err)
	}
	// The connection endpoint is inside the authenticated Workspace scope: the
	// immutable Workspace is carried both in the header for middleware
	// authorization and in the body for the binding command itself.
	client := cli.NewAPIClient(cfg.ServerURL, workspaceID, cfg.Token)
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var accepted struct {
		OK                  bool   `json:"ok"`
		WorkspaceID         string `json:"workspace_id"`
		Credential          string `json:"credential"`
		CredentialExpiresAt string `json:"credential_expires_at"`
	}
	if err := client.PostJSON(ctx, "/api/computers/"+identity+"/workspace-connections", map[string]any{
		"workspace_id": workspaceID,
	}, &accepted); err != nil {
		return fmt.Errorf("register Workspace connection: %w", err)
	}
	if !accepted.OK || accepted.WorkspaceID != workspaceID || accepted.Credential == "" {
		return fmt.Errorf("server did not accept the selected Workspace connection")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, accepted.CredentialExpiresAt)
	if err != nil {
		return fmt.Errorf("parse Workspace connection credential expiry: %w", err)
	}
	store := computer.NewBindingsStore(computer.RootDir(""))
	if err := store.AddOrRepair(computer.WorkspaceBinding{
		Environment:         string(target.Environment),
		Origin:              target.Origin,
		WorkspaceID:         workspaceID,
		WorkspaceSlug:       workspaceSlug,
		ComputerID:          identity,
		Credential:          accepted.Credential,
		CredentialExpiresAt: expiresAt,
		AcceptedAt:          time.Now().UTC(),
		Active:              true,
	}); err != nil {
		return fmt.Errorf("persist accepted Workspace connection: %w", err)
	}
	return nil
}

var bindingAcceptanceTimeout = 50 * time.Second

func waitForWorkspaceBindingAcceptance(cmd *cobra.Command) error {
	cfg, err := cli.LoadCLIConfigForProfile("")
	if err != nil || cfg.WorkspaceID == "" {
		return fmt.Errorf("Setup incomplete (selected Workspace is missing)")
	}
	deadline := time.Now().Add(bindingAcceptanceTimeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		health := computer.ProbeHealth(ctx, computer.ServiceControlEndpoint(computer.RootDir("")))
		cancel()
		if healthProvesSetupAcceptance(health, cfg, cfg.WorkspaceID) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("Setup incomplete (timed out waiting for the authenticated Computer and selected Workspace connection); existing session, Workspace connections, and Agent data were preserved")
}

func healthProvesSetupAcceptance(health map[string]any, cfg cli.CLIConfig, workspaceID string) bool {
	target, err := cli.ResolveServiceTarget(cfg)
	if err != nil {
		return false
	}
	connected, _ := health["connected"].(bool)
	return health["status"] == "running" &&
		connected &&
		normalizeAPIBaseURL(fmt.Sprint(health["server_url"])) == normalizeAPIBaseURL(target.Origin) &&
		fmt.Sprint(health["environment"]) == string(target.Environment) &&
		healthContainsWorkspace(health, workspaceID)
}

func healthContainsWorkspace(health map[string]any, workspaceID string) bool {
	workspaces, ok := health["workspaces"].([]any)
	if !ok {
		return false
	}
	for _, raw := range workspaces {
		workspace, ok := raw.(map[string]any)
		if ok && fmt.Sprint(workspace["id"]) == workspaceID {
			return true
		}
	}
	return false
}

// persistSelfHostConfigIfReachable probes serverURL and, only when it answers,
// overwrites the profile config with the given self-host URLs. When the server
// is unreachable it leaves any existing config — and its auth token — untouched
// and returns false, so a failed `setup self-host` never logs the user out or
// clobbers a working config (the original ordering saved first, then probed,
// then bailed — wiping the token on every failed probe). The prober is injected
// so tests can exercise both branches without real network I/O.
func persistSelfHostConfigIfReachable(serverURL, appURL, profile string, probe func(string) bool) (bool, error) {
	if !probe(serverURL) {
		return false, nil
	}
	cfg := cli.CLIConfig{
		ServerURL: serverURL,
		AppURL:    appURL,
	}
	if err := cli.SaveCLIConfigForProfile(cfg, profile); err != nil {
		return false, err
	}
	return true, nil
}

// resolveSelfHostServerURL picks the backend URL for `setup self-host`: the
// --server-url flag wins, then the MULTICA_SERVER_URL env var (consistent with
// the rest of the CLI and the env var documented on the root flag), then the
// localhost default built from --port. userProvided is true when the URL came
// from the user (flag or env) rather than the localhost fallback — the caller
// uses it to decide whether a remote host needs an explicit app_url.
//
// A user-supplied URL is run through normalizeAPIBaseURL, the same path
// resolveServerURL uses: MULTICA_SERVER_URL is documented as a ws:// daemon
// address (e.g. ws://localhost:8080/ws), so the ws/wss form and a trailing /ws
// are accepted and converted to the http(s) base that the reachability probe
// and the stored server_url expect.
func resolveSelfHostServerURL(cmd *cobra.Command) (serverURL string, userProvided bool) {
	if v := cli.FlagOrEnv(cmd, "server-url", "MULTICA_SERVER_URL", ""); v != "" {
		return normalizeAPIBaseURL(v), true
	}
	port, _ := cmd.Flags().GetInt("port")
	return fmt.Sprintf("http://localhost:%d", port), false
}

// serverHostIsLocal reports whether serverURL points at the same machine as
// the CLI (loopback literal or "localhost"). Used to decide whether to infer
// app_url from server_url or fall back to the local-dev default.
func serverHostIsLocal(serverURL string) bool {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return false
	}
	h := parsed.Hostname()
	if h == "localhost" {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// promptAppURL asks the user for the frontend URL interactively. We can't
// derive it from a remote server_url — api.example.com ≠ app.example.com in
// most production setups — so guessing would just defer the failure to the
// browser login step. Returns an empty string if the user hits enter.
func promptAppURL(serverURL string) (string, error) {
	fmt.Fprintf(os.Stderr, "No --app-url provided, and --server-url (%s) is remote.\n", serverURL)
	fmt.Fprint(os.Stderr, "Enter the frontend app URL (e.g. https://app.internal.co): ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", nil
	}
	return strings.TrimRight(strings.TrimSpace(line), "/"), nil
}

// probeServer checks whether a Multica backend is reachable at the given URL.
func probeServer(baseURL string) bool {
	url := strings.TrimRight(baseURL, "/") + "/health"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}

	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// probeApp checks whether the configured frontend URL is reachable before we
// save it. This catches typoed app URLs before they generate a broken login URL.
func probeApp(appURL string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, strings.TrimRight(appURL, "/"), nil)
	if err != nil {
		return false
	}

	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < http.StatusInternalServerError
}
