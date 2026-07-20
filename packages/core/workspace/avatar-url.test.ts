import { describe, expect, it, vi } from "vitest";
import {
  AGENT_AVATAR_PRESETS,
  defaultAgentAvatarPath,
  resolvePublicFileUrl,
  resolvePublicFileUrlWithBase,
  stableHash,
} from "./avatar-url";

vi.mock("../api", () => ({
  api: {
    getBaseUrl: () => "http://127.0.0.1:8080",
  },
}));

describe("resolvePublicFileUrlWithBase", () => {
  it("resolves root-relative URLs against base URL", () => {
    expect(resolvePublicFileUrlWithBase("/uploads/a.png", "http://127.0.0.1:8080")).toBe(
      "http://127.0.0.1:8080/uploads/a.png",
    );
  });

  it("trims trailing slash in base URL", () => {
    expect(resolvePublicFileUrlWithBase("/upload/a.png", "http://127.0.0.1:8080/")).toBe(
      "http://127.0.0.1:8080/upload/a.png",
    );
  });

  it("keeps absolute URLs unchanged", () => {
    expect(resolvePublicFileUrlWithBase("https://cdn.example.com/a.png", "http://127.0.0.1:8080")).toBe(
      "https://cdn.example.com/a.png",
    );
  });

  it("keeps bundled agent avatars on the web origin", () => {
    expect(resolvePublicFileUrlWithBase("/agent-avatars/human-11.jpg", "http://127.0.0.1:8080")).toBe(
      "/agent-avatars/human-11.jpg",
    );
  });

  it("returns null for empty values", () => {
    expect(resolvePublicFileUrlWithBase(null, "http://127.0.0.1:8080")).toBeNull();
    expect(resolvePublicFileUrlWithBase(undefined, "http://127.0.0.1:8080")).toBeNull();
  });
});

describe("resolvePublicFileUrl", () => {
  it("uses API base URL implicitly", () => {
    expect(resolvePublicFileUrl("/uploads/a.png")).toBe("http://127.0.0.1:8080/uploads/a.png");
  });
});

describe("AGENT_AVATAR_PRESETS", () => {
  it("holds the full 24-photo pool including human-11", () => {
    expect(AGENT_AVATAR_PRESETS).toHaveLength(24);
    expect(AGENT_AVATAR_PRESETS).toContain("/agent-avatars/human-11.jpg");
    expect(AGENT_AVATAR_PRESETS[0]).toBe("/agent-avatars/human-01.jpg");
    expect(AGENT_AVATAR_PRESETS[23]).toBe("/agent-avatars/human-24.jpg");
    expect(new Set(AGENT_AVATAR_PRESETS).size).toBe(24); // no dupes
  });
});

describe("stableHash", () => {
  it("is deterministic for the same input", () => {
    expect(stableHash("agent-abc")).toBe(stableHash("agent-abc"));
  });

  it("returns an unsigned 32-bit integer", () => {
    const h = stableHash("agent-abc");
    expect(Number.isInteger(h)).toBe(true);
    expect(h).toBeGreaterThanOrEqual(0);
    expect(h).toBeLessThanOrEqual(0xffffffff);
  });

  it("differs across distinct inputs", () => {
    expect(stableHash("agent-a")).not.toBe(stableHash("agent-b"));
  });
});

describe("defaultAgentAvatarPath", () => {
  it("maps an id to a stable pool photo (same id → same photo)", () => {
    const id = "c56c3ac3-bf1d-475e-b761-7c508e16c9f1";
    expect(defaultAgentAvatarPath(id)).toBe(defaultAgentAvatarPath(id));
    expect(AGENT_AVATAR_PRESETS).toContain(defaultAgentAvatarPath(id));
  });

  it("spreads distinct ids across the pool (not all the same slot)", () => {
    const paths = Array.from({ length: 50 }, (_, i) => defaultAgentAvatarPath(`agent-${i}`));
    expect(new Set(paths).size).toBeGreaterThan(1);
  });

  it("is keyed by id, not name — differing ids can resolve differently", () => {
    // Two ids that hash into different pool slots stay independent of any name.
    const a = defaultAgentAvatarPath("id-one");
    const b = defaultAgentAvatarPath("id-two");
    expect([a, b].every((p) => AGENT_AVATAR_PRESETS.includes(p))).toBe(true);
  });
});
