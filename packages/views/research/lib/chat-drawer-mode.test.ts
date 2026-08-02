import { describe, expect, it } from "vitest";
import { resolveChatDrawerMode } from "./chat-drawer-mode";

describe("resolveChatDrawerMode", () => {
  it("resolves empty vs loading vs running vs error", () => {
    expect(resolveChatDrawerMode(0, "drafting")).toBe("empty");
    expect(resolveChatDrawerMode(0, "completed")).toBe("empty");
    expect(resolveChatDrawerMode(0, "running")).toBe("loading");
    expect(resolveChatDrawerMode(0, "paused")).toBe("loading");
    expect(resolveChatDrawerMode(0, "drafting", { loading: true })).toBe("loading");
    expect(resolveChatDrawerMode(2, "running")).toBe("running");
    expect(resolveChatDrawerMode(1, "drafting")).toBe("running");
    expect(resolveChatDrawerMode(0, "running", { error: true })).toBe("error");
    expect(resolveChatDrawerMode(3, "running", { error: "send failed" })).toBe(
      "error",
    );
  });

  it("prefers error over loading", () => {
    expect(
      resolveChatDrawerMode(0, "running", { loading: true, error: true }),
    ).toBe("error");
  });
});
