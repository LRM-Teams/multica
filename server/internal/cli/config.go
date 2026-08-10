package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultCLIConfigPath = ".multica/config.json"

// CLIConfig holds persistent CLI settings.
type CLIConfig struct {
	// Environment and the four fields below are the effective, active
	// environment projection consumed by existing CLI and resident callers.
	// SaveCLIConfig writes that projection back into Environments[Environment]
	// rather than persisting a second flat copy.
	Environment string `json:"-"`
	ServerURL   string `json:"-"`
	AppURL      string `json:"-"`
	WorkspaceID string `json:"-"`
	Token       string `json:"-"`

	// Environments keeps independently authenticated production and test
	// sessions. Exactly one is projected into the effective fields above. The
	// resident still owns Workspace execution credentials in bindings.json;
	// this map is only human/CLI configuration and login state.
	Environments map[string]ServiceEnvironmentConfig `json:"-"`

	// Proxy contains machine-local daemon egress overrides. Environment
	// variables remain authoritative; these values are translated into the
	// standard HTTP_PROXY / HTTPS_PROXY / NO_PROXY family during daemon
	// startup when no corresponding proxy env is present.
	Proxy *ProxyConfig `json:"proxy,omitempty"`

	// Backends contains per-backend overrides for users who want to point
	// the daemon at non-default tool installations (e.g. an OpenClaw bundled
	// inside another desktop app, or multiple isolated profiles on the same
	// machine). Empty / absent means "discover from PATH and use vendor
	// defaults" — the historical behavior. See issue #3875.
	Backends *BackendOverrides `json:"backends,omitempty"`
}

