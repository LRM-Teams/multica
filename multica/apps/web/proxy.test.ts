import { describe, it, expect } from "vitest";
import { NextRequest } from "next/server";
import { proxy } from "./proxy";

function req(path: string, cookies: Record<string, string> = {}) {
  const r = new NextRequest(`https://app.test${path}`);
  for (const [k, v] of Object.entries(cookies)) r.cookies.set(k, v);
  return r;
}

describe("proxy — bare-/ fast-path for returning users", () => {
  it("redirects bare / (with session + last_workspace_slug) to /<slug>/channels", () => {
    const res = proxy(
      req("/", { multica_logged_in: "1", last_workspace_slug: "acme" }),
    );
    expect(res.status).toBe(307);
    expect(res.headers.get("location")).toBe("https://app.test/acme/channels");
  });

  it("does NOT redirect an explicit deep link — the route is honored verbatim", () => {
    const res = proxy(
      req("/acme/issues/abc", {
        multica_logged_in: "1",
        last_workspace_slug: "acme",
      }),
    );
    // A workspace-scoped path falls through to the locale-forwarding default,
    // which is a plain `next()` (no redirect / Location header).
    expect(res.headers.get("location")).toBeNull();
  });

  it("does NOT redirect bare / when there is no session", () => {
    const res = proxy(req("/", { last_workspace_slug: "acme" }));
    expect(res.headers.get("location")).toBeNull();
  });
});
