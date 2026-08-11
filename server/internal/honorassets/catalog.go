package honorassets

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"

	"github.com/multica-ai/multica/server/internal/staticasset"
)

const (
	UserLevelCount  = 80
	AgentLevelCount = 30
	AssetCount      = UserLevelCount + AgentLevelCount + 1
	PublicBaseURL   = "https://cdn.leagent.me/honor-assets/v1"
	objectPrefix    = "honor-assets/v1"
)

type Asset = staticasset.Asset

//go:embed assets/users/*.webp assets/agents/*.webp assets/honor-center-orbit.webp
var catalogAssets embed.FS

func Assets() ([]Asset, error) {
	userLevels, err := readLevelGroup(
		"assets/users",
		"user-honor-level",
		UserLevelCount,
		objectPrefix+"/users",
		PublicBaseURL+"/users",
	)
	if err != nil {
		return nil, err
	}
	agentLevels, err := readLevelGroup(
		"assets/agents",
		"agent-honor-level",
		AgentLevelCount,
		objectPrefix+"/agents",
		PublicBaseURL+"/agents",
	)
	if err != nil {
		return nil, err
	}
	hero, err := readAsset(
		"assets/honor-center-orbit.webp",
		objectPrefix+"/honor-center-orbit.webp",
		PublicBaseURL+"/honor-center-orbit.webp",
	)
	if err != nil {
		return nil, err
	}

	assets := make([]Asset, 0, AssetCount)
	assets = append(assets, userLevels...)
	assets = append(assets, agentLevels...)
	assets = append(assets, hero)
	return assets, nil
}

func readLevelGroup(directory, filenamePrefix string, count int, keyPrefix, publicBaseURL string) ([]Asset, error) {
	entries, err := fs.ReadDir(catalogAssets, directory)
	if err != nil {
		return nil, fmt.Errorf("read embedded honor assets from %s: %w", directory, err)
	}
	assets := make([]Asset, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".webp" {
			continue
		}
		asset, err := readAsset(
			directory+"/"+entry.Name(),
			keyPrefix+"/"+entry.Name(),
			publicBaseURL+"/"+entry.Name(),
		)
		if err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Name < assets[j].Name })
	if len(assets) != count {
		return nil, fmt.Errorf("embedded %s asset count = %d, want %d", filenamePrefix, len(assets), count)
	}
	for index, asset := range assets {
		wantName := fmt.Sprintf("%s-%02d.webp", filenamePrefix, index+1)
		if asset.Name != wantName {
			return nil, fmt.Errorf("embedded %s asset %d = %q, want %q", filenamePrefix, index, asset.Name, wantName)
		}
	}
	return assets, nil
}

func readAsset(embeddedPath, key, url string) (Asset, error) {
	data, err := catalogAssets.ReadFile(embeddedPath)
	if err != nil {
		return Asset{}, fmt.Errorf("read embedded honor asset %s: %w", embeddedPath, err)
	}
	return Asset{
		Name:        path.Base(embeddedPath),
		Key:         key,
		URL:         url,
		ContentType: "image/webp",
		Data:        data,
	}, nil
}
