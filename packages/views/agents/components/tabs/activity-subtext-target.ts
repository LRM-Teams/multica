export type ActivitySubtextPart =
  | { kind: "text"; value: string }
  | { kind: "handle"; value: string };

const HANDLE = /#[^\s]+/g;

/** Split Activity subtext so `target: #channel` / bare `#channel` can become links. */
export function parseActivitySubtext(subtext: string): ActivitySubtextPart[] {
  const parts: ActivitySubtextPart[] = [];
  const pushText = (value: string) => {
    if (value) parts.push({ kind: "text", value });
  };

  let cursor = 0;
  HANDLE.lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = HANDLE.exec(subtext)) !== null) {
    pushText(subtext.slice(cursor, match.index));
    parts.push({ kind: "handle", value: match[0] });
    cursor = match.index + match[0].length;
  }
  pushText(subtext.slice(cursor));
  return parts.length > 0 ? parts : [{ kind: "text", value: subtext }];
}

export function resolveActivityHandleHref(
  handle: string,
  channels: readonly { id: string; name: string; kind?: string }[],
  channelDetail: (id: string) => string,
): string | null {
  const body = handle.trim().replace(/^#/, "");
  if (!body || body.includes(":")) return null;
  const channel = channels.find(
    (candidate) => candidate.kind !== "dm" && candidate.name === body,
  );
  return channel ? channelDetail(channel.id) : null;
}
