import { describe, it, expect } from "vitest";
import type { AgentLifecyclePreflight } from "../types";
import {
  isTerminalAgentLifecycleStatus,
  agentLifecycleActionState,
  isImmediateExecution,
  resolveLifecycleDisabledReasonKey,
} from "./agent-lifecycle";

describe("isTerminalAgentLifecycleStatus", () => {
  it("terminates on succeeded/failed/scheduled; keeps polling only while running", () => {
    // scheduled is terminal for poll (#26): BE never auto-promotes it.
    expect(isTerminalAgentLifecycleStatus("succeeded")).toBe(true);
    expect(isTerminalAgentLifecycleStatus("failed")).toBe(true);
    expect(isTerminalAgentLifecycleStatus("scheduled")).toBe(true);
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

describe("resolveLifecycleDisabledReasonKey", () => {
  it("passes through a known reason", () => {
    expect(resolveLifecycleDisabledReasonKey("unsupported_runtime_capability")).toBe(
      "unsupported_runtime_capability",
    );
    expect(resolveLifecycleDisabledReasonKey("agent_active")).toBe("agent_active");
  });

  it("falls back to unavailable for an unknown or missing reason", () => {
    expect(resolveLifecycleDisabledReasonKey("some_future_reason_the_fe_has_no_copy_for")).toBe(
      "unavailable",
    );
    expect(resolveLifecycleDisabledReasonKey(null)).toBe("unavailable");
    expect(resolveLifecycleDisabledReasonKey(undefined)).toBe("unavailable");
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
