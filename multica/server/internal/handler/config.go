package handler

import (
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/multica-ai/multica/server/internal/analytics"
)

type AppConfig struct {
	CdnDomain string `json:"cdn_domain"`
	// Public auth config consumed by the web app at runtime so self-hosted
	// deployments do not need to rebuild the frontend image when operators
	// toggle signup or wire Google OAuth.
	AllowSignup    bool   `json:"allow_signup"`
	GoogleClientID string `json:"google_client_id,omitempty"`
	// WorkspaceCreationDisabled mirrors the server-side
	// DISABLE_WORKSPACE_CREATION env var so the UI can hide every
	// "Create workspace" affordance on self-hosted instances. Omitted
	// from the JSON when false to keep responses identical to the
	// previous shape for the common managed-cloud case (#3433).
	WorkspaceCreationDisabled bool `json:"workspace_creation_disabled,omitempty"`
	// Public daemon setup config consumed by the web app at runtime so
	// self-hosted instances can show `multica setup self-host` commands
	// with the operator's own domains instead of Multica Cloud defaults.
	DaemonServerURL string `json:"daemon_server_url,omitempty"`
	DaemonAppURL    string `json:"daemon_app_url,omitempty"`

	// PostHog public config for the frontend. The key is the same Project
	// API Key the backend uses; returning it here (instead of baking it
	// into the frontend bundle via NEXT_PUBLIC_*) means self-hosted
	// instances — whose server returns an empty key — automatically
	// disable frontend event shipping too.
	PosthogKey           string `json:"posthog_key"`
	PosthogHost          string `json:"posthog_host"`
	AnalyticsEnvironment string `json:"analytics_environment"`

	// DevAgentProfileAccessEnabled opens other agents' side-panel Activity
	// and Files read surfaces in development environments only. Production
	// keeps the owner-only profile gate by default.
	DevAgentProfileAccessEnabled bool `json:"dev_agent_profile_access_enabled"`
}

// GetConfig is mounted on the public (unauthenticated) route group because
// the web app calls it before login to decide whether to render the Google
// sign-in button and signup UI. Only add fields here that are safe to expose
// to anonymous callers — never user- or tenant-scoped data.
func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	config := AppConfig{
		AllowSignup:                  os.Getenv("ALLOW_SIGNUP") != "false",
		GoogleClientID:               os.Getenv("GOOGLE_CLIENT_ID"),
		WorkspaceCreationDisabled:    os.Getenv("DISABLE_WORKSPACE_CREATION") == "true",
		DevAgentProfileAccessEnabled: devAgentProfileAccessEnabled(),
	}
	if h.Storage != nil {
		config.CdnDomain = h.Storage.CdnDomain()
	}
	config.DaemonServerURL, config.DaemonAppURL = daemonSetupURLsFromEnv()

	// Re-read from env on every request so operators can rotate keys via
	// secret refresh without a server restart.
	if v := os.Getenv("ANALYTICS_DISABLED"); v != "true" && v != "1" {
		config.PosthogKey = os.Getenv("POSTHOG_API_KEY")
		config.PosthogHost = os.Getenv("POSTHOG_HOST")
		config.AnalyticsEnvironment = analytics.EnvironmentFromEnv()
		if config.PosthogHost == "" && config.PosthogKey != "" {
			config.PosthogHost = "https://us.i.posthog.com"
		}
	}

	writeJSON(w, http.StatusOK, config)
}

func devAgentProfileAccessEnabled() bool {
	if enabled, ok := boolEnv("MULTICA_DEV_AGENT_PROFILE_ACCESS"); ok {
		return enabled
	}
	return isExplicitDevEnvironment(os.Getenv("APP_ENV")) ||
		isExplicitDevEnvironment(os.Getenv("ANALYTICS_ENVIRONMENT"))
}

func boolEnv(name string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

func isExplicitDevEnvironment(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "dev", "development", "local", "test":
		return true
	default:
		return false
	}
}

// serverDispatchedReleaseManifestBaseURL is the server's current opinion of
// where daemons should download CLI release artifacts from, dispatched over
// every heartbeat ack (DaemonHeartbeatAckPayload.ReleaseManifestBaseURL).
// Empty means "server has no opinion" — the daemon falls through to its own
// MULTICA_RELEASE_MANIFEST_BASE_URL env var / compile-time default (task
// #815 step 2; step 1 was the daemon-side env var override in #1526). A
// plain env var is enough for v1: this is one global value, not
// per-workspace/per-daemon, and an env change + server restart is far
// cheaper than the alternative it replaces (reinstalling every machine).
func serverDispatchedReleaseManifestBaseURL() string {
	return strings.TrimSpace(os.Getenv("MULTICA_SERVER_RELEASE_MANIFEST_BASE_URL"))
}

func daemonSetupURLsFromEnv() (string, string) {
	serverURL := normalizePublicURL(os.Getenv("MULTICA_PUBLIC_URL"))
	appURL := normalizePublicURL(os.Getenv("MULTICA_APP_URL"))
	if appURL == "" {
		appURL = normalizePublicURL(os.Getenv("FRONTEND_ORIGIN"))
	}
	if appURL == "" {
		return "", ""
	}

	if serverURL == "" {
		serverURL = appURL
	}
	if isOfficialCloudDaemonConfig(appURL) {
		return "", ""
	}
	return serverURL, appURL
}

func normalizePublicURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// isOfficialCloudDaemonConfig reports whether this deployment is the official
// Multica Cloud, identified by its frontend host alone (multica.ai /
// app.multica.ai). The daemon setup for the managed cloud is always
// `multica setup` (which hardcodes api.multica.ai), so the per-deployment URLs
// must be omitted from /api/config even when MULTICA_PUBLIC_URL is unset or
// misconfigured. Previously this also required serverURL==api.multica.ai, so a
// cloud deployment that forgot MULTICA_PUBLIC_URL fell through and emitted a
// `setup self-host --server-url https://multica.ai` command — pointing the
// daemon's backend at the frontend (no /health, no WebSocket proxy).
func isOfficialCloudDaemonConfig(appURL string) bool {
	return urlHostEquals(appURL, "multica.ai") || urlHostEquals(appURL, "app.multica.ai")
}

func urlHostEquals(raw, want string) bool {
	host := canonicalURLHost(raw)
	if host == "" {
		return false
	}
	want = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(want)), ".")
	return host == want
}

func canonicalURLHost(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if host == "" && !strings.Contains(raw, "://") {
		u, err = url.Parse("https://" + raw)
		if err != nil {
			return ""
		}
		host = u.Hostname()
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}
