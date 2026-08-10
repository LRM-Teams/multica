export function workspaceURLPrefix(appUrl: string): string {
  const normalized = appUrl.trim();
  if (!normalized) return "";

  try {
    return `${new URL(normalized).host}/`;
  } catch {
    const host =
      normalized
        .replace(/^https?:\/\//, "")
        .split("/")[0]
        ?.replace(/\/+$/, "") ?? "";
    return host ? `${host}/` : "";
  }
}
