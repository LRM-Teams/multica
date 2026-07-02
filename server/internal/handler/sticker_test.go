package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListStickersIncludesFormalPackCatalog(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	h.ListStickers(rec, httptest.NewRequest(http.MethodGet, "/api/stickers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ListStickers: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Stickers []any                 `json:"stickers"`
		Packs    []StickerPackResponse `json:"packs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Stickers) == 0 {
		t.Fatal("legacy stickers catalog is empty")
	}
	if len(resp.Packs) != 1 {
		t.Fatalf("packs len = %d, want 1", len(resp.Packs))
	}
	pack := resp.Packs[0]
	if pack.ID != "builtin" || pack.Source == "" || pack.License == "" || len(pack.Stickers) == 0 {
		t.Fatalf("pack metadata = %+v, want builtin catalog with source/license/stickers", pack)
	}
	asset := pack.Stickers[0]
	if asset.PackID != "builtin" || asset.StickerID == "" || asset.AssetURL != "/api/stickers/"+asset.StickerID || asset.MimeType == "" || asset.Alt == "" {
		t.Fatalf("first sticker asset = %+v, want formal asset contract", asset)
	}
}
