import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { composePayloadKey, useComposeSendIntent } from "./use-compose-send-intent";

// Deterministic ids so we can assert reuse vs. re-mint.
let idCounter = 0;
vi.mock("@multica/core/utils", () => ({
  createSafeId: () => `id-${++idCounter}`,
}));

beforeEach(() => {
  idCounter = 0;
});

describe("composePayloadKey", () => {
  it("is stable for identical content + attachments", () => {
    expect(composePayloadKey("hi", ["a"])).toBe(composePayloadKey("hi", ["a"]));
  });

  it("differs when the text changes", () => {
    expect(composePayloadKey("hi")).not.toBe(composePayloadKey("hi there"));
  });

  it("differs when the bound attachments change", () => {
    expect(composePayloadKey("hi")).not.toBe(composePayloadKey("hi", ["a"]));
    expect(composePayloadKey("hi", ["a"])).not.toBe(composePayloadKey("hi", ["a", "b"]));
  });

  it("cannot alias a different content/attachment split", () => {
    // Without a separator, ("a","b"+"c") could collide with ("a"+"b","c").
    expect(composePayloadKey("a", ["bc"])).not.toBe(composePayloadKey("ab", ["c"]));
  });

  it("differs by scope so the same text in a different thread is a new intent", () => {
    // Backend treats a differing reply-thread as same-id / different-payload 409.
    expect(composePayloadKey("hi", [], "thread-A")).not.toBe(
      composePayloadKey("hi", [], "thread-B"),
    );
  });
});

describe("useComposeSendIntent", () => {
  it("send lock: a second begin is blocked until the first settles", () => {
    const { result } = renderHook(() => useComposeSendIntent());
    const key = composePayloadKey("hello");

    let first: string | null = null;
    let second: string | null = null;
    act(() => {
      first = result.current.beginSend(key);
      second = result.current.beginSend(key); // held/auto-repeat Enter in same burst
    });
    expect(first).toBe("id-1");
    expect(second).toBeNull(); // blocked → caller aborts → exactly one send

    // After the request settles the lock releases and sending is allowed again.
    act(() => result.current.settleSend());
    let third: string | null = null;
    act(() => {
      third = result.current.beginSend(key);
    });
    expect(third).not.toBeNull();
  });

  it("failure → unchanged retry reuses the same id (backend dedupes)", () => {
    const { result } = renderHook(() => useComposeSendIntent());
    const key = composePayloadKey("hello");

    let attempt1: string | null = null;
    act(() => {
      attempt1 = result.current.beginSend(key);
    });
    // Non-409 error: settle the lock but keep the intent for retry.
    act(() => result.current.settleSend());

    let attempt2: string | null = null;
    act(() => {
      attempt2 = result.current.beginSend(key);
    });
    expect(attempt2).toBe(attempt1); // same id → 200 upsert, no duplicate
  });

  it("failure → edited resend mints a new id (no 409 soft-lock)", () => {
    const { result } = renderHook(() => useComposeSendIntent());

    let attempt1: string | null = null;
    act(() => {
      attempt1 = result.current.beginSend(composePayloadKey("hello"));
    });
    act(() => result.current.settleSend());

    let attempt2: string | null = null;
    act(() => {
      attempt2 = result.current.beginSend(composePayloadKey("hello there")); // edited
    });
    expect(attempt2).not.toBe(attempt1); // new intent → new id
  });

  // Paired UX contract (asserted at the component layer): on a 409 the four send
  // handlers ALSO surface a failure toast — a silent 409 reads to the user as a
  // sent message. The hook's job is the recovery below: the NEXT send is a fresh
  // intent, so the user can simply resend.
  it("409 → resetIntent recovers with a fresh id", () => {
    const { result } = renderHook(() => useComposeSendIntent());
    const key = composePayloadKey("hello");

    let attempt1: string | null = null;
    act(() => {
      attempt1 = result.current.beginSend(key);
    });
    // Backend 409 for this id: abandon the intent and release the lock.
    act(() => {
      result.current.resetIntent();
      result.current.settleSend();
    });

    let attempt2: string | null = null;
    act(() => {
      attempt2 = result.current.beginSend(key);
    });
    expect(attempt2).not.toBe(attempt1); // recovered, not permanently locked
  });

  it("success → the next compose gets a fresh id", () => {
    const { result } = renderHook(() => useComposeSendIntent());
    const key = composePayloadKey("hello");

    let attempt1: string | null = null;
    act(() => {
      attempt1 = result.current.beginSend(key);
    });
    act(() => {
      result.current.finishSend(); // committed
      result.current.settleSend();
    });

    let attempt2: string | null = null;
    act(() => {
      attempt2 = result.current.beginSend(key); // same text, but a brand-new message
    });
    expect(attempt2).not.toBe(attempt1);
  });

  it("attachment path binds the id to the attachments too", () => {
    const { result } = renderHook(() => useComposeSendIntent());

    let attempt1: string | null = null;
    act(() => {
      attempt1 = result.current.beginSend(composePayloadKey("look", ["file-1"]));
    });
    act(() => result.current.settleSend());

    // Same text, attachment removed → different payload → new intent.
    let attempt2: string | null = null;
    act(() => {
      attempt2 = result.current.beginSend(composePayloadKey("look"));
    });
    expect(attempt2).not.toBe(attempt1);
  });
});
