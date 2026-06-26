package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/stickers"
)

// ListStickers returns the full sticker catalog (the agent sticker library).
// Public + unauthenticated on purpose: the catalog is non-sensitive embedded
// metadata, and the images it points at are loaded by <img> tags that cannot
// attach auth headers. The frontend uses this to power the (future) human
// picker and to resolve :sticker:<id>: tokens to names for tooltips/alt text.
func (h *Handler) ListStickers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"stickers": stickers.All(),
		"license":  stickers.License(),
		"source":   stickers.Source(),
	})
}

// GetStickerAsset serves one sticker image by id. Unknown ids 404 (Asset never
// touches the filesystem for an id absent from the catalog, so the raw URL
// param carries no path-traversal risk). Assets are immutable and content-
// addressed by a stable id, so they're cached aggressively.
func (h *Handler) GetStickerAsset(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	data, ok := stickers.Asset(id)
	if !ok {
		writeError(w, http.StatusNotFound, "sticker not found")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Write(data)
}
