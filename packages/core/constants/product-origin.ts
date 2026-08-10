/**
 * Official Multica product origins (daemon #29 domain unification).
 *
 * Cloud defaults for the hosted product. Self-host / desktop overrides still
 * win when configured. Install/update binaries stay on the OSS release CDN
 * (see repository.ts) until leagent.me CDN is unblocked.
 */

/** Public web app origin (no trailing slash). */
export const PRODUCT_APP_ORIGIN = "https://www.leagent.me";

/** Public HTTP API origin. */
export const PRODUCT_API_ORIGIN = "https://api.leagent.me";

/** WebSocket origin for the public API. */
export const PRODUCT_WS_ORIGIN = "wss://api.leagent.me/ws";

/** Marketing / bare product host label (workspace slug pills, etc.). */
export const PRODUCT_HOST_LABEL = "leagent.me";

/** Public docs root. */
export const PRODUCT_DOCS_ORIGIN = "https://www.leagent.me/docs";

/** Public changelog. */
export const PRODUCT_CHANGELOG_URL = "https://www.leagent.me/changelog";
