package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/cli"
)

// loginTokenPrefixes are the token prefixes `multica login --token` accepts.
// The CLI used to hardcode `mul_` only, which made it impossible to log in
// with a Multica Cloud Node PAT (`mcn_`) even though the server happily
// authenticates both kinds. Keep this list in sync with the prefix branches
// in server/internal/middleware/auth.go.
var loginTokenPrefixes = []string{"mul_", auth.CloudPATPrefix}

// validateLoginTokenPrefix returns nil if token starts with one of the
// CLI-recognised PAT prefixes, or an error describing the accepted set.
// Extracted so the prefix list has one obvious test surface.
func validateLoginTokenPrefix(token string) error {
	for _, p := range loginTokenPrefixes {
		if strings.HasPrefix(token, p) {
			return nil
		}
	}
	return fmt.Errorf("invalid token format: must start with %s", strings.Join(loginTokenPrefixes, " or "))
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authenticate multica with Multica",
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current authentication status",
	RunE:  runAuthStatus,
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove stored authentication token",
	RunE:  runAuthLogout,
}

// callbackHostFlag lets users override the host/IP that goes into the OAuth
// cli_callback URL. Useful when the CLI sits behind a reverse proxy or the
// auto-detected LAN IP isn't the one the browser can reach.
const callbackHostFlag = "callback-host"

func init() {
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(authLogoutCmd)
}

// multicaTokenEnvKey is the sole production BasicLit "MULTICA_TOKEN".
// Identifier may only be used by ambientTokenFromEnvOrFile and runtimeEnvToken
// (Barry #1305 primitive-location + no mutable global for map keys).
const multicaTokenEnvKey = "MULTICA_TOKEN"

// ambientTokenFromEnvOrFile returns MULTICA_TOKEN or the contents of
// MULTICA_TOKEN_FILE (daemon agent runs unset MULTICA_TOKEN and inject
// MULTICA_TOKEN_FILE with mat_* — see daemon cli_transport). Must stay in
// sync with isAgentAPIToken / isAgentAPITokenAmbient path selection (#801).
func ambientTokenFromEnvOrFile() string {
	if v := strings.TrimSpace(os.Getenv(multicaTokenEnvKey)); v != "" {
		return v
	}
	if path := strings.TrimSpace(os.Getenv("MULTICA_TOKEN_FILE")); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}

// runtimeEnvToken is a pure map lookup for sandboxd runtime_env agent token.
// No env/file I/O and no package mutable state.
func runtimeEnvToken(env map[string]string) string {
	if env == nil {
		return ""
	}
	return strings.TrimSpace(env[multicaTokenEnvKey])
}

func resolveToken(cmd *cobra.Command) string {
	if v := ambientTokenFromEnvOrFile(); v != "" {
		return v
	}
	if inAgentExecutionContext() {
		return ""
	}
	profile := resolveProfile(cmd)
	cfg, _ := cli.LoadCLIConfigForProfile(profile)
	return cfg.Token
}

func resolveAppURL(cmd *cobra.Command) string {
	// The persisted profile is the user's selected login surface. Ambient env
	// vars are only fallbacks for dev/no-config flows; otherwise a stale
	// MULTICA_APP_URL=http://localhost:3000 can make cloud setup open localhost.
	if val := configuredAppURL(cmd); val != "" {
		return val
	}
	for _, key := range []string{"MULTICA_APP_URL", "FRONTEND_ORIGIN"} {
		if val := strings.TrimSpace(os.Getenv(key)); val != "" {
			return strings.TrimRight(val, "/")
		}
	}
	fmt.Fprintln(os.Stderr, "No app URL configured. Run 'multica setup' first.")
	os.Exit(1)
	return "" // unreachable
}

func configuredAppURL(cmd *cobra.Command) string {
	profile := resolveProfile(cmd)
	cfg, err := cli.LoadCLIConfigForProfile(profile)
	if err == nil && cfg.AppURL != "" {
		return strings.TrimRight(cfg.AppURL, "/")
	}
	return ""
}

