package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log"

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
	for _, asset := range assets {
		if _, err := store.UploadImmutable(ctx, asset.Key, asset.Data, asset.ContentType, asset.Name); err != nil {
			log.Fatalf("publish %s: %v", asset.Key, err)
		}
		reader, err := store.GetReader(ctx, asset.Key)
		if err != nil {
			log.Fatalf("verify %s: %v", asset.Key, err)
		}
		stored, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			log.Fatalf("verify %s: read=%v close=%v", asset.Key, readErr, closeErr)
		}
		if !bytes.Equal(stored, asset.Data) {
			want := sha256.Sum256(asset.Data)
			got := sha256.Sum256(stored)
			log.Fatalf("verify %s: sha256=%x, want %x", asset.Key, got, want)
		}
		fmt.Printf("published %s -> %s\n", asset.Key, asset.URL)
	}
}
