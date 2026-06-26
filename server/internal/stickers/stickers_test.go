package stickers

import (
	"io/fs"
	"strings"
	"testing"
)

func TestCatalogMatchesAssetsOneToOne(t *testing.T) {
	entries, err := fs.ReadDir(fsys, "assets")
	if err != nil {
		t.Fatalf("read assets dir: %v", err)
	}
	assetIDs := make(map[string]bool)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".png") {
			t.Errorf("non-png asset present: %s", name)
			continue
		}
		assetIDs[strings.TrimSuffix(name, ".png")] = true
	}

	catalogIDs := make(map[string]bool)
	for _, s := range All() {
		if catalogIDs[s.ID] {
			t.Errorf("duplicate catalog id: %s", s.ID)
		}
		catalogIDs[s.ID] = true
		if !assetIDs[s.ID] {
			t.Errorf("catalog id %q has no assets/%s.png", s.ID, s.ID)
		}
		if s.Name == "" || s.Emotion == "" || len(s.Tags) == 0 {
			t.Errorf("catalog id %q is missing name/emotion/tags", s.ID)
		}
	}
	for id := range assetIDs {
		if !catalogIDs[id] {
			t.Errorf("asset %s.png has no catalog entry", id)
		}
	}
	if len(catalogIDs) == 0 {
		t.Fatal("catalog is empty")
	}
}

func TestAssetKnownAndUnknown(t *testing.T) {
	known := All()[0].ID
	if data, ok := Asset(known); !ok || len(data) == 0 {
		t.Errorf("Asset(%q) = ok %v, %d bytes; want ok with bytes", known, ok, len(data))
	}
	// Unknown id and path-traversal attempts must return ok=false without a
	// filesystem read.
	for _, bad := range []string{"does-not-exist", "../catalog", "..%2fcatalog", ""} {
		if _, ok := Asset(bad); ok {
			t.Errorf("Asset(%q) = ok true; want false", bad)
		}
	}
}

func TestSearchByMoodAndKeyword(t *testing.T) {
	// Exact mood match leads the results.
	happy := Search("happy")
	if len(happy) == 0 || happy[0].Emotion != "happy" {
		t.Errorf("Search(happy) should lead with an emotion=happy sticker, got %+v", happy)
	}

	// Chinese keyword in tags resolves.
	if got := Search("撒花"); len(got) == 0 {
		t.Error("Search(撒花) returned nothing; expected the tada sticker")
	} else {
		found := false
		for _, s := range got {
			if s.ID == "tada" {
				found = true
			}
		}
		if !found {
			t.Errorf("Search(撒花) missing tada; got %v", ids(got))
		}
	}

	// English keyword, case-insensitive.
	if got := Search("CELEBRATE"); len(got) == 0 {
		t.Error("Search(CELEBRATE) returned nothing; expected celebrate-mood stickers")
	}

	// Empty query returns the whole catalog.
	if len(Search("")) != len(All()) {
		t.Error("Search(empty) should return the full catalog")
	}

	// Nonsense query returns nothing.
	if got := Search("zzz-no-such-sticker-zzz"); len(got) != 0 {
		t.Errorf("Search(nonsense) = %v; want empty", ids(got))
	}
}

func ids(list []Sticker) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = s.ID
	}
	return out
}
