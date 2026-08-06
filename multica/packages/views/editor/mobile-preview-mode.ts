/**
 * Mobile attachment preview dispatch (LRM-219 / LRM-230).
 *
 * - Images: stream thumb → fullscreen big image
 * - HTML: compact card → fullscreen sandboxed srcDoc preview (LRM-230 restores
 *   this after LRM-219's empty-pane regression)
 * - PDF / other: compact card → fullscreen chrome + download guidance (no
 *   blank pane; PDF stays out of iframe because app CSP blocks it)
 *
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

function isHtmlFile(contentType: string, filename: string): boolean {
  const ct = contentType.toLowerCase().split(";")[0]?.trim() ?? "";
  const ext = getFileExtension(filename);
  return ct === "text/html" || ext === "html" || ext === "htm";
}

/** Images and HTML preview; PDF and everything else → none (with UI hint). */
export function resolveMobilePreviewMode(
  mode: MobilePreviewMode | undefined,
  contentType: string,
  filename: string,
): "html" | "image" | "none" {
  if (mode === "image") return "image";
  if (mode === "html") return "html";
  // Explicit pdf/none stay non-content; never iframe PDF under app CSP.
  if (mode === "pdf" || mode === "none") return "none";
  if (isImageFile(contentType, filename)) return "image";
  if (isHtmlFile(contentType, filename)) return "html";
  return "none";
}