func openBrowser(url string) error {
	cmd, args, err := browserOpenCommand(runtime.GOOS, isWSL(), url)
	if err != nil {
		return err
	}
	return exec.Command(cmd, args...).Start()
}

func browserOpenCommand(goos string, wsl bool, url string) (string, []string, error) {
	var cmd string
	var args []string
	switch goos {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "linux":
		if wsl {
			cmd = "cmd.exe"
			args = []string{"/c", "start", "", `"` + strings.ReplaceAll(url, `"`, `\"`) + `"`}
		} else {
			cmd = "xdg-open"
			args = []string{url}
		}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		return "", nil, fmt.Errorf("unsupported platform: %s", goos)
	}
	return cmd, args, nil
}

func isWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if strings.TrimSpace(os.Getenv("WSL_DISTRO_NAME")) != "" || strings.TrimSpace(os.Getenv("WSL_INTEROP")) != "" {
		return true
	}
	data, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return false
	}
	release := strings.ToLower(string(data))
	return strings.Contains(release, "microsoft") || strings.Contains(release, "wsl")
}

func runAuthLogin(cmd *cobra.Command, args []string) error {
	if cmd.Flags().Changed("token") {
		tokenFlag, _ := cmd.Flags().GetString("token")
		// `--token mul_xxx` (space form) is what users actually type — that's
		// the form from the docs and from #1994. NoOptDefVal prevents pflag
		// from consuming the next arg as the flag value, so it lands here as
		// a positional. Promote it to the token value.
		if tokenFlag == tokenPromptSentinel && len(args) == 1 {
			tokenFlag = args[0]
		}
		return runAuthLoginToken(cmd, tokenFlag)
	}
	return runAuthLoginDevice(cmd)
}

// resolveCallbackBinding picks the host that goes into the `cli_callback`
// URL and the interface the CLI should bind its local HTTP listener to.
//
// The browser running the login flow is on the *server's* machine (or
// wherever the user clicked the link), not on the CLI host. That means the
// callback URL must resolve to an address the browser can actually reach,
// which is different in each topology:
//
//   - hosted / public app URL: browser and CLI are on the same machine,
//     localhost works.
//   - self-host, CLI on server box: same as above.
//   - self-host, CLI on a different LAN box: the callback URL must point at
//     the CLI's own LAN IP, not the server's.
//   - reverse-proxied / FQDN setups: auto-detection can't know the right
//     host — the user supplies it via --callback-host.
//
// detectOutbound is injected so tests can exercise the routing decisions
// without real network calls.
func resolveCallbackBinding(flagHost, serverURL, appURL string, detectOutbound func(string) net.IP) (callbackHost, bindAddr string) {
	// Explicit flag always wins. Bind on all interfaces so the browser can
	// reach us regardless of which interface the host name resolves to.
	if h := strings.TrimSpace(flagHost); h != "" {
		return h, "0.0.0.0"
	}

	appIP := urlPrivateIP(appURL)
	if appIP == nil {
		// Public hostname, FQDN without private-IP mapping, or parse error.
		// Loopback is the only safe default — on hosted/public setups the
		// browser and CLI live on the same machine.
		return "localhost", "127.0.0.1"
	}

	// app_url is a private LAN IP. Figure out whether the CLI is on that
	// same box or a different one by asking the kernel which local address
	// it would use to reach the server. Same box → loopback is fine.
	// Different box → use the CLI's outbound IP so the browser can reach us.
	cliIP := detectOutbound(serverURL)
	if cliIP == nil {
		// Detection failed (offline, unreachable server, etc.). Fall back to
		// the app IP — preserves the pre-existing same-machine behaviour.
		return appIP.String(), "0.0.0.0"
	}
	if cliIP.Equal(appIP) {
		return "localhost", "127.0.0.1"
	}
	return cliIP.String(), "0.0.0.0"
}

