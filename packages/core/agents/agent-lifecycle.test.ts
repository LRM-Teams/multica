import { describe, it, expect } from "vitest";
import type { AgentLifecyclePreflight } from "../types";
import {
  isTerminalAgentLifecycleStatus,
  agentLifecycleActionState,
  isImmediateExecution,
} from "./agent-lifecycle";

describe("isTerminalAgentLifecycleStatus", () => {
  it("terminates on succeeded/failed, keeps polling otherwise", () => {
    expect(isTerminalAgentLifecycleStatus("succeeded")).toBe(true);
    expect(isTerminalAgentLifecycleStatus("failed")).toBe(true);
    expect(isTerminalAgentLifecycleStatus("scheduled")).toBe(false);
    expect(isTerminalAgentLifecycleStatus("running")).toBe(false);
    expect(isTerminalAgentLifecycleStatus(null)).toBe(false);
    expect(isTerminalAgentLifecycleStatus(undefined)).toBe(false);
  });
});

describe("agentLifecycleActionState", () => {
  const preflight: AgentLifecyclePreflight = {
    actions: {
      restart: { supported: true, execution_mode: "immediate" },
      reset_session_restart: { supported: true, execution_mode: "after_current_run" },
      full_reset_restart: {
        supported: false,
        disabled_reason: "agent_active",
        execution_mode: "immediate",
      },
    },
  };

  it("returns the per-action state", () => {
    expect(agentLifecycleActionState(preflight, "restart").supported).toBe(true);
    expect(agentLifecycleActionState(preflight, "full_reset_restart")).toMatchObject({
      supported: false,
      disabled_reason: "agent_active",
    });
  });

  it("fails closed (disabled) when preflight is missing or omits the action", () => {
    expect(agentLifecycleActionState(undefined, "restart")).toMatchObject({
      supported: false,
      disabled_reason: "unavailable",
    });
    expect(
      agentLifecycleActionState({ actions: {} as never }, "restart").supported,
    ).toBe(false);
  });
});

describe("isImmediateExecution", () => {
  it("distinguishes immediate from after_current_run", () => {
    expect(isImmediateExecution({ supported: true, execution_mode: "immediate" })).toBe(true);
    expect(
      isImmediateExecution({ supported: true, execution_mode: "after_current_run" }),
    ).toBe(false);
  });
});