// ServiceEnvironmentConfig is the saved CLI/session configuration for one
// service environment. The environment name itself is the map key.
type ServiceEnvironmentConfig struct {
	ServerURL   string `json:"server_url"`
	AppURL      string `json:"app_url"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Token       string `json:"token,omitempty"`
}

// cliConfigFile is the versioned-on-write config document. Legacy flat fields
// are read for migration only; new writes use active_environment plus
// environments so switching does not destroy the inactive environment's
// session. release_channel is intentionally legacy-read-only: package source
// is fixed by environment and is never a user-configurable axis.
type cliConfigFile struct {
	ActiveEnvironment string                              `json:"active_environment,omitempty"`
	Environments      map[string]ServiceEnvironmentConfig `json:"environments,omitempty"`
	Proxy             *ProxyConfig                        `json:"proxy,omitempty"`
	Backends          *BackendOverrides                   `json:"backends,omitempty"`

	LegacyEnvironment    string `json:"environment,omitempty"`
	LegacyReleaseChannel string `json:"release_channel,omitempty"`
	LegacyServerURL      string `json:"server_url,omitempty"`
	LegacyAppURL         string `json:"app_url,omitempty"`
	LegacyWorkspaceID    string `json:"workspace_id,omitempty"`
	LegacyToken          string `json:"token,omitempty"`
}

// ProxyConfig configures standard outbound HTTP(S) proxy behavior for the
// daemon and the child processes it launches. HTTP and HTTPS values may carry
// credentials, so callers must never print them verbatim.
type ProxyConfig struct {
	HTTP    string `json:"http,omitempty"`
	HTTPS   string `json:"https,omitempty"`
	NoProxy string `json:"no_proxy,omitempty"`
}

// BackendOverrides holds per-backend configuration overrides. Each field is
// optional; nil means "no override for this backend". Keep new fields additive
// and tagged with `json:",omitempty"` so empty values do not change the saved
// config shape. Unknown-key preservation is a separate forward-compat concern:
// Go's encoding/json drops fields that are not represented in this struct on
// load/save round-trip (see TestCLIConfig_UnknownFieldsArePreserved).
type BackendOverrides struct {
	OpenClaw *OpenClawOverride `json:"openclaw,omitempty"`
}

// OpenClawOverride configures the OpenClaw backend. All fields are optional;
// empty values fall through to the existing discovery path (PATH lookup for
// BinaryPath, default `~/.openclaw/` for StateDir).
//
// Resolution precedence (env beats config beats default, for back-compat):
//
//	BinaryPath: MULTICA_OPENCLAW_PATH (env)  > backends.openclaw.binary_path > PATH lookup
//	StateDir:   OPENCLAW_STATE_DIR (env)     > backends.openclaw.state_dir   > OpenClaw's built-in default (~/.openclaw)
//
// The StateDir env var here is OpenClaw's own OPENCLAW_STATE_DIR — NOT a new
// MULTICA_OPENCLAW_STATE_DIR. Rationale: OpenClaw already honors its own env
// var, the daemon already forwards inherited env to spawned children via
// `mergeEnv`, and a user who exports OPENCLAW_STATE_DIR in their shell
// already gets the right behavior with zero daemon changes today. This field
// is purely additive: when set, the daemon injects OPENCLAW_STATE_DIR=<value>
// into the spawned child's env unless the user already exported one upstream.
// (If a future use case needs daemon-namespaced isolation distinct from
// OpenClaw's own env, MULTICA_OPENCLAW_STATE_DIR can be layered on top
// without breaking this contract — see #3875 discussion.)
//
// Setting StateDir is the fix for the long-standing usability gap where
// users with non-default OpenClaw installations — multiple isolated
// profiles (dev/staging/prod, multiple accounts), containerized / CI
// deployments where ~/.openclaw isn't writable, or third-party desktop
// apps that bundle their own OpenClaw runtime — had to write a wrapper
// shell script to inject OPENCLAW_STATE_DIR + run `launchctl setenv`
// for GUI-launched daemons. With this field, those workarounds become
// unnecessary.
type OpenClawOverride struct {
	BinaryPath string `json:"binary_path,omitempty"`
	StateDir   string `json:"state_dir,omitempty"`
}

func userHomeDir() (string, error) {
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return home, nil
	}
	return os.UserHomeDir()
}

// CLIConfigPathForProfile returns the config file path for the given profile.
// An empty profile returns the default path (~/.multica/config.json).
// A named profile returns ~/.multica/profiles/<name>/config.json.
func CLIConfigPathForProfile(profile string) (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve CLI config path: %w", err)
	}
	if profile == "" {
		return filepath.Join(home, defaultCLIConfigPath), nil
	}
	return filepath.Join(home, ".multica", "profiles", profile, "config.json"), nil
}

// ProfileDir returns the base directory for a profile's state files (pid, log).
// An empty profile returns ~/.multica/. A named profile returns ~/.multica/profiles/<name>/.
func ProfileDir(profile string) (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve profile dir: %w", err)
	}
	if profile == "" {
		return filepath.Join(home, ".multica"), nil
	}
	return filepath.Join(home, ".multica", "profiles", profile), nil
}

// LoadCLIConfig reads the CLI config from disk (default profile).
func LoadCLIConfig() (CLIConfig, error) {
	return LoadCLIConfigForProfile("")
}

// LoadCLIConfigForProfile reads the CLI config for the given profile.
func LoadCLIConfigForProfile(profile string) (CLIConfig, error) {
	path, err := CLIConfigPathForProfile(profile)
	if err != nil {
		return CLIConfig{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CLIConfig{}, nil
		}
		return CLIConfig{}, fmt.Errorf("read CLI config: %w", err)
	}
	var stored cliConfigFile
	if err := json.Unmarshal(data, &stored); err != nil {
		return CLIConfig{}, fmt.Errorf("parse CLI config: %w", err)
	}
	return projectCLIConfig(stored), nil
}

// SaveCLIConfig writes the CLI config to disk atomically (default profile).
func SaveCLIConfig(cfg CLIConfig) error {
	return SaveCLIConfigForProfile(cfg, "")
}

// SaveCLIConfigForProfile writes the CLI config for the given profile.
func SaveCLIConfigForProfile(cfg CLIConfig, profile string) error {
	path, err := CLIConfigPathForProfile(profile)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create CLI config directory: %w", err)
	}
	stored := persistCLIConfig(cfg)
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("encode CLI config: %w", err)
	}

	// Write to a temp file in the same directory, then rename for atomicity.
	tmp, err := os.CreateTemp(dir, ".config-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp config file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp config file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp config file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("chmod temp config file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename config file: %w", err)
	}
	return nil
}

func projectCLIConfig(stored cliConfigFile) CLIConfig {
	cfg := CLIConfig{
		Proxy:        stored.Proxy,
		Backends:     stored.Backends,
		Environments: cloneServiceEnvironments(stored.Environments),
	}
	if len(stored.Environments) > 0 {
		environment := strings.ToLower(strings.TrimSpace(stored.ActiveEnvironment))
		if environment == "" {
			environment = string(ServiceEnvironmentProduction)
		}
		cfg.Environment = environment
		cfg.applyEnvironment(stored.Environments[environment])
		return cfg
	}

	// Flat config migration. Preserve custom/self-host configurations as flat
	// effective values; only the two Cloud environments enter the new map.
	cfg.Environment = strings.ToLower(strings.TrimSpace(stored.LegacyEnvironment))
	cfg.ServerURL = stored.LegacyServerURL
	cfg.AppURL = stored.LegacyAppURL
	cfg.WorkspaceID = stored.LegacyWorkspaceID
	cfg.Token = stored.LegacyToken
	if environment, ok := cloudEnvironmentForLegacyConfig(cfg); ok {
		cfg.Environment = string(environment)
		cfg.Environments = map[string]ServiceEnvironmentConfig{
			cfg.Environment: cfg.effectiveEnvironment(),
		}
	}
	return cfg
}

func persistCLIConfig(cfg CLIConfig) cliConfigFile {
	stored := cliConfigFile{Proxy: cfg.Proxy, Backends: cfg.Backends}
	environment, ok := normalizeCloudEnvironment(cfg.Environment)
	if !ok {
		if inferred, inferredOK := cloudEnvironmentForLegacyConfig(cfg); inferredOK {
			environment, ok = inferred, true
		}
	}
	if !ok {
		// Retired named-profile/self-host callers retain their flat shape. The
		// machine-wide Cloud Computer never reaches this branch.
		stored.LegacyEnvironment = cfg.Environment
		stored.LegacyServerURL = cfg.ServerURL
		stored.LegacyAppURL = cfg.AppURL
		stored.LegacyWorkspaceID = cfg.WorkspaceID
		stored.LegacyToken = cfg.Token
		return stored
	}

	stored.ActiveEnvironment = string(environment)
	stored.Environments = cloneServiceEnvironments(cfg.Environments)
	if stored.Environments == nil {
		stored.Environments = make(map[string]ServiceEnvironmentConfig, 2)
	}
	stored.Environments[string(environment)] = cfg.effectiveEnvironment()
	return stored
}

func (cfg *CLIConfig) applyEnvironment(environment ServiceEnvironmentConfig) {
	cfg.ServerURL = environment.ServerURL
	cfg.AppURL = environment.AppURL
	cfg.WorkspaceID = environment.WorkspaceID
	cfg.Token = environment.Token
}

func (cfg CLIConfig) effectiveEnvironment() ServiceEnvironmentConfig {
	return ServiceEnvironmentConfig{
		ServerURL:   cfg.ServerURL,
		AppURL:      cfg.AppURL,
		WorkspaceID: cfg.WorkspaceID,
		Token:       cfg.Token,
	}
}

func cloneServiceEnvironments(in map[string]ServiceEnvironmentConfig) map[string]ServiceEnvironmentConfig {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]ServiceEnvironmentConfig, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func normalizeCloudEnvironment(raw string) (ServiceEnvironment, bool) {
	environment := ServiceEnvironment(strings.ToLower(strings.TrimSpace(raw)))
	switch environment {
	case ServiceEnvironmentProduction, ServiceEnvironmentTest:
		return environment, true
	default:
		return "", false
	}
}

func cloudEnvironmentForLegacyConfig(cfg CLIConfig) (ServiceEnvironment, bool) {
	if environment, ok := normalizeCloudEnvironment(cfg.Environment); ok {
		return environment, true
	}
	serverURL := strings.TrimRight(strings.TrimSpace(cfg.ServerURL), "/")
	if serverURL == "" || CanonicalizeOfficialCloudAPIURL(serverURL) == OfficialCloudAPIURL {
		return ServiceEnvironmentProduction, true
	}
	return "", false
}

// PutServiceEnvironment creates or repairs one saved Cloud environment and
// makes it the effective projection. Existing session/workspace fields for
// that environment are preserved unless the origin changed.
func (cfg *CLIConfig) PutServiceEnvironment(target ServiceTarget) {
	if cfg.Environments == nil {
		cfg.Environments = make(map[string]ServiceEnvironmentConfig, 2)
	}
	cfg.storeEffectiveEnvironment()
	key := string(target.Environment)
	next := cfg.Environments[key]
	if strings.TrimRight(next.ServerURL, "/") != target.Origin || strings.TrimRight(next.AppURL, "/") != target.AppOrigin {
		next.WorkspaceID = ""
		next.Token = ""
	}
	next.ServerURL = target.Origin
	next.AppURL = target.AppOrigin
	cfg.Environments[key] = next
	cfg.Environment = key
	cfg.applyEnvironment(next)
}

// ActivateServiceEnvironment selects an already-configured environment in
// memory. Callers must use the Computer switch flow before saving this change
// while a resident is live; this helper deliberately performs no process I/O.
func (cfg *CLIConfig) ActivateServiceEnvironment(environment ServiceEnvironment) error {
	environment, ok := normalizeCloudEnvironment(string(environment))
	if !ok {
		return fmt.Errorf("unsupported environment %q: use production or test", environment)
	}
	key := string(environment)
	next, exists := cfg.Environments[key]
	if !exists || strings.TrimSpace(next.ServerURL) == "" {
		return fmt.Errorf("%s environment is not configured; run `multica setup --environment %s /<workspace>` first", key, key)
	}
	cfg.storeEffectiveEnvironment()
	cfg.Environment = key
	cfg.applyEnvironment(next)
	return nil
}

func (cfg *CLIConfig) storeEffectiveEnvironment() {
	current, ok := normalizeCloudEnvironment(cfg.Environment)
	if !ok || cfg.Environments == nil {
		return
	}
	key := string(current)
	if _, exists := cfg.Environments[key]; !exists {
		return
	}
	cfg.Environments[key] = cfg.effectiveEnvironment()
}

// ConfiguredServiceEnvironments returns the saved Cloud environments in
// stable production/test order without exposing tokens.
func (cfg CLIConfig) ConfiguredServiceEnvironments() []ServiceEnvironment {
	out := make([]ServiceEnvironment, 0, 2)
	for _, environment := range []ServiceEnvironment{ServiceEnvironmentProduction, ServiceEnvironmentTest} {
		if saved, ok := cfg.Environments[string(environment)]; ok && strings.TrimSpace(saved.ServerURL) != "" {
			out = append(out, environment)
		}
	}
	return out
}
