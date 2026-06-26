package stickers_test

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/stickerimg"
	"github.com/multica-ai/multica/server/internal/stickers"
)

func TestCatalogMatchesAssetsOneToOne(t *testing.T) {
	assetFiles := make(map[string]bool)
	for _, name := range stickerimg.Names() {
		assetFiles[name] = true
	}
	if len(assetFiles) == 0 {
		t.Fatal("no embedded sticker images found")
	}

	catalogFiles := make(map[string]bool)
	ids := make(map[string]bool)
	for _, s := range stickers.All() {
		if ids[s.ID] {
			t.Errorf("duplicate catalog id: %s", s.ID)
		}
		ids[s.ID] = true
		if s.File == "" || !assetFiles[s.File] {
			t.Errorf("catalog id %q references missing asset %q", s.ID, s.File)
		}
		catalogFiles[s.File] = true
		if s.Name == "" || s.Emotion == "" || len(s.Tags) == 0 {
			t.Errorf("catalog id %q is missing name/emotion/tags", s.ID)
		}
	}
	for f := range assetFiles {
		if !catalogFiles[f] {
			t.Errorf("asset %q has no catalog entry", f)
		}
	}
	if len(ids) == 0 {
		t.Fatal("catalog is empty")
	}
}

func TestGet(t *testing.T) {
	known := stickers.All()[0].ID
	if _, ok := stickers.Get(known); !ok {
		t.Errorf("Get(%q) = not found; want found", known)
	}
	if _, ok := stickers.Get("does-not-exist"); ok {
		t.Error("Get(unknown) = found; want not found")
	}
}

func TestSearchByMoodAndKeyword(t *testing.T) {
	// Exact mood match leads the results.
	praise := stickers.Search("praise")
	if len(praise) == 0 || praise[0].Emotion != "praise" {
		t.Errorf("Search(praise) should lead with an emotion=praise sticker, got %v", ids(praise))
	}

	// Chinese keyword in tags resolves.
	if !containsID(stickers.Search("鼓掌"), "applause") {
		t.Errorf("Search(鼓掌) should include applause; got %v", ids(stickers.Search("鼓掌")))
	}

	// English keyword, case-insensitive.
	if len(stickers.Search("CELEBRATE")) == 0 {
		t.Error("Search(CELEBRATE) returned nothing; expected celebrate-mood stickers")
	}

	// Empty query returns the whole catalog.
	if len(stickers.Search("")) != len(stickers.All()) {
		t.Error("Search(empty) should return the full catalog")
	}

	// Nonsense query returns nothing.
	if got := stickers.Search("zzz-no-such-sticker-zzz"); len(got) != 0 {
		t.Errorf("Search(nonsense) = %v; want empty", ids(got))
	}
}

func containsID(list []stickers.Sticker, id string) bool {
	for _, s := range list {
		if s.ID == id {
			return true
		}
	}
	return false
}

func ids(list []stickers.Sticker) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = s.ID
	}
	return out
}
