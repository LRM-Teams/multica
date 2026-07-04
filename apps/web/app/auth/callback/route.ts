import { NextResponse, type NextRequest } from "next/server";
import { sanitizeNextUrl } from "@multica/core/auth";
import { paths } from "@multica/core/paths";
import type { Invitation } from "@multica/core/types";
import { resolveRemoteApiUrl } from "@/config/runtime-urls";
import { fetchAuthedContext, postAuthDestination } from "@/features/auth/server-post-auth";

// Raw cookie value (as it appears in a Set-Cookie header, before attributes) so
// we can forward the session on a server-to-server GET. Not decoded — a Cookie
// header must send the value exactly as the browser would echo it.
function cookieValueFromSetCookies(setCookies: string[], name: string): string | null {
  for (const sc of setCookies) {
    const m = new RegExp(`^${name}=([^;]*)`).exec(sc);
    if (m?.[1] !== undefined) return m[1];
  }
  return null;
}

/**
 * The PUBLIC origin the browser used, reconstructed from proxy headers. Behind
 * the reverse proxy `request.url`'s origin is the internal one (e.g.
 * `http://0.0.0.0:3000`); using that for the OAuth `redirect_uri` would not match
 * the public `redirect_uri` the browser sent to Google and the code exchange
 * would hard-fail with `redirect_uri_mismatch`. Prefer the standard forwarded
 * headers, then the (proxy-preserved) Host header, then the request URL.
 */
function publicOrigin(request: NextRequest): string {
  const proto =
    request.headers.get("x-forwarded-proto")?.split(",")[0]?.trim() ||
    request.nextUrl.protocol.replace(/:$/, "");
  const host =
    request.headers.get("x-forwarded-host")?.split(",")[0]?.trim() ||
    request.headers.get("host") ||
    request.nextUrl.host;
  return `${proto}://${host}`;
}

// App redirects use a RELATIVE Location (same-origin destinations only), so no
// origin — internal or otherwise — is ever reflected into the response.
function relativeRedirect(path: string): NextResponse {
  return new NextResponse(null, { status: 307, headers: { location: path } });
}

/**
 * OAuth callback as a server-side Route Handler (#223 Phase 2). Replaces the old
 * client page that exchanged the code in a `useEffect` then `router.push`ed —
 * that flashed the "Signing in…" screen and tripped the client-side-redirect
 * rule. Here the code exchange, session-cookie issuance, and redirect all happen
 * server-side; the browser only ever sees a 307 to the final app destination.
 */
export async function GET(request: NextRequest) {
  const params = request.nextUrl.searchParams;
  const toLogin = (error: string) =>
    relativeRedirect(`${paths.login()}?error=${encodeURIComponent(error)}`);

  const errorParam = params.get("error");
  if (errorParam) {
    // Confine the `?error=` surface to a known set — never reflect a raw
    // provider string into the URL. /login maps these to localized copy (#227).
    return toLogin(errorParam === "access_denied" ? "access_denied" : "login_failed");
  }
  const code = params.get("code");
  if (!code) return toLogin("missing_code");

  const state = params.get("state") ?? "";
  const stateParts = state.split(",");

  // Desktop: keep the token exchange + `multica://` deep-link on the CLIENT so
  // the token never touches a server Location/log. The single-use OAuth code is
  // already public in this callback URL, so handing it to the client page is safe.
  if (stateParts.includes("platform:desktop")) {
    const q = new URLSearchParams({ code });
    if (state) q.set("state", state);
    return relativeRedirect(`/auth/callback/desktop?${q.toString()}`);
  }

  // Web: exchange the code server-to-server. `redirect_uri` MUST be the PUBLIC
  // callback the user started authorization with (RFC 6749) — never the internal
  // request origin and never the desktop deep-link.
  const base = resolveRemoteApiUrl(process.env);
  const redirectUri = `${publicOrigin(request)}/auth/callback`;
  let loginRes: Response;
  try {
    loginRes = await fetch(`${base}/auth/google`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ code, redirect_uri: redirectUri }),
      cache: "no-store",
    });
  } catch {
    return toLogin("login_failed");
  }
  if (!loginRes.ok) return toLogin("login_failed");

  // Forward EVERY auth cookie the backend issued, verbatim — never parse/rebuild
  // (drops SameSite/Secure/Domain/Expires) and never allowlist (misses the
  // CloudFront signed cookies). Standard BFF/reverse-proxy behaviour.
  const setCookies = loginRes.headers.getSetCookie();
  const authValue = cookieValueFromSetCookies(setCookies, "multica_auth");
  if (!authValue) return toLogin("login_failed");

  const nextPart = stateParts.find((s) => s.startsWith("next:"));
  const nextUrl = sanitizeNextUrl(nextPart ? nextPart.slice(5) : null);

  // Compute the destination with the freshly-issued session, reusing the same
  // shared logic as the landing page (last-active precedence incl. #225).
  const ctx = await fetchAuthedContext(`multica_auth=${authValue}`);

  let destination: string;
  if (nextUrl) {
    // A `next=` (e.g. /invite/<id>) always survives the OAuth round-trip.
    destination = nextUrl;
  } else if (!ctx) {
    // Session fetch failed right after a successful exchange (rare). Bounce to
    // `/`; it re-resolves server-side with the cookies we're about to set.
    destination = "/";
  } else {
    const onboarded = ctx.user.onboarded_at != null;
    // Parity with the previous client behaviour: only un-onboarded users get the
    // pending-invitation lookup; already-onboarded users see invites in the
    // sidebar, not as a forced wall. A network blip here is non-fatal.
    let inviteDest: string | null = null;
    if (!onboarded) {
      try {
        const invRes = await fetch(`${base}/api/invitations`, {
          headers: { cookie: `multica_auth=${authValue}` },
          cache: "no-store",
        });
        if (invRes.ok) {
          const invites = (await invRes.json()) as Invitation[];
          if (Array.isArray(invites) && invites.length > 0) inviteDest = paths.invitations();
        }
      } catch {
        /* non-fatal — fall through to the normal destination */
      }
    }
    const cookieSlug = request.cookies.get("last_workspace_slug")?.value ?? null;
    destination = inviteDest ?? postAuthDestination(ctx, cookieSlug);
  }

  // Relative Location — carries ONLY the final app path, never an origin (so no
  // internal host can leak) and never the token/code/cookie value.
  const res = relativeRedirect(destination);
  // Forward every backend cookie + the Next-side sentinel through the SAME
  // header path. Do NOT mix in `res.cookies.set()` — NextResponse's cookie store
  // re-serialises the set-cookie header and would drop these appended cookies.
  for (const sc of setCookies) {
    res.headers.append("set-cookie", sc);
  }
  // Sentinel so proxy.ts fast-path + client auth see the session.
  res.headers.append("set-cookie", "multica_logged_in=1; Path=/; Max-Age=31536000; SameSite=Lax");
  return res;
}
