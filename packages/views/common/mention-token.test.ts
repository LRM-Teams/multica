import { describe, expect, it } from "vitest";
import {
  mentionTokenClassName,
  resolveMentionTokenKind,
} from "./mention-token";

describe("resolveMentionTokenKind", () => {
  it("maps @all to the all kind", () => {
    expect(resolveMentionTokenKind("all", "all", "user-1")).toBe("all");
  });

  it("maps a member mention of the viewer to self", () => {
    expect(resolveMentionTokenKind("member", "user-1", "user-1")).toBe("self");
  });

  it("keeps other members, agents, and squads on default", () => {
    expect(resolveMentionTokenKind("member", "user-2", "user-1")).toBe("default");
    expect(resolveMentionTokenKind("agent", "agent-1", "user-1")).toBe("default");
    expect(resolveMentionTokenKind("squad", "squad-1", "user-1")).toBe("default");
  });

  it("does not treat agent ids as self even when they match the viewer id", () => {
    expect(resolveMentionTokenKind("agent", "user-1", "user-1")).toBe("default");
  });
});

describe("mentionTokenClassName", () => {
  it("uses brand-ink prose with no rest-state fill", () => {
    const cls = mentionTokenClassName("default");
    expect(cls).toContain("text-brand");
    expect(cls).toContain("font-medium");
    expect(cls).toContain("mention");
    expect(cls.split(/\s+/).some((c) => c.startsWith("bg-"))).toBe(false);
  });

  it("emphasizes @all with weight only, not a permanent wash", () => {
    const cls = mentionTokenClassName("all");
    expect(cls).toContain("text-brand");
    expect(cls).toContain("font-semibold");
    expect(cls.split(/\s+/).some((c) => c.startsWith("bg-"))).toBe(false);
  });

  it("emphasizes self with weight only (row wash is separate)", () => {
    const cls = mentionTokenClassName("self");
    expect(cls).toContain("text-brand");
    expect(cls).toContain("font-semibold");
    expect(cls.split(/\s+/).some((c) => c.startsWith("bg-"))).toBe(false);
  });
});
