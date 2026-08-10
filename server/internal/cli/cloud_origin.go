package cli

import (
	"net/url"
	"strings"
)

const (
	// OfficialCloudAPIHost is the production API, auth, attachment, and
	// WebSocket host used by Multica Cloud clients.
	OfficialCloudAPIHost = "api.leagent.me"
	OfficialCloudAPIURL  = "https://" + OfficialCloudAPIHost

	// OfficialCloudAppURL is the human-facing production web application.
	OfficialCloudAppURL = "https://www.leagent.me"
)

// CanonicalizeOfficialCloudAPIURL migrates the two historical web origins to
// the dedicated Cloud API origin. Custom/self-host/test origins are returned
// unchanged and remain configurable through the normal CLI flags and env.
func CanonicalizeOfficialCloudAPIURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	switch strings.ToLower(u.Hostname()) {
	case "leagent.me", "www.leagent.me":
		u.Scheme = "https"
		u.Host = OfficialCloudAPIHost
		return strings.TrimRight(u.String(), "/")
	default:
		return raw
	}
}

// IsOfficialCloudHost includes historical hosts so old profiles can be
// recognized and migrated without treating test/custom origins as Cloud.
func IsOfficialCloudHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case OfficialCloudAPIHost, "leagent.me", "www.leagent.me":
		return true
	default:
		return false
	}
}
