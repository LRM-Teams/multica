import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useJumpNotFoundToast } from "./use-jump-not-found-toast";

const showErrorToast = vi.fn();
vi.mock("@multica/ui/lib/error-toast", () => ({
  showErrorToast: (...args: unknown[]) => showErrorToast(...args),
}));

beforeEach(() => {
  showErrorToast.mockReset();
});

describe("useJumpNotFoundToast (LRM-736)", () => {
  it("toasts once when a target is missing", () => {
    const { rerender } = renderHook(
      ({ missing, targetId }) =>
        useJumpNotFoundToast({ missing, targetId, message: "gone" }),
      { initialProps: { missing: false, targetId: "msg-1" as string | null } },
    );
    expect(showErrorToast).not.toHaveBeenCalled();

    act(() => {
      rerender({ missing: true, targetId: "msg-1" });
    });
    expect(showErrorToast).toHaveBeenCalledTimes(1);
    expect(showErrorToast).toHaveBeenCalledWith("gone");

    act(() => {
      rerender({ missing: true, targetId: "msg-1" });
    });
    expect(showErrorToast).toHaveBeenCalledTimes(1);
  });

  it("toasts again for a new target id", () => {
    const { rerender } = renderHook(
      ({ missing, targetId }) =>
        useJumpNotFoundToast({ missing, targetId, message: "gone" }),
      { initialProps: { missing: true, targetId: "msg-1" as string | null } },
    );
    expect(showErrorToast).toHaveBeenCalledTimes(1);

    act(() => {
      rerender({ missing: false, targetId: null });
    });
    act(() => {
      rerender({ missing: true, targetId: "msg-2" });
    });
    expect(showErrorToast).toHaveBeenCalledTimes(2);
  });

  it("does not toast without a target id", () => {
    renderHook(() =>
      useJumpNotFoundToast({ missing: true, targetId: null, message: "gone" }),
    );
    expect(showErrorToast).not.toHaveBeenCalled();
  });
});
