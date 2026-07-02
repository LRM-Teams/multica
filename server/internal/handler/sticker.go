package handler

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/internal/stickerimg"
	"github.com/multica-ai/multica/server/internal/stickers"
)

type StickerPackResponse struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	Source   string                 `json:"source"`
	License  string                 `json:"license"`
	Stickers []StickerAssetResponse `json:"stickers"`
}

type StickerAssetResponse struct {
	PackID    string   `json:"pack_id"`
	StickerID string   `json:"sticker_id"`
	Name      string   `json:"name"`
	NameEn    string   `json:"name_en"`
	Emotion   string   `json:"emotion"`
	AssetURL  string   `json:"asset_url"`
	MimeType  string   `json:"mime_type"`
	Alt       string   `json:"alt"`
	Tags      []string `json:"tags"`
	Animated  bool     `json:"animated"`
}

// ListStickers returns the full sticker catalog (the agent sticker library).
// Public + unauthenticated on purpose: the catalog is non-sensitive embedded
// metadata, and the images it points at are loaded by <img> tags that cannot
// attach auth headers. The frontend uses this to power the (future) human
// picker and to resolve structured sticker parts to assets, names, and alt
// text for renderers.
func (h *Handler) ListStickers(w http.ResponseWriter, r *http.Request) {
	assets := make([]StickerAssetResponse, 0, len(stickers.All()))
	for _, sticker := range stickers.All() {
		assets = append(assets, stickerAssetResponse(sticker))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"stickers": stickers.All(),
		"license":  stickers.License(),
		"source":   stickers.Source(),
		"packs": []StickerPackResponse{{
			ID:       messageparts.BuiltinStickerPackID,
			Name:     "Built-in stickers",
			Source:   stickers.Source(),
			License:  stickers.License(),
			Stickers: assets,
		}},
	})
}

// GetStickerAsset serves one sticker image by id. Unknown ids 404 (the lookup
// goes through the catalog, and stickerimg.Read rejects anything outside the
// embedded set, so the raw URL param carries no path-traversal risk). Assets
// are immutable and content-addressed by a stable id, so they're cached
// aggressively.
func (h *Handler) GetStickerAsset(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sticker, ok := stickers.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "sticker not found")
		return
	}
	data, ok := stickerimg.Read(sticker.File)
	if !ok {
		writeError(w, http.StatusNotFound, "sticker not found")
		return
	}
	w.Header().Set("Content-Type", stickerContentType(sticker.File))
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Write(data)
}

func stickerContentType(file string) string {
	switch {
	case strings.HasSuffix(file, ".png"):
		return "image/png"
	case strings.HasSuffix(file, ".gif"):
		return "image/gif"
	default:
		return "image/jpeg"
	}
}

func stickerAssetResponse(sticker stickers.Sticker) StickerAssetResponse {
	return StickerAssetResponse{
		PackID:    messageparts.BuiltinStickerPackID,
		StickerID: sticker.ID,
		Name:      sticker.Name,
		NameEn:    sticker.NameEn,
		Emotion:   sticker.Emotion,
		AssetURL:  "/api/stickers/" + sticker.ID,
		MimeType:  stickerContentType(sticker.File),
		Alt:       firstNonEmpty(sticker.Name, sticker.NameEn, sticker.ID),
		Tags:      sticker.Tags,
		Animated:  strings.EqualFold(filepath.Ext(sticker.File), ".gif"),
	}
}
