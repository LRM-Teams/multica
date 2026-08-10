package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/agentavatar"
	"github.com/multica-ai/multica/server/internal/storage"
)

func main() {
	ctx := context.Background()
	store := storage.NewS3StorageFromEnv()
	if store == nil {
		log.Fatal("S3_BUCKET is required to publish agent avatar presets")
	}
	assets, err := agentavatar.Assets()
	if err != nil {
		log.Fatal(err)
	}
	legacyAssets, err := legacyAssetsNeedingPublish(ctx, store, &http.Client{Timeout: 30 * time.Second}, agentavatar.LegacySourceBaseURL)
	if err != nil {
		log.Fatal(err)
	}
	assets = append(legacyAssets, assets...)
	for _, asset := range assets {
		if err := publishAndVerify(ctx, store, asset); err != nil {
			log.Fatal(err)
		}
	}
}

func legacyAssetsNeedingPublish(
	ctx context.Context,
	store *storage.S3Storage,
	client *http.Client,
	sourceBaseURL string,
) ([]agentavatar.Asset, error) {
	assets := make([]agentavatar.Asset, 0, agentavatar.LegacyPresetCount)
	for number := 1; number <= agentavatar.LegacyPresetCount; number++ {
		name := fmt.Sprintf("human-%02d.jpg", number)
		key := "agent-avatars/v1/" + name
		reader, err := store.GetReader(ctx, key)
		if err == nil {
			stored, err := readAndClose(reader)
			if err != nil {
				return nil, fmt.Errorf("read existing %s: %w", key, err)
			}
			if _, err := agentavatar.LegacyAsset(number, stored); err != nil {
				return nil, fmt.Errorf("verify existing %s: %w", key, err)
			}
			fmt.Printf("verified existing %s -> %s\n", key, agentavatar.LegacyURL(number))
			continue
		}

		asset, err := downloadLegacyAsset(ctx, client, sourceBaseURL, number)
		if err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	return assets, nil
}

func downloadLegacyAsset(ctx context.Context, client *http.Client, sourceBaseURL string, number int) (agentavatar.Asset, error) {
	url := fmt.Sprintf("%s/human-%02d.jpg", strings.TrimRight(sourceBaseURL, "/"), number)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return agentavatar.Asset{}, fmt.Errorf("build legacy avatar request %02d: %w", number, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return agentavatar.Asset{}, fmt.Errorf("download legacy avatar %02d: %w", number, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return agentavatar.Asset{}, fmt.Errorf("download legacy avatar %02d: status %s", number, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024+1))
	if err != nil {
		return agentavatar.Asset{}, fmt.Errorf("download legacy avatar %02d: %w", number, err)
	}
	if len(data) > 5*1024*1024 {
		return agentavatar.Asset{}, fmt.Errorf("download legacy avatar %02d: exceeds 5 MiB", number)
	}
	asset, err := agentavatar.LegacyAsset(number, data)
	if err != nil {
		return agentavatar.Asset{}, fmt.Errorf("validate downloaded legacy avatar %02d: %w", number, err)
	}
	return asset, nil
}

func publishAndVerify(ctx context.Context, store *storage.S3Storage, asset agentavatar.Asset) error {
	if _, err := store.UploadImmutable(ctx, asset.Key, asset.Data, asset.ContentType, asset.Name); err != nil {
		return fmt.Errorf("publish %s: %w", asset.Key, err)
	}
	reader, err := store.GetReader(ctx, asset.Key)
	if err != nil {
		return fmt.Errorf("verify %s: %w", asset.Key, err)
	}
	stored, err := readAndClose(reader)
	if err != nil {
		return fmt.Errorf("verify %s: %w", asset.Key, err)
	}
	if !bytes.Equal(stored, asset.Data) {
		want := sha256.Sum256(asset.Data)
		got := sha256.Sum256(stored)
		return fmt.Errorf("verify %s: sha256=%x, want %x", asset.Key, got, want)
	}
	fmt.Printf("published %s -> %s\n", asset.Key, asset.URL)
	return nil
}

func readAndClose(reader io.ReadCloser) ([]byte, error) {
	data, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		return nil, fmt.Errorf("read=%v close=%v", readErr, closeErr)
	}
	return data, nil
}
