import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useComposerSendRestore } from "./use-composer-send-restore";

describe("useComposerSendRestore", () => {
  describe("persisted mode (main composer)", () => {
    it("restores the failed text via persist + bumps the nonce when the composer is unchanged", () => {
      const persist = vi.fn();
      const { result } = renderHook(() => useComposerSendRestore(persist));

      const nonceBefore = result.current.nonce;
      act(() => result.current.onFailed("hello", "hello"));

      expect(persist).toHaveBeenCalledExactlyOnceWith("hello");
      expect(result.current.nonce).toBe(nonceBefore + 1);
      expect(result.current.error).toEqual({ conflicted: false, tooLong: false });
    });

    it("keeps the composer's new text (no auto-cover) and flags conflicted", () => {
      const persist = vi.fn();
      const { result } = renderHook(() => useComposerSendRestore(persist));

      const nonceBefore = result.current.nonce;
      act(() => result.current.onFailed("failed text", "user's newer text"));

      // Iris A6 hard rule: never auto-cover the user's new input.
      expect(persist).not.toHaveBeenCalled();
      expect(result.current.nonce).toBe(nonceBefore);
      expect(result.current.error).toEqual({ conflicted: true, tooLong: false });
    });

    it("flags tooLong (413) while still restoring the text — shorten-and-retry, not lost", () => {
      const persist = vi.fn();
      const { result } = renderHook(() => useComposerSendRestore(persist));

      act(() => result.current.onFailed("way too long", "way too long", true));

      expect(persist).toHaveBeenCalledExactlyOnceWith("way too long");
      expect(result.current.error).toEqual({ conflicted: false, tooLong: true });
    });

    it("restorePrevious puts the kept-back text back and clears the error", () => {
      const persist = vi.fn();
      const { result } = renderHook(() => useComposerSendRestore(persist));

      act(() => result.current.onFailed("failed text", "user's newer text"));
      const nonceAfterFail = result.current.nonce;

      act(() => result.current.restorePrevious());

      expect(persist).toHaveBeenCalledExactlyOnceWith("failed text");
      expect(result.current.nonce).toBe(nonceAfterFail + 1);
      expect(result.current.error).toBeNull();
    });

    it("uses the latest persist closure across renders", () => {
      const first = vi.fn();
      const second = vi.fn();
      const { result, rerender } = renderHook(
        ({ p }) => useComposerSendRestore(p),
        { initialProps: { p: first } },
      );

      rerender({ p: second });
      act(() => result.current.onFailed("x", "x"));

      expect(first).not.toHaveBeenCalled();
      expect(second).toHaveBeenCalledExactlyOnceWith("x");
    });
  });

  describe("local mode (thread composer, no persistent draft)", () => {
    it("restores the failed text into restoreText when the composer is unchanged", () => {
      const { result } = renderHook(() => useComposerSendRestore());

      act(() => result.current.onFailed("thread reply", "thread reply"));

      expect(result.current.restoreText).toBe("thread reply");
      expect(result.current.error).toEqual({ conflicted: false, tooLong: false });
    });

    it("does not touch restoreText on conflict", () => {
      const { result } = renderHook(() => useComposerSendRestore());

      act(() => result.current.onFailed("failed", "typed something else"));

      expect(result.current.restoreText).toBe("");
      expect(result.current.error).toEqual({ conflicted: true, tooLong: false });
    });
  });

  describe("clearing", () => {
    it("clear() drops the error but leaves restoreText for a mid-flight restore", () => {
      const { result } = renderHook(() => useComposerSendRestore());
      act(() => result.current.onFailed("t", "t"));
      expect(result.current.restoreText).toBe("t");

      act(() => result.current.clear());

      expect(result.current.error).toBeNull();
      expect(result.current.restoreText).toBe("t");
    });

    it("reset() drops both the error and the restore text (new send dispatched)", () => {
      const { result } = renderHook(() => useComposerSendRestore());
      act(() => result.current.onFailed("t", "t"));

      act(() => result.current.reset());

      expect(result.current.error).toBeNull();
      expect(result.current.restoreText).toBe("");
    });
  });
});
