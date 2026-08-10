import { describe, expect, it, vi } from "vitest";
import {
  AGENT_AVATAR_PRESETS,
  isLegacyUploadsAvatarUrl,
  preferAuthorAvatarUrl,
  resolvePublicFileUrl,
  resolvePublicFileUrlWithBase,
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

  it("prefixes agent presets when an API base is set (desktop / remote API)", () => {
    expect(resolvePublicFileUrlWithBase("/agent-avatars/human-11.jpg", "http://127.0.0.1:8080")).toBe(
      "http://127.0.0.1:8080/agent-avatars/human-11.jpg?v=lrm218",
    );
  });

  it("keeps root-relative agent presets on same-origin web with cache-bust", () => {
    expect(resolvePublicFileUrlWithBase("/agent-avatars/human-11.jpg", "")).toBe(
      "/agent-avatars/human-11.jpg?v=lrm218",
    );
    expect(resolvePublicFileUrlWithBase("/uploads/a.png", "")).toBe("/uploads/a.png");
  });

  it("does not double-append the agent-avatar cache-bust token", () => {
    expect(
      resolvePublicFileUrlWithBase("/agent-avatars/human-11.jpg?v=lrm218", "http://127.0.0.1:8080"),
    ).toBe("http://127.0.0.1:8080/agent-avatars/human-11.jpg?v=lrm218");
    expect(
      resolvePublicFileUrlWithBase("/agent-avatars/human-11.jpg?v=lrm218", ""),
    ).toBe("/agent-avatars/human-11.jpg?v=lrm218");
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
  it("holds the full immutable OSS-backed pool", () => {
    expect(AGENT_AVATAR_PRESETS).toHaveLength(15);
    expect(AGENT_AVATAR_PRESETS).toContain(
      "https://cdn.leagent.me/agent-avatars/v2/agent-11.png",
    );
    expect(AGENT_AVATAR_PRESETS[0]).toBe(
      "https://cdn.leagent.me/agent-avatars/v2/agent-01.png",
    );
    expect(AGENT_AVATAR_PRESETS[14]).toBe(
      "https://cdn.leagent.me/agent-avatars/v2/agent-15.png",
    );
    expect(new Set(AGENT_AVATAR_PRESETS).size).toBe(15); // no dupes
  });
});

describe("isLegacyUploadsAvatarUrl / preferAuthorAvatarUrl (LRM-855)", () => {
  it("detects relative and absolute /uploads/ paths", () => {
    expect(isLegacyUploadsAvatarUrl("/uploads/a.png")).toBe(true);
    expect(isLegacyUploadsAvatarUrl("https://cdn.example.com/uploads/a.png")).toBe(true);
    expect(isLegacyUploadsAvatarUrl("https://cdn.example.com/avatars/a.png")).toBe(false);
    expect(isLegacyUploadsAvatarUrl("/agent-avatars/human-01.jpg")).toBe(false);
  });

  it("lets OSS incoming replace cached /uploads/", () => {
    expect(
      preferAuthorAvatarUrl(
        "https://cdn.example.com/avatars/a.png",
        "/uploads/old.png",
      ),
    ).toBe("https://cdn.example.com/avatars/a.png");
  });

  it("keeps cached OSS when incoming is stale /uploads/", () => {
    expect(
      preferAuthorAvatarUrl(
        "/uploads/stale.png",
        "https://cdn.example.com/avatars/a.png",
      ),
    ).toBe("https://cdn.example.com/avatars/a.png");
  });

  it("falls back to cache when incoming omits the URL", () => {
    expect(preferAuthorAvatarUrl(null, "/uploads/keep.png")).toBe("/uploads/keep.png");
  });
});
