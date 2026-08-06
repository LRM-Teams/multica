export interface StickerAsset {
  pack_id: string;
  sticker_id: string;
  name: string;
  name_en: string;
  emotion: string;
  asset_url: string;
  mime_type: string;
  alt: string;
  tags: string[];
  animated: boolean;
}

export interface StickerPack {
  id: string;
  name: string;
  source: string;
  license: string;
  stickers: StickerAsset[];
}

export interface StickerCatalogResponse {
  stickers: unknown[];
  license: string;
  source: string;
  packs: StickerPack[];
}
