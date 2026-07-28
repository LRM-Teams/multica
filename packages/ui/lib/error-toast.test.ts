import { describe, expect, it, vi } from "vitest";

const toastMock = vi.hoisted(() => ({ error: vi.fn() }));
vi.mock("sonner", () => ({ toast: toastMock }));

const { showErrorToast } = await import("./error-toast");

describe("showErrorToast (#835/#836)", () => {
  it("never lets a failure age out, and always offers a way to close it", () => {
    // sonner's default lifetime is 4s and a 4th toast evicts an unresolved one.
    // A failure is unresolved state: it must persist until the user dismisses it,
    // and it must be dismissible (otherwise a permanent toast is just clutter).
    showErrorToast("boom");
    expect(toastMock.error).toHaveBeenCalledWith("boom", {
      duration: Infinity,
      closeButton: true,
    });
  });
});
