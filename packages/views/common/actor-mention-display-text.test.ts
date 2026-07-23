import { describe, expect, it } from "vitest";
import { resolveActorMentionInk } from "./actor-mention-display-text";

describe("resolveActorMentionInk (LRM-515)", () => {
  it("prefers live display_name over emit-time slug", () => {
    expect(
      resolveActorMentionInk({
        displayName: "贝克汉姆",
        handle: "bei-ke-han-mu-11",
        emitLabel: "@bei-ke-han-mu-11",
      }),
    ).toEqual({ primary: "贝克汉姆", unresolved: false });
  });

  it("grays the handle when display_name is missing (no silent slug-as-name)", () => {
    expect(
      resolveActorMentionInk({
        displayName: null,
        handle: "bei-ke-han-mu-11",
        emitLabel: "@bei-ke-han-mu-11",
      }),
    ).toEqual({ primary: "bei-ke-han-mu-11", unresolved: true });
  });

  it("still brands when the live name equals the handle (identity resolved)", () => {
    expect(
      resolveActorMentionInk({
        displayName: "alice",
        handle: "alice",
        emitLabel: "alice",
      }),
    ).toEqual({ primary: "alice", unresolved: false });
  });

  it("never paints a bare uuid as main ink", () => {
    expect(
      resolveActorMentionInk({
        displayName: "019f8fb3-0be5-7962-bec4-bd63236ed1c5",
        handle: "019f8fb3-0be5-7962-bec4-bd63236ed1c5",
        emitLabel: "019f8fb3-0be5-7962-bec4-bd63236ed1c5",
      }),
    ).toBeNull();
  });

  it("falls back to emit label handle when directory handle is empty", () => {
    expect(
      resolveActorMentionInk({
        displayName: null,
        handle: "",
        emitLabel: "@actor_14",
      }),
    ).toEqual({ primary: "actor_14", unresolved: true });
  });
});
