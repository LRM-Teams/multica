import { describe, it, expect, vi, beforeEach } from "vitest";
import { paths } from "@multica/core/paths";

const { mockCookieGet, mockHeaderGet } = vi.hoisted(() => ({
  mockCookieGet: vi.fn(),
  mockHeaderGet: vi.fn(() => "multica_auth=tok"),
}));

vi.mock("next/headers", () => ({
  cookies: async () => ({ get: mockCookieGet }),
  headers: async () => ({ get: mockHeaderGet }),
}));

import { resolveServerPostAuthDestination } from "./resolve-server-destination";

function cookieMap(map: Record<string, string>) {
  mockCookieGet.mockImplementation((name: string) =>
    name in map ? { value: map[name] } : undefined,
  );
}

const okJson = (body: unknown) => ({ ok: true, json: async () => body });
const fetchMock = vi.fn();

beforeEach(() => {
  vi.clearAllMocks();
  mockHeaderGet.mockReturnValue("multica_auth=tok");
  global.fetch = fetchMock as unknown as typeof fetch;
});

describe("resolveServerPostAuthDestination", () => {
  it("returns null (unauthenticated) with no session cookie — never hits the backend", async () => {
    cookieMap({});
    expect(await resolveServerPostAuthDestination()).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("redirects an onboarded user to their last-active workspace", async () => {
    cookieMap({ multica_auth: "tok", last_workspace_slug: "beta" });
    fetchMock
      .mockResolvedValueOnce(okJson({ id: "u1", onboarded_at: "2026-01-01" }))
      .mockResolvedValueOnce(okJson([{ slug: "acme" }, { slug: "beta" }]));
    expect(await resolveServerPostAuthDestination()).toBe(paths.workspace("beta").channels());
  });

  it("falls back to the default workspace when there is no last-active cookie", async () => {
    cookieMap({ multica_auth: "tok" });
    fetchMock
      .mockResolvedValueOnce(okJson({ id: "u1", onboarded_at: "2026-01-01" }))
      .mockResolvedValueOnce(okJson([{ slug: "acme" }, { slug: "beta" }]));
    expect(await resolveServerPostAuthDestination()).toBe(paths.workspace("acme").channels());
  });

  it("returns null when the backend rejects the session (non-2xx) — treated as unauthenticated", async () => {
    cookieMap({ multica_auth: "expired" });
    fetchMock.mockResolvedValue({ ok: false, json: async () => ({}) });
    expect(await resolveServerPostAuthDestination()).toBeNull();
  });

  it("returns null on a network error — never traps a real visitor", async () => {
    cookieMap({ multica_auth: "tok" });
    fetchMock.mockRejectedValue(new Error("network"));
    expect(await resolveServerPostAuthDestination()).toBeNull();
  });

  it("routes an un-onboarded user to onboarding", async () => {
    cookieMap({ multica_auth: "tok", last_workspace_slug: "beta" });
    fetchMock
      .mockResolvedValueOnce(okJson({ id: "u1", onboarded_at: null }))
      .mockResolvedValueOnce(okJson([{ slug: "beta" }]));
    expect(await resolveServerPostAuthDestination()).toBe(paths.onboarding());
  });

  it("stale last-active slug (no longer accessible) falls back to the default", async () => {
    cookieMap({ multica_auth: "tok", last_workspace_slug: "gone" });
    fetchMock
      .mockResolvedValueOnce(okJson({ id: "u1", onboarded_at: "2026-01-01" }))
      .mockResolvedValueOnce(okJson([{ slug: "acme" }, { slug: "beta" }]));
    expect(await resolveServerPostAuthDestination()).toBe(paths.workspace("acme").channels());
  });
});
