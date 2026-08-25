package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log"

	"github.com/multica-ai/multica/server/internal/agentavatar"
	"github.com/multica-ai/multica/server/internal/staticasset"
	"github.com/multica-ai/multica/server/internal/storage"
)

func main() {
	ctx := context.Background()
	store := storage.NewS3StorageFromEnv()
	if store == nil {
		log.Fatal("S3_BUCKET is required to publish agent avatars")
	}
	assets, err := agentavatar.Assets()
	if err != nil {
		log.Fatal(err)
	}
	for _, asset := range assets {
		if err := publishAndVerify(ctx, store, asset); err != nil {
			log.Fatal(err)
		}
	}
}

func publishAndVerify(ctx context.Context, store *storage.S3Storage, asset staticasset.Asset) error {
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
