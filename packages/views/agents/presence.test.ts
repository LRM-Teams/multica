import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import type { AgentPresenceDetail } from "@multica/core/agents";
import {
  formatPresenceStatus,
  presenceStatusDotClass,
  presenceStatusToken,
  presenceStatusVisual,
  workloadConfig,
  availabilityConfig,
} from "./presence";

function presence(over: Partial<AgentPresenceDetail>): AgentPresenceDetail {
  return {
    availability: "online",
    workload: "idle",
    runningCount: 0,
    queuedCount: 0,
    capacity: 1,
    ...over,
  };
}

const LABELS = {
  workload: { working: "Working", queued: "Queued", idle: "Idle" },
  availability: {
    online: "Online",
    unstable: "Unstable",
    offline: "Offline",
    archived: "Archived",
  },
} as const;

// Minimal stand-in for useT("agents").t — only the selector form is used.
const t = ((selector: (res: typeof LABELS) => string) =>
  selector(LABELS)) as TFunction<"agents">;

describe("formatPresenceStatus", () => {
  it("returns null while loading", () => {
    expect(formatPresenceStatus("loading", t)).toBeNull();
    expect(formatPresenceStatus(null, t)).toBeNull();
    expect(formatPresenceStatus(undefined, t)).toBeNull();
  });

  it("localizes workload while online", () => {
    expect(
      formatPresenceStatus(presence({ availability: "online", workload: "working" }), t),
    ).toBe("Working");
    expect(
      formatPresenceStatus(presence({ availability: "online", workload: "idle" }), t),
    ).toBe("Idle");
  });

  it("localizes availability when not online (never a workload word)", () => {
    expect(
      formatPresenceStatus(
        presence({ availability: "offline", workload: "working" }),
        t,
      ),
    ).toBe("Offline");
    expect(
      formatPresenceStatus(
        presence({ availability: "unstable", workload: "idle" }),
        t,
      ),
    ).toBe("Unstable");
  });
});

describe("presenceStatusVisual", () => {
  it("returns the config for the same token as formatPresenceStatus", () => {
    const onlineWorking = presence({ availability: "online", workload: "working" });
    expect(presenceStatusToken(onlineWorking)).toEqual({
      kind: "workload",
      value: "working",
    });
    expect(presenceStatusVisual(onlineWorking)).toBe(workloadConfig.working);

    const offline = presence({ availability: "offline", workload: "working" });
    expect(presenceStatusToken(offline)).toEqual({
      kind: "availability",
      value: "offline",
    });
    expect(presenceStatusVisual(offline)).toBe(availabilityConfig.offline);
  });
});

describe("presenceStatusDotClass", () => {
  it("returns null while loading", () => {
    expect(presenceStatusDotClass("loading")).toBeNull();
    expect(presenceStatusDotClass(null)).toBeNull();
  });

  it("maps workload to semantic fills while online", () => {
    expect(
      presenceStatusDotClass(presence({ availability: "online", workload: "working" })),
    ).toBe("bg-brand");
    expect(
      presenceStatusDotClass(presence({ availability: "online", workload: "queued" })),
    ).toBe("bg-warning");
    expect(
      presenceStatusDotClass(presence({ availability: "online", workload: "idle" })),
    ).toBe("bg-muted-foreground/40");
  });

  it("reuses availabilityConfig.dotClass when offline", () => {
    const offline = presence({ availability: "offline", workload: "idle" });
    expect(presenceStatusDotClass(offline)).toBe(availabilityConfig.offline.dotClass);
  });
});
