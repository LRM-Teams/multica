// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { onlineManager, QueryClient, QueryObserver } from "@tanstack/react-query";
import {
  installQueryOnlineRecovery,
  resetQueryOnlineRecoveryForTests,
} from "./query-online";

describe("installQueryOnlineRecovery (LRM-844)", () => {
  beforeEach(() => {
    resetQueryOnlineRecoveryForTests();
    onlineManager.setOnline(true);
  });

  afterEach(() => {
    resetQueryOnlineRecoveryForTests();
    onlineManager.setOnline(true);
  });

  it("re-asserts online immediately and recovers a false offline latch", () => {
    onlineManager.setOnline(false);
    expect(onlineManager.isOnline()).toBe(false);
    installQueryOnlineRecovery();
    expect(onlineManager.isOnline()).toBe(true);
  });

  it("resumes a paused first-paint query when focus fires after a missed online", async () => {
    installQueryOnlineRecovery();
    const qc = new QueryClient({
      defaultOptions: {
        queries: {
          networkMode: "online",
          retry: false,
          staleTime: Infinity,
          refetchOnReconnect: true,
        },
      },
    });
    qc.mount();

    let fetches = 0;
    onlineManager.setOnline(false);
    const observer = new QueryObserver(qc, {
      queryKey: ["dm", "ws", "list"],
      queryFn: async () => {
        fetches += 1;
        return [];
      },
    });
    const unsub = observer.subscribe(() => {});
    await vi.waitFor(() => {
      expect(observer.getCurrentResult().isPaused).toBe(true);
    });
    expect(fetches).toBe(0);

    // Missed `online` event — only focus recovery (our listener) fires.
    window.dispatchEvent(new Event("focus"));
    await vi.waitFor(() => {
      expect(observer.getCurrentResult().status).toBe("success");
    });
    expect(fetches).toBe(1);
    expect(onlineManager.isOnline()).toBe(true);

    unsub();
    qc.unmount();
  });

  it("is idempotent across createQueryClient-style repeat calls", () => {
    installQueryOnlineRecovery();
    installQueryOnlineRecovery();
    onlineManager.setOnline(false);
    window.dispatchEvent(new Event("focus"));
    expect(onlineManager.isOnline()).toBe(true);
  });

  // AC2 stand-in at query layer: 20 consecutive cold-start false-offline
  // latches must each recover (focus path) so dm-list cannot stay paused.
  it("recovers 20 consecutive cold-start false-offline latches", async () => {
    for (let i = 0; i < 20; i++) {
      resetQueryOnlineRecoveryForTests();
      onlineManager.setOnline(true);
      installQueryOnlineRecovery();

      const qc = new QueryClient({
        defaultOptions: {
          queries: {
            networkMode: "online",
            retry: false,
            staleTime: Infinity,
            refetchOnReconnect: true,
          },
        },
      });
      qc.mount();

      let fetches = 0;
      onlineManager.setOnline(false);
      const observer = new QueryObserver(qc, {
        queryKey: ["dm", "ws", "list", i],
        queryFn: async () => {
          fetches += 1;
          return [];
        },
      });
      const unsub = observer.subscribe(() => {});
      await vi.waitFor(() => {
        expect(observer.getCurrentResult().isPaused).toBe(true);
      });

      window.dispatchEvent(new Event("focus"));
      await vi.waitFor(() => {
        expect(observer.getCurrentResult().status).toBe("success");
      });
      expect(fetches).toBe(1);
      expect(onlineManager.isOnline()).toBe(true);

      unsub();
      qc.unmount();
    }
  });
});
