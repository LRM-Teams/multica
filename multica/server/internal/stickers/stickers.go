// Package stickers holds the platform sticker catalog (a curated set of
// expressive images) and lookup + search over it. Messages reference stickers
// through structured message parts; renderers resolve sticker_id values to
// GET /api/stickers/<id>.
//
// This package embeds only catalog.json (small) so it can be imported by the
// CLI — which needs search but not the image bytes — without bloating that
// binary. The image bytes live in the sibling stickerimg package, embedded
// separately and imported only by the server. catalog.json is the source of
// truth: each entry's File names an image in stickerimg/files, kept 1:1 (a
// catalog entry without an asset, or vice versa, is a build-time bug surfaced
// by the package tests).
package stickers

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed catalog.json
var catalogJSON []byte

// Sticker is one catalog entry. Tags mix Chinese and English (the zh caption
// plus an en gloss) so search works in either language.
type Sticker struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	NameEn  string   `json:"name_en"`
	Emotion string   `json:"emotion"`
	File    string   `json:"file"`
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
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
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

// Search returns catalog entries whose id, name, English name, emotion, or any
// tag contains the (case-insensitive) query as a substring. Results keep
// catalog order; an empty query returns the full catalog. Entries whose emotion
// exactly equals the query are surfaced first so a mood query ("praise",
// "鼓掌") leads with the most on-point stickers.
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
