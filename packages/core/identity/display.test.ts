import { describe, expect, it } from "vitest";
import {
  computeDuplicatedHandleLabels,
  formatActorHandleLabel,
  normalizeActorHandle,
  resolveActorDisplayName,
  resolveActorHandle,
  resolveActorIdentityPresentation,
  shouldShowActorHandleLabel,
} from "./display";

describe("normalizeActorHandle", () => {
  it("strips leading @ and whitespace", () => {
    expect(normalizeActorHandle("  @alice  ")).toBe("alice");
  });

  it("returns empty string for nullish input", () => {
    expect(normalizeActorHandle(null)).toBe("");
    expect(normalizeActorHandle(undefined)).toBe("");
  });
});

describe("resolveActorDisplayName", () => {
  it("prefers display_name over name", () => {
    expect(resolveActorDisplayName({ display_name: "Alice Zhang", name: "alice" }, "fallback")).toBe(
      "Alice Zhang",
    );
  });

  it("falls back to name then explicit fallback", () => {
    expect(resolveActorDisplayName({ display_name: "", name: "alice" }, "fallback")).toBe("alice");
    expect(resolveActorDisplayName({ display_name: "  ", name: "" }, "fallback")).toBe("fallback");
    expect(resolveActorDisplayName(null, "fallback")).toBe("fallback");
  });
});

describe("formatActorHandleLabel", () => {
  it("formats @handle labels", () => {
    expect(formatActorHandleLabel("alice")).toBe("@alice");
    expect(formatActorHandleLabel("@alice")).toBe("@alice");
    expect(formatActorHandleLabel("")).toBeNull();
  });
});

describe("shouldShowActorHandleLabel", () => {
  it("hides handle when primary label equals handle", () => {
    expect(shouldShowActorHandleLabel("alice", "alice")).toBe(false);
    expect(shouldShowActorHandleLabel("Alice", "alice")).toBe(false);
  });

  it("shows handle when display name differs", () => {
    expect(shouldShowActorHandleLabel("Alice Zhang", "alice")).toBe(true);
  });
});

describe("resolveActorIdentityPresentation", () => {
  it("returns a full presentation tuple", () => {
    expect(
      resolveActorIdentityPresentation({ display_name: "Aegis", name: "agent_aegis" }, "Agent"),
    ).toEqual({
      displayName: "Aegis",
      handle: "agent_aegis",
      handleLabel: "@agent_aegis",
      showHandleLabel: true,
    });
  });

  it("hides secondary handle when display falls back to name", () => {
    expect(resolveActorIdentityPresentation({ display_name: "", name: "alice" }, "Unknown")).toEqual({
      displayName: "alice",
      handle: "alice",
      handleLabel: "@alice",
      showHandleLabel: false,
    });
  });
});

describe("resolveActorHandle", () => {
  it("reads the stable handle from name", () => {
    expect(resolveActorHandle({ display_name: "Alice", name: "@alice" })).toBe("alice");
  });

  it("falls back to a normalized fallback handle", () => {
    expect(resolveActorHandle({ display_name: "Alice", name: "" }, "@legacy")).toBe("legacy");
    expect(resolveActorHandle(null, "@legacy")).toBe("legacy");
  });
});

describe("computeDuplicatedHandleLabels", () => {
  it("labels only actors whose display name collides", () => {
    const labels = computeDuplicatedHandleLabels([
      { id: "a1", display_name: "贝克汉姆", name: "beckham-lrm2" },
      { id: "a2", display_name: "贝克汉姆", name: "beckham-ops" },
      { id: "a3", display_name: "Wendy", name: "wendy" },
    ]);
    expect(labels.get("a1")).toBe("@beckham-lrm2");
    expect(labels.get("a2")).toBe("@beckham-ops");
    expect(labels.has("a3")).toBe(false);
    expect(labels.size).toBe(2);
  });

  it("matches display names case-insensitively and trims whitespace", () => {
    const labels = computeDuplicatedHandleLabels([
      { id: "a1", display_name: "Wendy", name: "wendy-a" },
      { id: "a2", display_name: " wendy ", name: "wendy-b" },
      { id: "a3", display_name: "WENDY2", name: "wendy-c" },
    ]);
    expect(labels.get("a1")).toBe("@wendy-a");
    expect(labels.get("a2")).toBe("@wendy-b");
    expect(labels.has("a3")).toBe(false);
  });

  it("never collides when display falls back to the unique handle", () => {
    const labels = computeDuplicatedHandleLabels([
      { id: "a1", display_name: "", name: "beckham-lrm2" },
      { id: "a2", display_name: "", name: "beckham-ops" },
    ]);
    expect(labels.size).toBe(0);
  });

  it("skips colliding actors that have no usable handle", () => {
    const labels = computeDuplicatedHandleLabels([
      { id: "a1", display_name: "贝克汉姆", name: "beckham-lrm2" },
      { id: "a2", display_name: "贝克汉姆", name: "  " },
    ]);
    expect(labels.get("a1")).toBe("@beckham-lrm2");
    expect(labels.has("a2")).toBe(false);
  });
});
