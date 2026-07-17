import { describe, it, expect, vi, beforeEach } from "vitest";
import { NextRequest } from "next/server";
import { paths } from "@multica/core/paths";
import { GET } from "./route";

const SET_COOKIES = [
  "multica_auth=tok; Path=/; HttpOnly; SameSite=Lax",
  "multica_csrf=csrf; Path=/; SameSite=Lax",
  "CloudFront-Policy=pol; Path=/; Secure",
  "CloudFront-Signature=sig; Path=/; Secure",
  "CloudFront-Key-Pair-Id=kpid; Path=/; Secure",
];

function mockFetch(opts: {
  loginOk?: boolean;
  onboarded?: boolean;
  workspaces?: { slug: string; last_active_at: string | null }[];
  invitations?: unknown[];
} = {}) {
  const {
    loginOk = true,
    onboarded = true,
    workspaces = [{ slug: "acme", last_active_at: null }, { slug: "beta", last_active_at: "2026-01-05" }],
    invitations = [],
  } = opts;
  return vi.fn((input: RequestInfo | URL, _init?: RequestInit) => {
    const u = String(input);
    if (u.endsWith("/auth/google")) {
      return Promise.resolve({
        ok: loginOk,
        headers: { getSetCookie: () => (loginOk ? SET_COOKIES : []) },
        json: async () => ({ token: "tok", user: { id: "u1", onboarded_at: onboarded ? "2026-01-01" : null } }),
      });
    }
    if (u.endsWith("/api/me")) {
      return Promise.resolve({ ok: true, json: async () => ({ id: "u1", onboarded_at: onboarded ? "2026-01-01" : null }) });
    }
    if (u.endsWith("/api/workspaces")) {
      return Promise.resolve({ ok: true, json: async () => workspaces });
    }
    if (u.endsWith("/api/invitations")) {
      return Promise.resolve({ ok: true, json: async () => invitations });
    }
    return Promise.reject(new Error(`unexpected fetch ${u}`));
  });
}

function req(query: string, cookies: Record<string, string> = {}) {
  const r = new NextRequest(`https://app.test/auth/callback?${query}`);
  for (const [k, v] of Object.entries(cookies)) r.cookies.set(k, v);
  return r;
}

// A request as it arrives behind the reverse proxy: the URL origin is internal,
// the public host/proto come via forwarded headers.
function proxyReq(query: string) {
  return new NextRequest(`http://0.0.0.0:3000/auth/callback?${query}`, {
    headers: { "x-forwarded-proto": "https", "x-forwarded-host": "app.multica.ai" },
  });
}

// Location is now a relative path — return it verbatim.
const loc = (res: Response) => res.headers.get("location")!;

beforeEach(() => vi.restoreAllMocks());

describe("GET /auth/callback", () => {
  it("no code → /login?error=missing_code", async () => {
    const res = await GET(req(""));
    expect(loc(res)).toBe(`${paths.login()}?error=missing_code`);
  });

  it("error param → /login?error=access_denied", async () => {
    const res = await GET(req("error=access_denied"));
    expect(loc(res)).toBe(`${paths.login()}?error=access_denied`);
  });

  it("normalizes an unknown provider error to login_failed (no raw string reflected)", async () => {
    const res = await GET(req("error=some_provider_error"));
    expect(loc(res)).toBe(`${paths.login()}?error=login_failed`);
  });

  it("desktop → redirects to the client desktop page, no exchange/cookies", async () => {
    const fetchSpy = mockFetch();
    global.fetch = fetchSpy as unknown as typeof fetch;
    const res = await GET(req("code=abc&state=platform:desktop,next:/x"));
    const l = res.headers.get("location")!;
    expect(l.startsWith("/auth/callback/desktop?")).toBe(true);
    expect(new URLSearchParams(l.split("?")[1]).get("code")).toBe("abc");
    expect(fetchSpy).not.toHaveBeenCalled(); // token exchange stays on the client
    expect(res.headers.getSetCookie()).toHaveLength(0);
  });

  it("web happy path: exchanges, forwards ALL Set-Cookie + sets multica_logged_in, redirects to last-active", async () => {
    global.fetch = mockFetch({ workspaces: [{ slug: "acme", last_active_at: null }, { slug: "beta", last_active_at: "2026-01-05" }] }) as unknown as typeof fetch;
    const res = await GET(req("code=abc"));
    // No cookie slug → newest last_active_at (beta) wins over created-first acme.
    expect(loc(res)).toBe(paths.workspace("beta").channels());
    const cookies = res.headers.getSetCookie();
    expect(cookies.some((c) => c.startsWith("multica_auth="))).toBe(true);
    expect(cookies.some((c) => c.startsWith("multica_csrf="))).toBe(true);
    expect(cookies.some((c) => c.startsWith("CloudFront-Policy="))).toBe(true);
    expect(cookies.some((c) => c.startsWith("CloudFront-Signature="))).toBe(true);
    expect(cookies.some((c) => c.startsWith("CloudFront-Key-Pair-Id="))).toBe(true);
    expect(cookies.some((c) => c.startsWith("multica_logged_in="))).toBe(true);
    // Location must never carry the token.
    expect(res.headers.get("location")).not.toContain("tok");
  });

  it("cookie slug wins over the server last_active_at when accessible", async () => {
    global.fetch = mockFetch() as unknown as typeof fetch;
    const res = await GET(req("code=abc", { last_workspace_slug: "acme" }));
    expect(loc(res)).toBe(paths.workspace("acme").channels());
  });

  it("next= always wins the OAuth round-trip", async () => {
    global.fetch = mockFetch() as unknown as typeof fetch;
    const res = await GET(req("code=abc&state=next:/invite/123"));
    expect(loc(res)).toBe("/invite/123");
  });

  it("failed exchange → /login?error=login_failed", async () => {
    global.fetch = mockFetch({ loginOk: false }) as unknown as typeof fetch;
    const res = await GET(req("code=abc"));
    expect(loc(res)).toBe(`${paths.login()}?error=login_failed`);
  });

  // --- proxy-shape regression: internal request URL + forwarded public host ---

  it("behind the proxy, the redirect Location is relative and never leaks the internal origin", async () => {
    global.fetch = mockFetch() as unknown as typeof fetch;
    const res = await GET(proxyReq("code=abc"));
    const l = res.headers.get("location")!;
    expect(l.startsWith("/")).toBe(true); // relative
    expect(l).not.toContain("0.0.0.0");
    expect(l).not.toMatch(/^https?:/);
  });

  it("behind the proxy, the OAuth redirect_uri is the PUBLIC origin, not the internal request origin", async () => {
    const fetchSpy = mockFetch();
    global.fetch = fetchSpy as unknown as typeof fetch;
    await GET(proxyReq("code=abc"));
    const googleCall = fetchSpy.mock.calls.find(([u]) => String(u).endsWith("/auth/google"));
    const init = googleCall?.[1] as RequestInit;
    const body = JSON.parse(init.body as string);
    expect(body.redirect_uri).toBe("https://app.multica.ai/auth/callback");
    expect(body.redirect_uri).not.toContain("0.0.0.0");
  });

  it("error path behind the proxy also stays relative", async () => {
    const res = await GET(proxyReq("error=access_denied"));
    expect(loc(res)).toBe(`${paths.login()}?error=access_denied`);
    expect(res.headers.get("location")).not.toContain("0.0.0.0");
  });
});
