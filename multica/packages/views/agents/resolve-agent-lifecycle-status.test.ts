// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import { resolveAgentLifecycleStatus } from "./resolve-agent-lifecycle-status";

const AGENTS = {
  lifecycle_status: {
    starting: "Starting",
    disconnected: "Disconnected",
    offline: "Offline",
    stopped: "Stopped",
    crashed: "Crashed",
    blocked: "Quota blocked",
  },
} as const;

const t = ((selector: (r: typeof AGENTS) => string) =>
  selector(AGENTS)) as unknown as TFunction<"agents">;

describe("resolveAgentLifecycleStatus", () => {
  it("returns null for idle/working — those have their own presentation elsewhere", () => {
    expect(resolveAgentLifecycleStatus("idle", t)).toBeNull();
    expect(resolveAgentLifecycleStatus("working", t)).toBeNull();
  });

  // Iris, 08-02: shape = "can it recover on its own" — stopped is the only
  // terminal state (square); everything else that might come back reads dot.
  it("stopped is a filled square, grey — the one terminal state", () => {
    expect(resolveAgentLifecycleStatus("stopped", t)).toEqual({
      label: "Stopped",
      shape: "square",
      toneClass: "text-muted-foreground",
      dotClass: "bg-muted-foreground/40",
    });
  });

  it("disconnected is a dot, grey — may recover on its own", () => {
    expect(resolveAgentLifecycleStatus("disconnected", t)).toEqual({
      label: "Disconnected",
      shape: "dot",
      toneClass: "text-muted-foreground",
      dotClass: "bg-muted-foreground/40",
    });
  });

  it("crashed is a dot, grey — same shape as disconnected (both recoverable), text is the only differentiator", () => {
    expect(resolveAgentLifecycleStatus("crashed", t)).toEqual({
      label: "Crashed",
      shape: "dot",
      toneClass: "text-muted-foreground",
      dotClass: "bg-muted-foreground/40",
    });
  });

  it("blocked (provider quota) is a grey recoverable dot — not Online", () => {
    expect(resolveAgentLifecycleStatus("blocked", t)).toEqual({
      label: "Quota blocked",
      shape: "dot",
      toneClass: "text-muted-foreground",
      dotClass: "bg-muted-foreground/40",
    });
  });

  // Iris: starting is an active in-progress process (half of the healthy
  // Starting→Idle sequence), not a wait-for-recovery state — reads brand
  // like RUNTIME_HEALTH_STATE_VISUAL's updating/ready_to_apply, not grey.
  it("starting is a dot, brand-toned — an active process, not a wait state", () => {
    expect(resolveAgentLifecycleStatus("starting", t)).toEqual({
      label: "Starting",
      shape: "dot",
      toneClass: "text-brand",
      dotClass: "bg-brand",
    });
  });

  it("offline falls back to the generic grey dot", () => {
    expect(resolveAgentLifecycleStatus("offline", t)).toEqual({
      label: "Offline",
      shape: "dot",
      toneClass: "text-muted-foreground",
      dotClass: "bg-muted-foreground/40",
    });
  });

  // The hard requirement (Parker, 08-02): a missing/unrecognized value must
  // never be guess-classified into a more specific state — it reads as the
  // same generic "Offline" as an explicit "offline" value.
  it("null/undefined never guesses a specific state — falls back to Offline like an explicit 'offline'", () => {
    expect(resolveAgentLifecycleStatus(null, t)).toEqual(
      resolveAgentLifecycleStatus("offline", t),
    );
    expect(resolveAgentLifecycleStatus(undefined, t)).toEqual(
      resolveAgentLifecycleStatus("offline", t),
    );
  });
});
