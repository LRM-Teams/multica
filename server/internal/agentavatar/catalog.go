package agentavatar

import (
	"crypto/md5" // #nosec G501 -- distribution only, never security-sensitive.
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	PresetCount         = 15
	LegacyPresetCount   = 24
	PublicBaseURL       = "https://cdn.leagent.me/agent-avatars/v2"
	LegacyPublicBaseURL = "https://cdn.leagent.me/agent-avatars/v1"
)

var (
	canonicalPreset       = regexp.MustCompile(`^https://cdn\.leagent\.me/agent-avatars/v2/agent-(0[1-9]|1[0-5])\.png$`)
	canonicalLegacyPreset = regexp.MustCompile(`^https://cdn\.leagent\.me/agent-avatars/v1/human-(0[1-9]|1[0-9]|2[0-4])\.jpg$`)
	legacyPreset          = regexp.MustCompile(`^/agent-avatars/human-(0[1-9]|1[0-9]|2[0-4])\.jpg$`)
)

func URL(number int) string {
	if number < 1 || number > PresetCount {
		panic(fmt.Sprintf("agent avatar preset number %d outside 1..%d", number, PresetCount))
	}
	return fmt.Sprintf("%s/agent-%02d.png", PublicBaseURL, number)
}

func URLs() []string {
	urls := make([]string, PresetCount)
	for index := range urls {
		urls[index] = URL(index + 1)
	}
	return urls
}

func LegacyURL(number int) string {
	validateLegacyNumber(number)
	return fmt.Sprintf("%s/human-%02d.jpg", LegacyPublicBaseURL, number)
}

func validateLegacyNumber(number int) {
	if number < 1 || number > LegacyPresetCount {
		panic(fmt.Sprintf("legacy agent avatar preset number %d outside 1..%d", number, LegacyPresetCount))
	}
}

// DefaultURL returns one stable, evenly distributed preset for an Agent ID.
// Persisting this result keeps the face stable even if the catalog changes.
func DefaultURL(agentID string) string {
	sum := md5.Sum([]byte(agentID)) // #nosec G401 -- distribution only.
	return URL(int(sum[0])%PresetCount + 1)
}

// CanonicalizeSelection validates current preset URLs and maps the legacy
// bundled 24-photo catalog submitted by older clients onto the OSS catalog.
func CanonicalizeSelection(rawURL string) (string, bool) {
	if rawURL != strings.TrimSpace(rawURL) {
		return "", false
	}
	if canonicalPreset.MatchString(rawURL) {
		return rawURL, true
	}
	if canonicalLegacyPreset.MatchString(rawURL) {
		return rawURL, true
	}
	match := legacyPreset.FindStringSubmatch(rawURL)
	if match == nil {
		return "", false
	}
	legacyNumber, err := strconv.Atoi(match[1])
	if err != nil {
		return "", false
	}
	return LegacyURL(legacyNumber), true
}
