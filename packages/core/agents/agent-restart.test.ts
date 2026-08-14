import { describe, it, expect } from "vitest";
import type { AgentRestartPreflight } from "../types";
import {
  agentRestartModeState,
  resolveRestartDisabledReasonKey,
} from "./agent-restart";

describe("agentRestartModeState", () => {
  const preflight: AgentRestartPreflight = {
    actions: {
      restart: { supported: true },
      session: { supported: true },
      full: {
        supported: false,
        disabled_reason: "agent_active",
      },
    },
  };

  it("returns the per-action state", () => {
    expect(agentRestartModeState(preflight, "restart").supported).toBe(true);
    expect(agentRestartModeState(preflight, "full")).toMatchObject({
      supported: false,
      disabled_reason: "agent_active",
    });
  });

  it("fails closed (disabled) when preflight is missing or omits the action", () => {
    expect(agentRestartModeState(undefined, "restart")).toMatchObject({
      supported: false,
      disabled_reason: "unavailable",
    });
    expect(
      agentRestartModeState({ actions: {} as never }, "restart").supported,
    ).toBe(false);
  });
});

describe("resolveRestartDisabledReasonKey", () => {
  it("passes through a known reason", () => {
    expect(resolveRestartDisabledReasonKey("unsupported_runtime_capability")).toBe(
      "unsupported_runtime_capability",
    );
    expect(resolveRestartDisabledReasonKey("agent_active")).toBe("agent_active");
  });

  it("falls back to unavailable for an unknown or missing reason", () => {
    expect(resolveRestartDisabledReasonKey("some_future_reason_the_fe_has_no_copy_for")).toBe(
      "unavailable",
    );
    expect(resolveRestartDisabledReasonKey(null)).toBe("unavailable");
    expect(resolveRestartDisabledReasonKey(undefined)).toBe("unavailable");
  });
});
