export function parseWindyCreateAgentURL(raw: string): URL | null {
  try {
    const url = new URL(raw);
    if (url.protocol !== "multica:" || url.hostname !== "create-agent") return null;
    return url;
  } catch {
    return null;
  }
}

export function listParam(url: URL, key: string): string[] {
  const values: string[] = [];
  for (const raw of url.searchParams.getAll(key)) {
    for (const part of raw.split(",")) {
      const trimmed = part.trim();
      if (trimmed) values.push(trimmed);
    }
  }
  return values;
}

export function isWindyCreateAgentLink(href: string | undefined): boolean {
  return !!href && parseWindyCreateAgentURL(href) != null;
}

/** Server CreateAgent / GetAgentDraft errors that mean reopen Wendy's hiring card. */
export function isAgentDraftUnavailableError(message: string): boolean {
  return /agent draft (not found|already used|is no longer available)/i.test(message);
}
