const ISSUE_RETURN_TO_PARAM = "returnTo";
const ISSUE_RETURN_ORIGIN = "https://multica.invalid";

function isMessagesPath(pathname: string | undefined, channelsPath: string): boolean {
  return Boolean(
    pathname && (pathname === channelsPath || pathname.startsWith(`${channelsPath}/`)),
  );
}

/**
 * Preserve an explicit Messages origin when a user opens an issue from a
 * channel. The resulting issue page can expose a reliable return action
 * instead of depending on the browser history stack.
 */
export function issueDetailHrefFromChannel(
  issuePath: string,
  channelsPath: string,
  pathname: string,
  searchParams: URLSearchParams,
  sourceMessageId?: string,
): string {
  if (!isMessagesPath(pathname, channelsPath)) return issuePath;

  // A channel's normal route has no `?message=` query, even when a reader taps
  // an issue reference inside a particular row. Prefer that row's id when the
  // renderer knows it, so the return control lands on the actual source rather
  // than merely reopening the channel. Keeping all other query parameters
  // preserves the established deep-link contract.
  const returnSearch = new URLSearchParams(searchParams);
  if (sourceMessageId) returnSearch.set("message", sourceMessageId);
  const query = returnSearch.toString();
  const returnTo = query ? `${pathname}?${query}` : pathname;
  return `${issuePath}?${ISSUE_RETURN_TO_PARAM}=${encodeURIComponent(returnTo)}`;
}

/**
 * A return target is intentionally limited to the current workspace's
 * Messages routes. The URL parameter is user-controlled, so it must not turn
 * an issue page into a generic redirector.
 */
export function issueMessagesReturnPath(
  searchParams: URLSearchParams | undefined,
  channelsPath: string,
): string | null {
  const returnTo = searchParams?.get(ISSUE_RETURN_TO_PARAM);
  if (!returnTo) return null;

  let url: URL;
  try {
    url = new URL(returnTo, ISSUE_RETURN_ORIGIN);
  } catch {
    return null;
  }

  if (url.origin !== ISSUE_RETURN_ORIGIN || !isMessagesPath(url.pathname, channelsPath)) {
    return null;
  }

  return `${url.pathname}${url.search}${url.hash}`;
}
