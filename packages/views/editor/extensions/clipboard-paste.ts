/**
 * Office apps (PowerPoint, Word, Excel) put BOTH the copied text and a
 * bitmap of the selection on the clipboard. File-manager copies put a
 * path or filename in text/plain beside the real file.
 *
 * Prefer the text so "copy text from a slide" does not become an uploaded
 * screenshot. Keep file-manager copies as files.
 */
export function clipboardPrefersTextOverFiles(
  clipboard: Pick<DataTransfer, "getData" | "files">,
): boolean {
  const text = clipboard.getData("text/plain").trim();
  if (!text) return false;
  const files = clipboard.files;
  if (!files?.length) return true;
  return !looksLikeFileManagerCopy(text, files);
}

function looksLikeFileManagerCopy(text: string, files: FileList): boolean {
  if (text.includes("\n") || text.includes("\r")) return false;
  const names = Array.from(files)
    .map((file) => file.name)
    .filter(Boolean);
  if (
    names.some(
      (name) =>
        text === name || text.endsWith(`/${name}`) || text.endsWith(`\\${name}`),
    )
  ) {
    return true;
  }
  return /^(file:\/\/|\/|[a-zA-Z]:[\\/])/.test(text);
}
