/**
 * Convert sticker tokens `:sticker:<id>:` into standard markdown images
 * pointing at the public sticker endpoint, e.g.
 *
 *   :sticker:tada:  ->  ![sticker:tada](/api/stickers/tada)
 *
 * Agents embed the token in message content (see the multica-stickers built-in
 * skill); this normalises it to an `<img>` the app-level renderImage hook then
 * styles as a sticker (it recognises the `/api/stickers/<id>` src). Keeping the
 * transform here — a pure string pass with no business logic — means both chat
 * and channel surfaces get it for free via the shared Markdown component.
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
