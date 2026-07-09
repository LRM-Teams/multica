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
    // Agent and user namespaces can theoretically collide as strings; only
    // member mentions address a human viewer.
    expect(resolveMentionTokenKind("agent", "user-1", "user-1")).toBe("default");
  });
});

describe("mentionTokenClassName", () => {
  it("always includes the shared brand semantic base", () => {
    const cls = mentionTokenClassName("default");
    expect(cls).toContain("text-brand");
    expect(cls).toContain("bg-brand/10");
    expect(cls).toContain("mention");
  });

  it("adds light emphasis for @all without leaving the brand family", () => {
    const cls = mentionTokenClassName("all");
    expect(cls).toContain("text-brand");
    expect(cls).toContain("bg-brand/[0.16]");
    expect(cls).toContain("ring-brand/20");
  });

  it("adds a lighter self emphasis for the viewer's own mention token", () => {
    const cls = mentionTokenClassName("self");
    expect(cls).toContain("text-brand");
    expect(cls).toContain("bg-brand/[0.14]");
  });
});
