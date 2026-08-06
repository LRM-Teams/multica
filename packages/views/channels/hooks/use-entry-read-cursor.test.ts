// @vitest-environment jsdom
import { describe, it, expect } from "vitest";
import { renderHook, act } from "@testing-library/react";
import type { MarkChannelReadResult } from "@multica/core/types";
import { useEntryReadCursor } from "./use-entry-read-cursor";

function makeMarkRead() {
  const calls: Array<{
    id: string;
    onSuccess?: (r: MarkChannelReadResult) => void;
  }> = [];
  const fn = (
    id: string,
    opts?: { onSuccess?: (r: MarkChannelReadResult) => void },
  ) => {
    calls.push({ id, onSuccess: opts?.onSuccess });
  };
  return { fn, calls };
}

const echo = (seq: number | null): MarkChannelReadResult => ({
  ok: true,
  previous_last_read_seq: seq,
});

describe("useEntryReadCursor", () => {
  it("returns the payload cursor immediately when present (cached → mount-position)", () => {
    const { fn } = makeMarkRead();
    const { result } = renderHook(() => useEntryReadCursor("c1", 5, fn));
    expect(result.current).toBe(5);
  });

  it("fires mark-read once on entry", () => {
    const { fn, calls } = makeMarkRead();
    renderHook(() => useEntryReadCursor("c1", 5, fn));
    expect(calls).toHaveLength(1);
    expect(calls[0]?.id).toBe("c1");
  });

  it("falls back to the echoed pre-advance cursor when payload is absent (cold-load)", () => {
    const { fn, calls } = makeMarkRead();
    const { result } = renderHook(() => useEntryReadCursor("c1", undefined, fn));
    expect(result.current).toBeNull();
    act(() => calls[0]?.onSuccess?.(echo(3)));
    expect(result.current).toBe(3);
  });

  it("keeps the entry snapshot on re-marks of the same channel (divider stays frozen)", () => {
    const { fn, calls } = makeMarkRead();
    const { result } = renderHook(() => useEntryReadCursor("c1", undefined, fn));
    act(() => calls[0]?.onSuccess?.(echo(3)));
    expect(result.current).toBe(3);
    // A later mark-read for the same channel echoes an advanced cursor — ignored.
    act(() => calls[0]?.onSuccess?.(echo(9)));
    expect(result.current).toBe(3);
  });

  it("ignores a payload that resolves advanced after a cold entry — echo wins (deep-link fix)", () => {
    const { fn, calls } = makeMarkRead();
    const { result, rerender } = renderHook(
      ({ seq }) => useEntryReadCursor("c1", seq, fn),
      { initialProps: { seq: undefined as number | undefined } },
    );
    expect(result.current).toBeNull();
    // On a cold/deep-link open the list resolves AFTER mark-read ran, so it
    // carries the already-advanced cursor. The first-render snapshot was null,
    // so this later value must NOT be used (else the divider would be hidden).
    rerender({ seq: 9 });
    expect(result.current).toBeNull();
    // The echo (pre-advance) arrives and wins — the divider anchors correctly.
    act(() => calls[0]?.onSuccess?.(echo(3)));
    expect(result.current).toBe(3);
  });

  it("re-snapshots for a new conversation", () => {
    const { fn, calls } = makeMarkRead();
    const { result, rerender } = renderHook(
      ({ id }) => useEntryReadCursor(id, undefined, fn),
      { initialProps: { id: "c1" } },
    );
    act(() => calls[0]?.onSuccess?.(echo(3)));
    expect(result.current).toBe(3);

    rerender({ id: "c2" });
    // The c1 echo no longer applies to c2.
    expect(result.current).toBeNull();
    const c2 = calls.find((c) => c.id === "c2");
    act(() => c2?.onSuccess?.(echo(7)));
    expect(result.current).toBe(7);
  });
});
