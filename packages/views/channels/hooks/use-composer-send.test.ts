import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "@multica/core/api";
import { composePayloadKey } from "./use-compose-send-intent";
import { classifySendFailure, useComposerSend } from "./use-composer-send";

/**
 * A stand-in for a react-query `mutate` that captures the callbacks so a test
 * can drive the 3-way outcome (success / 409 / other) on demand — modelling the
 * real send mutation without a network.
 */
function makeMutate() {
  const calls: Array<{
    onSuccess: () => void;
    onError: (err: unknown) => void;
    onSettled: () => void;
  }> = [];
  const mutate = (
    _vars: unknown,
    cbs: { onSuccess: () => void; onError: (err: unknown) => void; onSettled: () => void },
  ) => {
    calls.push(cbs);
  };
  return { mutate, calls };
}

describe("classifySendFailure", () => {
  it("maps a same-id / different-payload 409 to the conflict branch", () => {
    expect(classifySendFailure(new ApiError("conflict", 409, "Conflict"))).toBe("conflict");
  });

  it("maps 5xx / permission / validation / network to the retry branch", () => {
    expect(classifySendFailure(new ApiError("boom", 500, "Server Error"))).toBe("retry");
    expect(classifySendFailure(new ApiError("nope", 403, "Forbidden"))).toBe("retry");
    expect(classifySendFailure(new ApiError("bad", 422, "Unprocessable"))).toBe("retry");
    expect(classifySendFailure(new TypeError("Failed to fetch"))).toBe("retry");
    expect(classifySendFailure(undefined)).toBe("retry");
  });
});

describe("useComposerSend", () => {
  function run(overrides: Partial<Parameters<ReturnType<typeof useComposerSend>["send"]>[0]> = {}) {
    const mutate = makeMutate();
    const onCommitted = vi.fn();
    const onVisibleError = vi.fn();
    const base = {
      payloadKey: composePayloadKey("hello"),
      buildVars: (id: string) => ({ clientMessageId: id }),
      mutate: mutate.mutate,
      onCommitted,
      onVisibleError,
    };
    return { mutate, onCommitted, onVisibleError, base: { ...base, ...overrides } };
  }

  it("send lock: N rapid triggers dispatch exactly one request", () => {
    const { result } = renderHook(() => useComposerSend());
    const { mutate, base } = run();

    let dispatches: boolean[] = [];
    act(() => {
      dispatches = [base, base, base].map((r) => result.current.send(r));
    });

    // First wins; the held/auto-repeat burst is dropped.
    expect(dispatches).toEqual([true, false, false]);
    expect(mutate.calls).toHaveLength(1);
  });

  it("dispatched vars carry the minted client_message_id", () => {
    const { result } = renderHook(() => useComposerSend());
    const captured: string[] = [];
    const { base } = run({
      buildVars: (id: string) => {
        captured.push(id);
        return { clientMessageId: id };
      },
    });

    act(() => {
      result.current.send(base);
    });
    expect(captured).toHaveLength(1);
    expect(captured[0]).toBeTruthy();
  });

  it("200-dedup success is silent: commit fires, no visible error, lock releases", () => {
    const { result } = renderHook(() => useComposerSend());
    const { mutate, onCommitted, onVisibleError, base } = run();

    act(() => {
      result.current.send(base);
    });
    act(() => {
      mutate.calls[0]!.onSuccess();
      mutate.calls[0]!.onSettled();
    });

    expect(onCommitted).toHaveBeenCalledTimes(1);
    expect(onVisibleError).not.toHaveBeenCalled();

    // Lock released → a subsequent send dispatches again.
    let second = false;
    act(() => {
      second = result.current.send(base);
    });
    expect(second).toBe(true);
    expect(mutate.calls).toHaveLength(2);
  });

  it("409: visible conflict error, then recovers with a fresh intent", () => {
    const { result } = renderHook(() => useComposerSend());
    const { mutate, onCommitted, onVisibleError, base } = run();

    act(() => {
      result.current.send(base);
    });
    act(() => {
      mutate.calls[0]!.onError(new ApiError("conflict", 409, "Conflict"));
      mutate.calls[0]!.onSettled();
    });

    expect(onCommitted).not.toHaveBeenCalled();
    expect(onVisibleError).toHaveBeenCalledWith("conflict");

    // resetIntent recovered the lock → the retry dispatches again (not soft-locked).
    let retried = false;
    act(() => {
      retried = result.current.send(base);
    });
    expect(retried).toBe(true);
    expect(mutate.calls).toHaveLength(2);
  });

  it("net/5xx/perm/validation: visible retryable error, lock releases", () => {
    const { result } = renderHook(() => useComposerSend());
    const { mutate, onCommitted, onVisibleError, base } = run();

    act(() => {
      result.current.send(base);
    });
    act(() => {
      mutate.calls[0]!.onError(new ApiError("boom", 500, "Server Error"));
      mutate.calls[0]!.onSettled();
    });

    expect(onCommitted).not.toHaveBeenCalled();
    expect(onVisibleError).toHaveBeenCalledWith("retry");

    let retried = false;
    act(() => {
      retried = result.current.send(base);
    });
    expect(retried).toBe(true);
  });

  it("never leaves the lock stuck: onSettled always re-enables sending", () => {
    const { result } = renderHook(() => useComposerSend());
    const { mutate, base } = run();

    act(() => {
      result.current.send(base);
    });
    // While in flight (no settle yet) a second trigger is blocked.
    let blocked = true;
    act(() => {
      blocked = result.current.send(base);
    });
    expect(blocked).toBe(false);

    act(() => {
      mutate.calls[0]!.onSettled();
    });
    let allowed = false;
    act(() => {
      allowed = result.current.send(base);
    });
    expect(allowed).toBe(true);
  });
});
