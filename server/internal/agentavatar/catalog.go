package agentavatar

import (
	"crypto/md5" // #nosec G501 -- distribution only, never security-sensitive.
	"crypto/sha256"
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
	LegacySourceBaseURL = "https://www.leagent.me/agent-avatars"
	objectPrefix        = "agent-avatars/v2"
	legacyObjectPrefix  = "agent-avatars/v1"
)

var (
	canonicalPreset       = regexp.MustCompile(`^https://cdn\.leagent\.me/agent-avatars/v2/agent-(0[1-9]|1[0-5])\.png$`)
	canonicalLegacyPreset = regexp.MustCompile(`^https://cdn\.leagent\.me/agent-avatars/v1/human-(0[1-9]|1[0-9]|2[0-4])\.jpg$`)
	legacyPreset          = regexp.MustCompile(`^/agent-avatars/human-(0[1-9]|1[0-9]|2[0-4])\.jpg$`)
)

//go:embed assets/agent-*.png
var presetAssets embed.FS

var legacySHA256 = [...]string{
	"91a21befa97aecf19a4073a229258ebe342ec2ae32272e80b42bdb7265da1912",
	"03b54f387eff0acdbcb7f355e31357f99c7cd88e737ea85d19df4eec4fa0c0f6",
	"2a954219cd8335c6390c4be3b6b9a789b8486da56c737e4215f5bf3e39dfa117",
	"368948b6d5c1dc2af66d0bf8883c5fdc8b14158f89615cde8e1bf2266b6db5ab",
	"5afc0e3a35a52060d6662494c2933adf07b97eba441d73e0655b98070b4a8759",
	"918d2e7d5869a87aa72d8d2e9ec62be70ebf6acc69f1eeb9ff3ba641f4f2ccd2",
	"dd7ba0a7a7827599534c760564af89a52cfe18b76cdbb26bb6cb500fb6681ad9",
	"58fa6d4b0be40294f41c5dea5d938e4d78b0e4672f9b9255fff10cd58dcd90cd",
	"c52cc845fc834ef935ededc096b0d0c6e1a2a7eb9e8e587b54e67f6f398c74c8",
	"6a2278f0f24be0062481cf0e2e48927bec072f8eb0d68e9ecf888d70453d7d86",
	"42e1adf6f695fc8f4980ee570622fd2d37374d810b48aa7b631a50564a3c61bc",
	"54cafa5e22b1c9b20b5812882a2a7d09309ccae2c22654936c43b19e1126fa72",
	"d074036c244a99594defbac2172211c36edb6dfd24a48c476f9be958f1e17db8",
	"bbf581e644887814e517dbf12f7fb15bc0965086a55084d68d1ddce487e92434",
	"40a31dd62e2073f573ed2b8e020161cfe817154c9acad2237127d9219ee3a52e",
	"dd475457ea10ff03ffef8d2cdc2f53d6def82267758dcf1995b65f24884f46e9",
	"0408c31fee6c8e433eff15ad042139af777b31c3b6a0d18157701dca83943ebd",
	"a3de22ef5c08e0cbc6deb44eebbb7f7203d0c2b9fb3a36cb307e080e9a3442dc",
	"68f3fe7a88c50f84a86ce2b29ac0178eeb5c564f16bea105c30f72194222f0c5",
	"49d4928f04c6da9f59f2f5f645a0d8e8e6c86ca2f080490dba13c6201543168c",
	"064269a05b59a90dcab0b49070ee3a4b287eb81c79403a81737968ef307fc2f8",
	"43b97e328383d6d886e3f050d53db575b92d153b8562b6e098d2a58c0c29a7c6",
	"9f6c5e25e5a1abf01c66c04d9206d964fa93e08d1dbfa352723ab10dc8926e6f",
	"34e36b4321c7fecf53d41d84019afeab10bcc94864c50cc16201df7e30581a4b",
}

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
	validateLegacyNumber(number)
	return fmt.Sprintf("%s/human-%02d.jpg", LegacyPublicBaseURL, number)
}

func LegacyAsset(number int, data []byte) (Asset, error) {
	validateLegacyNumber(number)
	got := fmt.Sprintf("%x", sha256.Sum256(data))
	if want := legacySHA256[number-1]; got != want {
		return Asset{}, fmt.Errorf("legacy agent avatar %02d sha256 = %s, want %s", number, got, want)
	}
	name := fmt.Sprintf("human-%02d.jpg", number)
	return Asset{
		Name:        name,
		Key:         legacyObjectPrefix + "/" + name,
		URL:         LegacyURL(number),
		ContentType: "image/jpeg",
		Data:        data,
	}, nil
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

	return current, nil
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
