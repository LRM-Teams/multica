package cli

import (
	"fmt"
	"net/url"
	"strings"
)

// ServiceEnvironment names the deployment stage a single machine-wide
// Computer currently serves. It is intentionally separate from the binary
// release channel: selecting test must not implicitly install alpha builds.
type ServiceEnvironment string

const (
	ServiceEnvironmentProduction ServiceEnvironment = "production"
	ServiceEnvironmentTest       ServiceEnvironment = "test"
)

// ServiceTarget is the validated app/API coordinate for one explicit
// environment. Production uses the leagent.me product family with dedicated
// app and API hosts; test accepts one caller-supplied origin (for example a
// Tencent Cloud IP or test.leagent.me) for both surfaces.
type ServiceTarget struct {
	Environment ServiceEnvironment
	Origin      string // API/auth/WebSocket origin
	AppOrigin   string // human-facing browser origin
}

// NewServiceTarget validates the public setup choice. testOrigin must be
// empty for production and is required for test.
func NewServiceTarget(environment, testOrigin string) (ServiceTarget, error) {
	env := ServiceEnvironment(strings.ToLower(strings.TrimSpace(environment)))
	if env == "" {
		env = ServiceEnvironmentProduction
	}
	switch env {
	case ServiceEnvironmentProduction:
		if strings.TrimSpace(testOrigin) != "" {
			return ServiceTarget{}, fmt.Errorf("--test-url is only valid with --environment test")
		}
		return ServiceTarget{Environment: env, Origin: OfficialCloudAPIURL, AppOrigin: OfficialCloudAppURL}, nil
	case ServiceEnvironmentTest:
		origin, err := NormalizeTestServiceOrigin(testOrigin)
		if err != nil {
			return ServiceTarget{}, err
		}
		return ServiceTarget{Environment: env, Origin: origin, AppOrigin: origin}, nil
	default:
		return ServiceTarget{}, fmt.Errorf("unsupported environment %q: use production or test", environment)
	}
}

// ResolveServiceTarget validates persisted configuration. A pre-environment
// config pointing at leagent.me is treated as production. Historical custom
// server configs are not silently promoted to test because doing so would
// turn an old arbitrary redirect into a trusted Computer destination.
func ResolveServiceTarget(cfg CLIConfig) (ServiceTarget, error) {
	env := ServiceEnvironment(strings.ToLower(strings.TrimSpace(cfg.Environment)))
	serverURL := strings.TrimRight(strings.TrimSpace(cfg.ServerURL), "/")
	appURL := strings.TrimRight(strings.TrimSpace(cfg.AppURL), "/")
	if env == "" {
		if serverURL == "" || CanonicalizeOfficialCloudAPIURL(serverURL) == OfficialCloudAPIURL {
			env = ServiceEnvironmentProduction
		} else {
			return ServiceTarget{}, fmt.Errorf("legacy custom server %q is not a Computer environment; run `multica setup --environment test --test-url <url> /<workspace>` explicitly", serverURL)
		}
	}
	switch env {
	case ServiceEnvironmentProduction:
		if serverURL != "" && CanonicalizeOfficialCloudAPIURL(serverURL) != OfficialCloudAPIURL {
			return ServiceTarget{}, fmt.Errorf("production is fixed to %s", OfficialCloudAPIURL)
		}
		if appURL != "" && appURL != OfficialCloudAppURL && appURL != "https://leagent.me" {
			return ServiceTarget{}, fmt.Errorf("production app is fixed to %s", OfficialCloudAppURL)
		}
		return ServiceTarget{Environment: env, Origin: OfficialCloudAPIURL, AppOrigin: OfficialCloudAppURL}, nil
	case ServiceEnvironmentTest:
		origin, err := NormalizeTestServiceOrigin(serverURL)
		if err != nil {
			return ServiceTarget{}, err
		}
		if appURL != "" && appURL != origin {
			return ServiceTarget{}, fmt.Errorf("test app_url must use the same origin as server_url")
		}
		return ServiceTarget{Environment: env, Origin: origin, AppOrigin: origin}, nil
	default:
		return ServiceTarget{}, fmt.Errorf("unsupported environment %q: use production or test", cfg.Environment)
	}
}

// NormalizeTestServiceOrigin accepts an HTTP(S) origin only. A bare IP is
// deliberately rejected so users must state whether the Tencent Cloud stage
// is using TLS. Paths, query strings, fragments, and embedded credentials are
// forbidden because a Computer environment is an origin, not an endpoint.
func NormalizeTestServiceOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("--test-url is required with --environment test")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("invalid test URL %q: use an absolute http(s) URL", raw)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("invalid test URL %q: scheme must be http or https", raw)
	}
	if parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid test URL %q: provide only the origin, without credentials, path, query, or fragment", raw)
	}
	origin := parsed.Scheme + "://" + parsed.Host
	if IsOfficialCloudHost(parsed.Hostname()) {
		return "", fmt.Errorf("%s belongs to the production environment; select --environment production", origin)
	}
	return origin, nil
}
