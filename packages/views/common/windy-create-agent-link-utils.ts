/**
 * Hire chat links (Frank/Parker hard-cut — no draft_id bridge).
 *
 * Canonical: multica://action-card/agent:create?id=<uuid>
 * Also accepted: multica://action-card?id=<uuid> (type defaults to agent:create)
 *
 * Legacy multica://create-agent?draft_id=… is intentionally NOT parsed —
 * that hire path is retired (agent draft create → 410).
 */

const ACTION_TYPE_CREATE = "agent:create";

export type AgentCreateActionLink = {
  actionType: typeof ACTION_TYPE_CREATE;
  cardId: string;
};

export function parseAgentCreateActionURL(raw: string): AgentCreateActionLink | null {
  try {
    const url = new URL(raw);
    if (url.protocol !== "multica:") return null;

    const host = url.hostname.toLowerCase();
    // multica://action-card/agent:create?id=…  → host=action-card, path=/agent:create
    // multica://action-card?id=…               → host=action-card, path empty
    if (host !== "action-card") return null;

    const pathType = url.pathname.replace(/^\/+/, "").trim();
    const actionType =
      pathType || url.searchParams.get("type")?.trim() || ACTION_TYPE_CREATE;
    if (actionType !== ACTION_TYPE_CREATE) return null;

    const cardId = url.searchParams.get("id")?.trim() || "";
    if (!cardId) return null;

    return { actionType: ACTION_TYPE_CREATE, cardId };
  } catch {
    return null;
  }
}

export function isAgentCreateActionLink(href: string | undefined): boolean {
  return !!href && parseAgentCreateActionURL(href) != null;
}

/** @deprecated Use parseAgentCreateActionURL — draft hire links are retired. */
export function parseWindyCreateAgentURL(raw: string): URL | null {
  // Only accept the new action-card scheme so markdown still routes here;
  // legacy create-agent URLs return null (not rendered as hire buttons).
  const parsed = parseAgentCreateActionURL(raw);
  if (!parsed) return null;
  try {
    return new URL(raw);
  } catch {
    return null;
  }
}

/** @deprecated Use isAgentCreateActionLink */
export function isWindyCreateAgentLink(href: string | undefined): boolean {
  return isAgentCreateActionLink(href);
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

export function buildAgentCreateActionHref(cardId: string): string {
  return `multica://action-card/agent:create?id=${encodeURIComponent(cardId)}`;
}
