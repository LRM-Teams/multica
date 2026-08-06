// The "Runtimes" page was renamed to "Computers" (task #18) — keep old
// links/bookmarks working instead of 404ing. Not `permanent` (307, not
// 308): a 308 is aggressively cached by browsers, and we may still
// adjust this rename.
//
// `:workspaceSlug` is a wildcard single-segment match — without the
// `(?!api/)` exclusion it also matches `/api/runtimes`, redirecting the
// real API request to a nonexistent `/api/computers` before it ever
// reaches the `/api/:path*` rewrite in next.config.ts. That broke every
// caller of the runtimes API in production (task #18 hotfix). The
// lookahead is anchored to `api/` (not bare `api`) so it excludes only
// the literal `api` segment, not real workspace slugs that happen to
// start with "api" (e.g. `apitest`).
export const RUNTIMES_TO_COMPUTERS_REDIRECTS = [
  {
    source: "/:workspaceSlug((?!api/)[^/]+)/runtimes",
    destination: "/:workspaceSlug/computers",
    permanent: false,
  },
  {
    source: "/:workspaceSlug((?!api/)[^/]+)/runtimes/:id",
    destination: "/:workspaceSlug/computers/:id",
    permanent: false,
  },
] as const;
