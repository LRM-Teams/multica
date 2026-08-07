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
	Use:   "setup [/workspace]",
	Short: "Configure the CLI, authenticate, and install the daemon service",
	Long: `Configures the CLI to connect to Multica Cloud (leagent.me), then
authenticates via browser and installs the agent daemon as a per-user OS
service (LaunchAgent / systemd --user / Windows Scheduled Task) so it
survives terminal close and restarts after upgrade.

If a configuration already exists, you will be prompted before overwriting.

Pass exactly one workspace path to connect this computer in that workspace:
multica setup /my-workspace

Use 'multica setup self-host' to connect to a self-hosted server instead.

Use --profile to create an isolated configuration for a separate environment:
  multica setup self-host --profile staging --server-url https://api-staging.co`,
	Args: requireWorkspacePath,
	RunE: runSetupCloud,
}

var setupCloudCmd = &cobra.Command{
	Use:   "cloud [/workspace]",
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
	// #2496: self-host is no longer a supported setup surface; the retired
	// command is unregistered + hidden (its helpers/tests remain for the
	// bounded legacy-migration path).
	setupSelfHostCmd.Hidden = true
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

// configureWorkspaceDaemonProfile gives each cloud workspace its own daemon
// service and state directory. An explicit profile remains an advanced override.
func configureWorkspaceDaemonProfile(cmd *cobra.Command, args []string) (string, error) {
	if profile := resolveProfile(cmd); profile != "" {
		return profile, nil
	}
	profile := "workspace-" + strings.TrimPrefix(args[0], "/")
	if err := cmd.Flags().Set("profile", profile); err != nil {
		return "", fmt.Errorf("set workspace daemon profile: %w", err)
	}
	return profile, nil
}

func runSetupCloud(cmd *cobra.Command, args []string) error {
	if err := applyWorkspacePositional(cmd, args); err != nil {
		return err
	}
	profile, err := configureWorkspaceDaemonProfile(cmd, args)
	if err != nil {
		return err
	}

	ok, err := confirmOverwrite(profile)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	cfg := cloudCLIConfig()
	if err := cli.SaveCLIConfigForProfile(cfg, profile); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Fprintln(os.Stderr, "Configured for Multica Cloud (https://leagent.me).")
	fmt.Fprintf(os.Stderr, "  server_url: %s\n", cfg.ServerURL)
	fmt.Fprintf(os.Stderr, "  app_url:    %s\n", cfg.AppURL)
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

func cloudCLIConfig() cli.CLIConfig {
	return cli.CLIConfig{
		ServerURL: cli.OfficialCloudAPIURL,
		AppURL:    cli.OfficialCloudAppURL,
	}
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

// startDaemonAfterSetup installs the per-user OS service so setup always ends
// with a supervised daemon (launchd / systemd --user / Windows Scheduled Task).
// Bare `multica daemon start` remains for development/emergency only and is
// not treated as a completed machine setup — unsupervised processes stay
// stopped after an auto-update self-stop (see daemon update logs).
func startDaemonAfterSetup(cmd *cobra.Command) error {
	// Establish the Workspace Binding (#2489). A failure here must not read as
	// a successful setup.
	if err := establishWorkspaceBinding(cmd); err != nil {
		return fmt.Errorf("Setup incomplete (%w): the Computer identity is registered but this Workspace Binding could not be established; re-run `multica setup /<ws>` to repair", err)
	}

	// Start/install the resident so the Computer stays connected. (Per #2487/#2496
	// this should eventually be the machine-wide detached resident with no OS
	// supervisor; that live-verified cutover is tracked separately.)
	fmt.Fprintln(os.Stderr, "\nInstalling daemon service (auto-start at login + auto-restart)...")
	if err := runDaemonInstallService(cmd, nil); err != nil {
		return fmt.Errorf("install daemon service: %w\n  For development only you can fall back to `multica daemon start`; setup expects install-service so upgrades can reconnect without a terminal", err)
	}
	return nil
}

// establishWorkspaceBinding records the selected Workspace as a Binding for
// this Computer (#2489): persisted locally (machine-wide, keyed by the
// immutable workspace_id) and registered with the server so the Web Computers
// projection reflects a connected Machine. Returns an error (Setup
// incomplete) so a partial setup never reads as successful.
func establishWorkspaceBinding(cmd *cobra.Command) error {
	profile := resolveProfile(cmd)
	cfg, err := cli.LoadCLIConfigForProfile(profile)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	if cfg.WorkspaceID == "" {
		return nil // no workspace selected; nothing to bind yet
	}

	identity, err := (&computer.Lifecycle{Profile: ""}).Identity()
	if err != nil {
		return fmt.Errorf("resolve computer identity: %w", err)
	}
	store := computer.NewBindingsStore(computer.RootDir(""))
	if err := store.AddOrRepair(computer.WorkspaceBinding{
		WorkspaceID: cfg.WorkspaceID,
		ComputerID:  identity,
		Active:      true,
	}); err != nil {
		return fmt.Errorf("persist binding: %w", err)
	}

	// Best-effort server registration; local persistence is the durable record.
	if cfg.ServerURL != "" {
		client := cli.NewAPIClient(cfg.ServerURL, "", cfg.Token)
		ctx, cancel := cli.APIContext(context.Background())
		defer cancel()
		_ = client.PostJSON(ctx, "/api/daemons/"+identity+"/bindings", map[string]any{
			"workspace_id": cfg.WorkspaceID,
		}, nil)
	}
	return nil
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
