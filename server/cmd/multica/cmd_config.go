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
	configCmd.AddCommand(configUseCmd)
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
	if cfg.Environment != "" {
		fmt.Fprintf(os.Stdout, "active_environment: %s\n", cfg.Environment)
		fmt.Fprintf(os.Stdout, "package_source:     %s\n", packageSourceName(cli.ReleaseChannelForEnvironment(cli.ServiceEnvironment(cfg.Environment))))
		for _, environment := range cfg.ConfiguredServiceEnvironments() {
			saved := cfg.Environments[string(environment)]
			marker := "saved"
			if cfg.Environment == string(environment) {
				marker = "active"
			}
			fmt.Fprintf(os.Stdout, "%s: %s, %s packages, login %s, origin %s\n",
				environment,
				marker,
				packageSourceName(cli.ReleaseChannelForEnvironment(environment)),
				secretPresence(saved.Token),
				saved.ServerURL,
			)
		}
	}
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

	// A machine-wide Computer selects its service through `multica setup`.
	// Keep this lower-level setting for server administration and older clients,
	// but do not advertise the retired profile/self-host Computer lifecycle.
	if key == "server_url" && cfg.ServerURL == "" && value != "" {
		fmt.Fprintln(os.Stderr, "Note: the supported Computer flow is `multica setup /<workspace>` for production, or `multica setup --environment test --test-url <origin> /<workspace>` for test. `config set server_url` does not create a Workspace connection or start the Computer.")
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
