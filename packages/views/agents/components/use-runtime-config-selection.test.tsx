// @vitest-environment jsdom

import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { RuntimeDevice } from "@multica/core/types";
import { useRuntimeConfigSelection } from "./use-runtime-config-selection";

vi.mock("../../runtimes/components/runtime-machines", () => ({
  buildRuntimeMachines: (runtimes: RuntimeDevice[]) => {
    const grouped = new Map<string, RuntimeDevice[]>();
    for (const runtime of runtimes) {
      const id = runtime.daemon_id ?? runtime.id;
      grouped.set(id, [...(grouped.get(id) ?? []), runtime]);
    }
    return [...grouped.entries()].map(([id, machineRuntimes]) => ({
      id,
      daemonId: id,
      title: id,
      runtimes: machineRuntimes,
      health: "online",
    }));
  },
}));

function runtime(id: string, daemonId = "machine-1"): RuntimeDevice {
  return {
    id,
    daemon_id: daemonId,
    status: "online",
    provider: "codex",
    runtime_mode: "local",
  } as RuntimeDevice;
}

describe("useRuntimeConfigSelection cascade identity", () => {
  it("does not clear model or reasoning when reselecting the current runtime", () => {
    const { result } = renderHook(() =>
      useRuntimeConfigSelection({
        runtimes: [runtime("runtime-1")],
        currentUserId: "user-1",
        initialRuntimeId: "runtime-1",
        initialModel: "gpt-5.6-sol",
        initialThinkingLevel: "high",
      }),
    );

    act(() => result.current.selectRuntime("runtime-1"));

    expect(result.current.model).toBe("gpt-5.6-sol");
    expect(result.current.thinkingLevel).toBe("high");
  });

  it("does not clear reasoning when reselecting the current model", () => {
    const { result } = renderHook(() =>
      useRuntimeConfigSelection({
        runtimes: [runtime("runtime-1")],
        currentUserId: "user-1",
        initialRuntimeId: "runtime-1",
        initialModel: "gpt-5.6-sol",
        initialThinkingLevel: "high",
      }),
    );

    act(() => result.current.selectModel("gpt-5.6-sol"));

    expect(result.current.thinkingLevel).toBe("high");
  });

  it("preserves an initial binding while runtimes load asynchronously", () => {
    const { result, rerender } = renderHook(
      ({ runtimes }: { runtimes: RuntimeDevice[] }) =>
        useRuntimeConfigSelection({
          runtimes,
          currentUserId: "user-1",
          initialRuntimeId: "saved-runtime",
          initialModel: "saved-model",
          initialThinkingLevel: "high",
        }),
      { initialProps: { runtimes: [] as RuntimeDevice[] } },
    );

    expect(result.current.runtimeId).toBe("saved-runtime");
    expect(result.current.model).toBe("saved-model");
    expect(result.current.thinkingLevel).toBe("high");

    rerender({
      runtimes: [
        runtime("unrelated-runtime", "machine-a"),
        runtime("saved-runtime", "machine-b"),
      ],
    });

    expect(result.current.machineId).toBe("machine-b");
    expect(result.current.runtimeId).toBe("saved-runtime");
    expect(result.current.model).toBe("saved-model");
    expect(result.current.thinkingLevel).toBe("high");
  });
});