// urlPrivateIP returns the hostname of rawURL parsed as an RFC 1918 IP, or
// nil if the URL is unparsable or the host is not a private literal.
func urlPrivateIP(rawURL string) net.IP {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !ip.IsPrivate() {
		return nil
	}
	return ip
}

// deviceCodeResponse mirrors the server's RFC 8628 §3.2 response shape
// (server/internal/handler/device_auth.go's deviceCodeResponse) — field
// names are the contract between the two, kept in sync manually since the
// CLI has no generated client.
type deviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type issueDeviceTokenResponse struct {
	Token         string `json:"token"`
	ExpiresInDays int    `json:"expires_in_days"`
}

// runAuthLoginDevice implements the RFC 8628 Device Authorization Grant —
// the default `multica login` path (GitHub CLI / gcloud / az use the same
// shape). Unlike runAuthLoginBrowser, this does not require the browser and
// the CLI process to be on the same machine: the CLI only needs outbound
// HTTPS to the server, and confirmation happens wherever the user opens the
// printed link.
func runAuthLoginDevice(cmd *cobra.Command) error {
	serverURL := resolveServerURL(cmd)
	client := cli.NewAPIClient(serverURL, "", "")

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}

	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var code deviceCodeResponse
	if err := client.PostJSON(ctx, "/api/device/code", map[string]string{"client_hint": hostname}, &code); err != nil {
		return cli.WithUserMessage("Could not start sign-in — the server did not accept the device-code request.", err)
	}

	fmt.Fprintf(os.Stderr, "Confirm this sign-in in your browser:\n\n  %s\n\n", code.VerificationURIComplete)
	fmt.Fprintf(os.Stderr, "(Code: %s — if the link doesn't open, visit %s and enter it manually.)\n", code.UserCode, code.VerificationURI)
	if err := openBrowser(code.VerificationURIComplete); err != nil {
		fmt.Fprintln(os.Stderr, "Could not open browser automatically.")
	}
	fmt.Fprintln(os.Stderr, "\nWaiting for confirmation...")

	rawToken, expiresInDays, err := pollDeviceToken(cmd, client, code)
	if err != nil {
		return err
	}

	// Verify the PAT works, same tail as runAuthLoginBrowser.
	patClient := cli.NewAPIClient(serverURL, "", rawToken)
	verifyCtx, verifyCancel := cli.APIContext(context.Background())
	defer verifyCancel()
	var me struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := patClient.GetJSON(verifyCtx, "/api/me", &me); err != nil {
		return cli.WithUserMessage("Sign-in did not complete: the server did not accept the new credential. Run `multica login` again.", err)
	}

	profile := resolveProfile(cmd)
	cfg, _ := cli.LoadCLIConfigForProfile(profile)
	cfg.WorkspaceID = ""
	cfg.Token = rawToken
	cfg.ServerURL = serverURL
	cfg.AppURL = resolveAppURL(cmd)
	if err := cli.SaveCLIConfigForProfile(cfg, profile); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	_ = expiresInDays
	fmt.Fprintf(os.Stderr, "Authenticated as %s (%s)\nToken saved to config.\n", me.Name, me.Email)
	return nil
}

