/**
 * LRM-219: mobile attachment preview dispatch — only images preview.
 * Kept outside the component file so Fast Refresh can preserve state
 * (react-doctor: only-export-components).
 */

export type MobilePreviewMode = "html" | "image" | "pdf" | "none";

function getFileExtension(filename: string): string {
  const base = filename.toLowerCase().split(/[\\/]/).pop() ?? "";
  const dot = base.lastIndexOf(".");
  if (dot <= 0) return "";
  return base.slice(dot + 1);
}

function isImageFile(contentType: string, filename: string): boolean {
  const ct = contentType.toLowerCase();
  const ext = getFileExtension(filename);
  return (
    ct.startsWith("image/") ||
    ["png", "jpg", "jpeg", "gif", "webp", "svg", "avif", "bmp", "ico"].includes(
      ext,
    )
  );
}

/** Only images preview. HTML/PDF (and anything else) → none. */
export function resolveMobilePreviewMode(
  mode: MobilePreviewMode | undefined,
  contentType: string,
  filename: string,
): "image" | "none" {
  if (mode === "image") return "image";
  if (mode === "html" || mode === "pdf" || mode === "none") return "none";
  return isImageFile(contentType, filename) ? "image" : "none";
}
