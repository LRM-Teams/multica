const DEFAULT_PUBLIC_APP_ORIGIN = "https://www.leagent.me";

/** Build-time public web origin used by metadata and browser-facing links. */
export const PUBLIC_APP_ORIGIN = (
  process.env.NEXT_PUBLIC_APP_URL?.trim() || DEFAULT_PUBLIC_APP_ORIGIN
).replace(/\/+$/, "");