// pollDeviceToken polls /api/device/token on the server-advertised interval
// (backing off on slow_down) until the flow resolves or the device code's
// own expiry elapses. Mirrors waitForWorkspaceCreation's poll-with-deadline
// shape (cmd_login.go).
func pollDeviceToken(cmd *cobra.Command, client *cli.APIClient, code deviceCodeResponse) (token string, expiresInDays int, err error) {
	interval := time.Duration(code.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(code.ExpiresIn) * time.Second)

	for {
		if time.Now().After(deadline) {
			return "", 0, fmt.Errorf("timed out waiting for confirmation — run `multica login` again")
		}
		time.Sleep(interval)

		pollCtx, cancel := context.WithTimeout(context.Background(), cli.AtLeastAPITimeout(10*time.Second))
		var resp issueDeviceTokenResponse
		pollErr := client.PostJSON(pollCtx, "/api/device/token", map[string]string{"device_code": code.DeviceCode}, &resp)
		cancel()

		if pollErr == nil {
			return resp.Token, resp.ExpiresInDays, nil
		}

		var httpErr *cli.HTTPError
		if !errors.As(pollErr, &httpErr) {
			continue // transient network error, keep polling
		}
		switch deviceTokenErrorCode(httpErr.Body) {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "access_denied":
			return "", 0, fmt.Errorf("sign-in was denied")
		case "expired_token":
			return "", 0, fmt.Errorf("the device code expired — run `multica login` again")
		default:
			continue // unrecognized 4xx body, keep polling until deadline
		}
	}
}

func deviceTokenErrorCode(body string) string {
	var parsed struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &parsed)
	return parsed.Error
}

func runAuthLoginToken(cmd *cobra.Command, providedToken string) error {
	// The prompt sentinel is what pflag substitutes for `--token` with no
	// value (see loginCmd init); treat it the same as an empty string so we
	// fall through to the interactive prompt.
	if providedToken == tokenPromptSentinel {
		providedToken = ""
	}
	token := strings.TrimSpace(providedToken)
	if token == "" {
		fmt.Print("Enter your personal access token: ")
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			return fmt.Errorf("no input")
		}
		token = strings.TrimSpace(scanner.Text())
	}
	if token == "" {
		return fmt.Errorf("token is required")
	}
	if err := validateLoginTokenPrefix(token); err != nil {
		return err
	}

	serverURL := resolveServerURL(cmd)
	client := cli.NewAPIClient(serverURL, "", token)

	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	var me struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := client.GetJSON(ctx, "/api/me", &me); err != nil {
		return cli.WithUserMessage("Could not sign in with that token — make sure it is valid and not expired, then run `multica login --token <token>` again.", err)
	}

	profile := resolveProfile(cmd)
	cfg, _ := cli.LoadCLIConfigForProfile(profile)
	cfg.WorkspaceID = ""
	cfg.Token = token
	cfg.ServerURL = serverURL
	if err := cli.SaveCLIConfigForProfile(cfg, profile); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Authenticated as %s (%s)\nToken saved to config.\n", me.Name, me.Email)
	return nil
}

func runAuthStatus(cmd *cobra.Command, _ []string) error {
	token := resolveToken(cmd)
	serverURL := resolveServerURL(cmd)

	if token == "" {
		fmt.Fprintln(os.Stderr, "Not authenticated. Run 'multica login' to authenticate.")
		return nil
	}

	client := cli.NewAPIClient(serverURL, "", token)

	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	var me struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := client.GetJSON(ctx, "/api/me", &me); err != nil {
		fmt.Fprintf(os.Stderr, "Token is invalid or expired: %v\nRun 'multica login' to re-authenticate.\n", err)
		return nil
	}

	prefix := token
	if len(prefix) > 12 {
		prefix = prefix[:12] + "..."
	}

	fmt.Fprintf(os.Stderr, "Server:  %s\nUser:    %s (%s)\nToken:   %s\n", serverURL, me.Name, me.Email, prefix)
	return nil
}

func runAuthLogout(cmd *cobra.Command, _ []string) error {
	profile := resolveProfile(cmd)
	cfg, _ := cli.LoadCLIConfigForProfile(profile)
	if cfg.Token == "" {
		fmt.Fprintln(os.Stderr, "Not authenticated.")
		return nil
	}

	cfg.Token = ""
	if err := cli.SaveCLIConfigForProfile(cfg, profile); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Fprintln(os.Stderr, "Token removed. You are now logged out.")
	return nil
}
