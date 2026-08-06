/**
 * Convert sticker tokens `:sticker:<id>:` into standard markdown images
 * pointing at the public sticker endpoint, e.g.
 *
 *   :sticker:tada:  ->  ![sticker:tada](/api/stickers/tada)
 *
 * This is a legacy Markdown compatibility transform. Formal channel messages
 * use structured message parts instead, and those surfaces opt out of this
 * preprocessing so `content` never becomes a sticker display fallback.
 *
 * The id charset is restricted to [a-z0-9-] so the token can't swallow
 * surrounding punctuation or collide with `mention://` / URL syntax. An id that
 * doesn't exist resolves to a 404 image, which the renderer drops silently.
 */
const STICKER_TOKEN = /:sticker:([a-z0-9-]+):/g;

export function preprocessStickers(text: string): string {
  if (!text.includes(":sticker:")) return text;
  return text.replace(STICKER_TOKEN, (_match, id: string) => {
    return `![sticker:${id}](/api/stickers/${id})`;
  });
}
