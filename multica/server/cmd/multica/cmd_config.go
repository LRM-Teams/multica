package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage configuration for multica",
	RunE:  runConfigShow,
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current CLI configuration",
	RunE:  runConfigShow,
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a CLI configuration value",
	Long:  "Supported keys: server_url, app_url, workspace_id, proxy.http, proxy.https, proxy.no_proxy",
	Args:  exactArgs(2),
	RunE:  runConfigSet,
}

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configSetCmd)
}

func runConfigShow(cmd *cobra.Command, _ []string) error {
	profile := resolveProfile(cmd)
	cfg, err := cli.LoadCLIConfigForProfile(profile)
	if err != nil {
		return err
	}

	path, _ := cli.CLIConfigPathForProfile(profile)
	fmt.Fprintf(os.Stdout, "Config file: %s\n", path)
	if profile != "" {
		fmt.Fprintf(os.Stdout, "Profile:      %s\n", profile)
	}
	fmt.Fprintf(os.Stdout, "server_url:   %s\n", valueOrDefault(cfg.ServerURL, "(not set)"))
	fmt.Fprintf(os.Stdout, "app_url:      %s\n", valueOrDefault(cfg.AppURL, "(not set)"))
	fmt.Fprintf(os.Stdout, "workspace_id: %s\n", valueOrDefault(cfg.WorkspaceID, "(not set)"))
	if cfg.Proxy == nil {
		fmt.Fprintln(os.Stdout, "proxy.http:   (not set)")
		fmt.Fprintln(os.Stdout, "proxy.https:  (not set)")
		fmt.Fprintln(os.Stdout, "proxy.no_proxy: (not set)")
	} else {
		fmt.Fprintf(os.Stdout, "proxy.http:   %s\n", secretPresence(cfg.Proxy.HTTP))
		fmt.Fprintf(os.Stdout, "proxy.https:  %s\n", secretPresence(cfg.Proxy.HTTPS))
		fmt.Fprintf(os.Stdout, "proxy.no_proxy: %s\n", valueOrDefault(cfg.Proxy.NoProxy, "(not set)"))
	}
	return nil
}

func runConfigSet(cmd *cobra.Command, args []string) error {
	key, value := args[0], args[1]

	profile := resolveProfile(cmd)
	cfg, err := cli.LoadCLIConfigForProfile(profile)
	if err != nil {
		return err
	}

	// `multica setup` / `multica setup self-host` already do server_url +
	// app_url + login + daemon start in one step (including device-code
	// sign-in, which works without a browser on this machine — see
	// ConnectRemoteDialog's troubleshooting copy). Warn, don't block: this
	// command is also how self-host operators repoint an already-configured
	// install, which must keep working. The signal for "someone is hand-
	// rolling first-time setup instead of using it" is server_url going
	// from unset to set — that's the exact step `multica setup` replaces.
	if key == "server_url" && cfg.ServerURL == "" && value != "" {
		fmt.Fprintln(os.Stderr, "Note: `multica setup` (or `multica setup self-host --server-url ... --app-url ...`) configures this, signs you in, and starts the daemon in one step. Continuing with `config set` still works, but skips that.")
	}

	switch key {
	case "server_url":
		cfg.ServerURL = value
	case "app_url":
		cfg.AppURL = value
	case "workspace_id":
		cfg.WorkspaceID = value
	case "proxy.http":
		ensureProxyConfig(&cfg).HTTP = value
	case "proxy.https":
		ensureProxyConfig(&cfg).HTTPS = value
	case "proxy.no_proxy":
		ensureProxyConfig(&cfg).NoProxy = value
	default:
		return fmt.Errorf("unknown config key %q (supported: server_url, app_url, workspace_id, proxy.http, proxy.https, proxy.no_proxy)", key)
	}

	if err := cli.SaveCLIConfigForProfile(cfg, profile); err != nil {
		return err
	}

	if key == "proxy.http" || key == "proxy.https" {
		value = secretPresence(value)
	}
	fmt.Fprintf(os.Stderr, "Set %s = %s\n", key, value)
	return nil
}

func ensureProxyConfig(cfg *cli.CLIConfig) *cli.ProxyConfig {
	if cfg.Proxy == nil {
		cfg.Proxy = &cli.ProxyConfig{}
	}
	return cfg.Proxy
}

func secretPresence(v string) string {
	if v == "" {
		return "(not set)"
	}
	return "(set)"
}

func valueOrDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
