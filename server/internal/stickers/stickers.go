// Package stickers embeds the platform sticker library (a curated set of
// expressive images) into the server binary and exposes lookup + search over
// it. Agents reference a sticker in chat/channel/DM content with the token
// :sticker:<id>: ; the frontend resolves that token to GET /api/stickers/<id>.
//
// catalog.json is the single source of truth (id, bilingual name/tags,
// emotion); assets/<id>.png holds the image for each catalog entry. The two are
// kept 1:1 — a catalog entry without an asset (or vice versa) is a build-time
// bug, surfaced by the package tests.
package stickers

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed catalog.json assets
var fsys embed.FS

// Sticker is one catalog entry. Tags mix Chinese and English so search works in
// either language (agents and users both query in both).
type Sticker struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	NameEn  string   `json:"name_en"`
	Emotion string   `json:"emotion"`
	Tags    []string `json:"tags"`
}

type catalogFile struct {
	License  string    `json:"license"`
	Source   string    `json:"source"`
	Stickers []Sticker `json:"stickers"`
}

var (
	catalog catalogFile
	byID    map[string]Sticker
)

func init() {
	raw, err := fsys.ReadFile("catalog.json")
	if err != nil {
		panic(fmt.Sprintf("stickers: read catalog.json: %v", err))
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		panic(fmt.Sprintf("stickers: parse catalog.json: %v", err))
	}
	byID = make(map[string]Sticker, len(catalog.Stickers))
	for _, s := range catalog.Stickers {
		byID[s.ID] = s
	}
}

// All returns the full catalog in authored order.
func All() []Sticker { return catalog.Stickers }

// License and Source describe the asset provenance (for the catalog endpoint).
func License() string { return catalog.License }
func Source() string  { return catalog.Source }

// Get returns the catalog entry for an id, if it exists.
func Get(id string) (Sticker, bool) {
	s, ok := byID[id]
	return s, ok
}

// Asset returns the PNG bytes for a known sticker id. Unknown ids return
// ok=false (never a filesystem read), so the id can be passed straight from a
// URL param without path-traversal risk.
func Asset(id string) ([]byte, bool) {
	if _, ok := byID[id]; !ok {
		return nil, false
	}
	data, err := fsys.ReadFile("assets/" + id + ".png")
	if err != nil {
		return nil, false
	}
	return data, true
}

// Search returns catalog entries whose id, name, English name, emotion, or any
// tag contains the (case-insensitive) query as a substring. Results keep
// catalog order; an empty query returns the full catalog. Entries whose emotion
// exactly equals the query are surfaced first so a mood query ("happy", "开心")
// leads with the most on-point stickers.
func Search(query string) []Sticker {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return All()
	}
	var exact, partial []Sticker
	for _, s := range catalog.Stickers {
		if strings.EqualFold(s.Emotion, q) {
			exact = append(exact, s)
			continue
		}
		if stickerMatches(s, q) {
			partial = append(partial, s)
		}
	}
	return append(exact, partial...)
}

func stickerMatches(s Sticker, q string) bool {
	if strings.Contains(strings.ToLower(s.ID), q) ||
		strings.Contains(strings.ToLower(s.Name), q) ||
		strings.Contains(strings.ToLower(s.NameEn), q) ||
		strings.Contains(strings.ToLower(s.Emotion), q) {
		return true
	}
	for _, t := range s.Tags {
		if strings.Contains(strings.ToLower(t), q) {
			return true
		}
	}
	return false
}

// Emotions returns the distinct emotion buckets present in the catalog, sorted.
// Used by the CLI to hint available moods when a search comes up empty.
func Emotions() []string {
	seen := make(map[string]struct{})
	var out []string
	for _, s := range catalog.Stickers {
		if _, ok := seen[s.Emotion]; ok {
			continue
		}
		seen[s.Emotion] = struct{}{}
		out = append(out, s.Emotion)
	}
	sort.Strings(out)
	return out
}
