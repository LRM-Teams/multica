"use client";

import { create } from "zustand";
import { api, ApiError } from "../api";
import { createSafeId } from "../utils";

export type ComputerUpgradePhase =
  | "pending"
  | "running"
  | "completed"
  | "failed";

export interface ComputerUpgradeRecord {
  daemonId: string;
  machineKey: string;
  runtimeId?: string | null;
  machineTitle?: string | null;
  targetVersion: string;
  requestId: string;
  phase: ComputerUpgradePhase;
  progress?: string | null;
  error?: string | null;
  percent?: number | null;
  startedAt: number;
}

export interface ComputerUpgradeStore {
  upgrades: Record<string, ComputerUpgradeRecord>;
  getUpgrade: (daemonId: string | null | undefined) => ComputerUpgradeRecord | undefined;
  startUpgrade: (params: {
    daemonId: string;
    targetVersion: string;
    machineKey?: string;
    machineTitle?: string;
    runtimeId?: string | null;
    requestId?: string;
  }) => Promise<string>;
  recordProgress: (payload: {
    computer_id: string;
    requestId?: string;
    message?: string;
    phase?: string;
    percent?: number;
  }) => void;
  recordDone: (payload: {
    computer_id: string;
    requestId?: string;
    ok: boolean;
    error?: string;
    newVersion?: string;
  }) => void;
  dismissUpgrade: (daemonId: string) => void;
  clearCompleted: (daemonId: string) => void;
  reset: () => void;
}

export const useComputerUpgradeStore = create<ComputerUpgradeStore>((set, get) => ({
  upgrades: {},
  getUpgrade: (daemonId) => {
    if (!daemonId) return undefined;
    return get().upgrades[daemonId.trim()];
  },
  startUpgrade: async ({
    daemonId,
    targetVersion,
    machineKey,
    machineTitle,
    runtimeId,
    requestId,
  }) => {
    const cleanDaemonId = daemonId.trim();
    const reqId = requestId?.trim() || createSafeId();
    const mKey = machineKey?.trim() || cleanDaemonId;

    set((state) => ({
      upgrades: {
        ...state.upgrades,
        [cleanDaemonId]: {
          daemonId: cleanDaemonId,
          machineKey: mKey,
          runtimeId: runtimeId ?? null,
          machineTitle: machineTitle ?? null,
          targetVersion,
          requestId: reqId,
          phase: "pending",
          error: null,
          startedAt: Date.now(),
        },
      },
    }));

    try {
      await api.initiateMachineUpgrade(cleanDaemonId, targetVersion, reqId);
      set((state) => {
        const current = state.upgrades[cleanDaemonId];
        if (!current || current.requestId !== reqId) return state;
        return {
          upgrades: {
            ...state.upgrades,
            [cleanDaemonId]: {
              ...current,
              phase: "running",
            },
          },
        };
      });
      return reqId;
    } catch (err) {
      set((state) => {
        const current = state.upgrades[cleanDaemonId];
        if (!current || current.requestId !== reqId) return state;
        if (
          err instanceof ApiError &&
          err.status === 409 &&
          err.body &&
          typeof err.body === "object" &&
          (err.body as Record<string, unknown>).code === "runtime_pinned"
        ) {
          const { [cleanDaemonId]: _, ...rest } = state.upgrades;
          return { upgrades: rest };
        }
        let errorMessage: string | null = null;
        if (err instanceof Error && err.message.trim()) {
          errorMessage = err.message;
        }
        return {
          upgrades: {
            ...state.upgrades,
            [cleanDaemonId]: {
              ...current,
              phase: "failed",
              error: errorMessage,
            },
          },
        };
      });
      throw err;
    }
  },
  recordProgress: (payload) => {
    const daemonId = payload.computer_id.trim();
    if (!daemonId) return;
    set((state) => {
      const current = state.upgrades[daemonId];
      if (current) {
        if (payload.requestId && current.requestId && payload.requestId !== current.requestId) {
          return state;
        }
        return {
          upgrades: {
            ...state.upgrades,
            [daemonId]: {
              ...current,
              phase: "running",
              progress: payload.message ?? current.progress,
              percent: payload.percent ?? current.percent,
            },
          },
        };
      }
      return {
        upgrades: {
          ...state.upgrades,
          [daemonId]: {
            daemonId,
            machineKey: daemonId,
            targetVersion: "",
            requestId: payload.requestId ?? "",
            phase: "running",
            progress: payload.message ?? null,
            percent: payload.percent ?? null,
            startedAt: Date.now(),
          },
        },
      };
    });
  },
  recordDone: (payload) => {
    const daemonId = payload.computer_id.trim();
    if (!daemonId) return;
    set((state) => {
      const current = state.upgrades[daemonId];
      if (current && payload.requestId && current.requestId && payload.requestId !== current.requestId) {
        return state;
      }
      if (payload.ok) {
        return {
          upgrades: {
            ...state.upgrades,
            [daemonId]: {
              ...(current ?? {
                daemonId,
                machineKey: daemonId,
                targetVersion: payload.newVersion ?? "",
                requestId: payload.requestId ?? "",
                startedAt: Date.now(),
              }),
              phase: "completed",
              error: null,
            },
          },
        };
      }
      return {
        upgrades: {
          ...state.upgrades,
          [daemonId]: {
            ...(current ?? {
              daemonId,
              machineKey: daemonId,
              targetVersion: "",
              requestId: payload.requestId ?? "",
              startedAt: Date.now(),
            }),
            phase: "failed",
            error: payload.error || null,
          },
        },
      };
    });
  },
  dismissUpgrade: (daemonId) => {
    const cleanId = daemonId.trim();
    set((state) => {
      if (!(cleanId in state.upgrades)) return state;
      const { [cleanId]: _, ...rest } = state.upgrades;
      return { upgrades: rest };
    });
  },
  clearCompleted: (daemonId) => {
    const cleanId = daemonId.trim();
    set((state) => {
      const current = state.upgrades[cleanId];
      if (!current || current.phase !== "completed") return state;
      const { [cleanId]: _, ...rest } = state.upgrades;
      return { upgrades: rest };
    });
  },
  reset: () => set({ upgrades: {} }),
}));

export function useComputerUpgrade(daemonId?: string | null): ComputerUpgradeRecord | undefined {
  return useComputerUpgradeStore((s) => (daemonId ? s.upgrades[daemonId.trim()] : undefined));
}

export function useAllComputerUpgrades(): Record<string, ComputerUpgradeRecord> {
  return useComputerUpgradeStore((s) => s.upgrades);
}
