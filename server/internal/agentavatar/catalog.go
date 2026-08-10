package agentavatar

import (
	"crypto/md5" // #nosec G501 -- distribution only, never security-sensitive.
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	PresetCount         = 15
	LegacyPresetCount   = 24
	PublicBaseURL       = "https://cdn.leagent.me/agent-avatars/v2"
	LegacyPublicBaseURL = "https://cdn.leagent.me/agent-avatars/v1"
	objectPrefix        = "agent-avatars/v2"
	legacyObjectPrefix  = "agent-avatars/v1"
)

var (
	canonicalPreset       = regexp.MustCompile(`^https://cdn\.leagent\.me/agent-avatars/v2/agent-(0[1-9]|1[0-5])\.png$`)
	canonicalLegacyPreset = regexp.MustCompile(`^https://cdn\.leagent\.me/agent-avatars/v1/human-(0[1-9]|1[0-9]|2[0-4])\.jpg$`)
	legacyPreset          = regexp.MustCompile(`^/agent-avatars/human-(0[1-9]|1[0-9]|2[0-4])\.jpg$`)
)

//go:embed assets/agent-*.png assets/legacy/human-*.jpg
var presetAssets embed.FS

type Asset struct {
	Name        string
	Key         string
	URL         string
	ContentType string
	Data        []byte
}

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
	if number < 1 || number > LegacyPresetCount {
		panic(fmt.Sprintf("legacy agent avatar preset number %d outside 1..%d", number, LegacyPresetCount))
	}
	return fmt.Sprintf("%s/human-%02d.jpg", LegacyPublicBaseURL, number)
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

func Assets() ([]Asset, error) {
	current, err := readAssetGroup("assets", ".png", objectPrefix, PublicBaseURL, "image/png")
	if err != nil {
		return nil, err
	}
	if len(current) != PresetCount {
		return nil, fmt.Errorf("embedded current agent avatar count = %d, want %d", len(current), PresetCount)
	}
	for index, asset := range current {
		wantName := fmt.Sprintf("agent-%02d.png", index+1)
		if asset.Name != wantName {
			return nil, fmt.Errorf("embedded current agent avatar %d = %q, want %q", index, asset.Name, wantName)
		}
	}

	legacy, err := readAssetGroup("assets/legacy", ".jpg", legacyObjectPrefix, LegacyPublicBaseURL, "image/jpeg")
	if err != nil {
		return nil, err
	}
	if len(legacy) != LegacyPresetCount {
		return nil, fmt.Errorf("embedded legacy agent avatar count = %d, want %d", len(legacy), LegacyPresetCount)
	}
	for index, asset := range legacy {
		wantName := fmt.Sprintf("human-%02d.jpg", index+1)
		if asset.Name != wantName {
			return nil, fmt.Errorf("embedded legacy agent avatar %d = %q, want %q", index, asset.Name, wantName)
		}
	}
	return append(legacy, current...), nil
}

func readAssetGroup(directory, suffix, keyPrefix, publicBaseURL, contentType string) ([]Asset, error) {
	entries, err := fs.ReadDir(presetAssets, directory)
	if err != nil {
		return nil, fmt.Errorf("read embedded agent avatar assets from %s: %w", directory, err)
	}
	assets := make([]Asset, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		data, err := presetAssets.ReadFile(directory + "/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read embedded agent avatar %s: %w", entry.Name(), err)
		}
		assets = append(assets, Asset{
			Name:        entry.Name(),
			Key:         keyPrefix + "/" + entry.Name(),
			URL:         publicBaseURL + "/" + entry.Name(),
			ContentType: contentType,
			Data:        data,
		})
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Name < assets[j].Name })
	return assets, nil
}
