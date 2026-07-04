import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DMItem } from "@multica/core/dm";
import { evaluateSendToTarget, useSendTo } from "./use-send-to";

const ME = "user-me";
const WS = "ws-1";

// Mocked deps so the hook runs without stores / network.
const mutate = vi.fn();
vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (s: { user: { id: string } }) => unknown) =>
    selector({ user: { id: ME } }),
}));
vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => WS,
}));
vi.mock("@multica/core/dm", () => ({
  useCreateOrFindDM: () => ({ mutate }),
}));

beforeEach(() => {
  mutate.mockReset();
});

describe("evaluateSendToTarget", () => {
  const ctx = { currentUserId: ME, workspaceId: WS };

  it("accepts a reachable same-workspace peer", () => {
    expect(evaluateSendToTarget({ type: "agent", id: "agent-1" }, ctx)).toEqual({
      ok: true,
      target: { peer_type: "agent", peer_id: "agent-1" },
    });
  });

  it("rejects a self-DM target", () => {
    expect(evaluateSendToTarget({ type: "user", id: ME }, ctx)).toEqual({
      ok: false,
      reason: "self",
    });
  });

  it("rejects a cross-workspace target", () => {
    expect(
      evaluateSendToTarget({ type: "agent", id: "agent-x", workspaceId: "ws-other" }, ctx),
    ).toEqual({ ok: false, reason: "cross_workspace" });
  });

  it("rejects an unresolved (missing) target", () => {
    expect(evaluateSendToTarget(null, ctx)).toEqual({ ok: false, reason: "unresolved" });
  });
});

describe("useSendTo — non-leaky guardrails", () => {
  it("self-DM: rejected before any server call, generic unavailable path", () => {
    const { result } = renderHook(() => useSendTo());
    const onResolved = vi.fn();
    const onUnavailable = vi.fn();

    act(() => {
      result.current.sendTo({ type: "user", id: ME }, { onResolved, onUnavailable });
    });

    expect(onResolved).not.toHaveBeenCalled();
    // Never hits the server — no chance to disclose target existence.
    expect(mutate).not.toHaveBeenCalled();
    // Zero-arg: structurally cannot carry a reason to the visible layer.
    expect(onUnavailable).toHaveBeenCalledWith();
  });

  it("cross-workspace: rejected before any server call, generic unavailable path", () => {
    const { result } = renderHook(() => useSendTo());
    const onResolved = vi.fn();
    const onUnavailable = vi.fn();

    act(() => {
      result.current.sendTo(
        { type: "agent", id: "agent-x", workspaceId: "ws-other" },
        { onResolved, onUnavailable },
      );
    });

    expect(onResolved).not.toHaveBeenCalled();
    expect(mutate).not.toHaveBeenCalled();
    expect(onUnavailable).toHaveBeenCalledWith();
  });

  it("target-not-found / unauthorized (server error): SAME generic unavailable path", () => {
    // A reachable-looking target whose create-or-find fails server-side (the
    // target does not exist, or the caller is not allowed to reach it). This
    // MUST be indistinguishable from self / cross-workspace above.
    mutate.mockImplementation(
      (_body: unknown, cbs: { onError: (e: unknown) => void }) => cbs.onError(new Error("404")),
    );
    const { result } = renderHook(() => useSendTo());
    const onResolved = vi.fn();
    const onUnavailable = vi.fn();

    act(() => {
      result.current.sendTo({ type: "user", id: "user-ghost" }, { onResolved, onUnavailable });
    });

    expect(onResolved).not.toHaveBeenCalled();
    expect(onUnavailable).toHaveBeenCalledWith();
  });

  it("reachable target: resolves the DM to send into", () => {
    const dm = { id: "dm-1", source: "dm_channel" } as DMItem;
    mutate.mockImplementation(
      (_body: unknown, cbs: { onSuccess: (dm: DMItem) => void }) => cbs.onSuccess(dm),
    );
    const { result } = renderHook(() => useSendTo());
    const onResolved = vi.fn();
    const onUnavailable = vi.fn();

    act(() => {
      result.current.sendTo({ type: "agent", id: "agent-1" }, { onResolved, onUnavailable });
    });

    expect(mutate).toHaveBeenCalledWith(
      { peer_type: "agent", peer_id: "agent-1" },
      expect.anything(),
    );
    expect(onResolved).toHaveBeenCalledWith(dm);
    expect(onUnavailable).not.toHaveBeenCalled();
  });
});
