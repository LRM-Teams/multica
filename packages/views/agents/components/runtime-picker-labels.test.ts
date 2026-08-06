// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { AgentRuntime } from "@multica/core/types";
import {
  runtimePickerBrandLabel,
  runtimePickerHostSubtitle,
} from "./runtime-picker-labels";

function makeRuntime(over: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    name: "Grok (ubuntu)",
    provider: "grok",
    status: "online",
    device_info: "ubuntu",
    daemon_id: "d-1",
    runtime_mode: "local",
    owner_id: "user-me",
    last_seen_at: new Date().toISOString(),
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...over,
  } as AgentRuntime;
}

describe("runtimePickerBrandLabel", () => {
  it("maps grok → Grok Build", () => {
    expect(runtimePickerBrandLabel(makeRuntime({ provider: "grok" }))).toBe(
      "Grok Build",
    );
  });

  it("maps cursor → Cursor", () => {
    expect(runtimePickerBrandLabel(makeRuntime({ provider: "cursor" }))).toBe(
      "Cursor",
    );
  });
});

describe("runtimePickerHostSubtitle", () => {
  it("hides host when provider is unique in the list", () => {
    expect(
      runtimePickerHostSubtitle(makeRuntime(), [
        makeRuntime(),
        makeRuntime({ id: "rt-2", provider: "cursor", name: "Cursor (ubuntu)" }),
      ]),
    ).toBeNull();
  });

  it("shows computer label when two of the same provider appear", () => {
    expect(
      runtimePickerHostSubtitle(makeRuntime({ display_name: "build-01" }), [
        makeRuntime({ id: "rt-a", display_name: "build-01" }),
        makeRuntime({
          id: "rt-b",
          provider: "grok",
          name: "Grok (other)",
          display_name: "other",
          daemon_id: "d-2",
        }),
      ]),
    ).toBe("build-01");
  });
});
