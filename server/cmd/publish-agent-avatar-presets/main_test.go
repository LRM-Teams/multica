package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadLegacyAssetRequiresPinnedBytes(t *testing.T) {
	t.Parallel()

	want, err := os.ReadFile(filepath.Join("..", "..", "..", "apps", "web", "public", "agent-avatars", "human-01.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/human-01.jpg" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(want)
	}))
	defer server.Close()

	asset, err := downloadLegacyAsset(context.Background(), server.Client(), server.URL, 1)
	if err != nil {
		t.Fatal(err)
	}
	if asset.Name != "human-01.jpg" || asset.Key != "agent-avatars/v1/human-01.jpg" {
		t.Fatalf("asset metadata = %#v", asset)
	}

	badServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not the pinned legacy avatar"))
	}))
	defer badServer.Close()
	if _, err := downloadLegacyAsset(context.Background(), badServer.Client(), badServer.URL, 1); err == nil {
		t.Fatal("downloadLegacyAsset accepted bytes outside the pinned manifest")
	}
}
