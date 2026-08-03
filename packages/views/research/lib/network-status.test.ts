// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  isServerError,
  readBrowserOnline,
  resolveOfflineBannerMode,
} from "./network-status";

describe("isServerError (LRM-833)", () => {
  it("flags status-bearing 5xx errors", () => {
    expect(isServerError({ message: "boom", status: 500 })).toBe(true);
    expect(isServerError({ message: "bad gateway", status: 502 })).toBe(true);
    expect(isServerError({ message: "not found", status: 404 })).toBe(false);
  });

  it("rejects plain Errors and nullish", () => {
    expect(isServerError(new Error("offline"))).toBe(false);
    expect(isServerError(null)).toBe(false);
    expect(isServerError(undefined)).toBe(false);
  });
});

describe("resolveOfflineBannerMode (LRM-833)", () => {
  it("prefers offline while the browser reports offline", () => {
    expect(resolveOfflineBannerMode(false, "failed")).toBe("offline");
    expect(resolveOfflineBannerMode(false, "reconnecting")).toBe("offline");
    expect(resolveOfflineBannerMode(false, "idle")).toBe("offline");
  });

  it("surfaces reconnect / failed only when online", () => {
    expect(resolveOfflineBannerMode(true, "idle")).toBeNull();
    expect(resolveOfflineBannerMode(true, "reconnecting")).toBe("reconnecting");
    expect(resolveOfflineBannerMode(true, "failed")).toBe("failed");
  });
});

describe("readBrowserOnline (LRM-833)", () => {
  it("reads navigator.onLine when present", () => {
    const prev = navigator.onLine;
    Object.defineProperty(navigator, "onLine", { configurable: true, value: false });
    expect(readBrowserOnline()).toBe(false);
    Object.defineProperty(navigator, "onLine", { configurable: true, value: true });
    expect(readBrowserOnline()).toBe(true);
    Object.defineProperty(navigator, "onLine", { configurable: true, value: prev });
  });
});
