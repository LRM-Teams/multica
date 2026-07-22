import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import type { AgentPresenceDetail } from "@multica/core/agents";
import {
  formatPresenceStatus,
  presenceStatusDotClass,
  presenceStatusToken,
  presenceStatusVisual,
  toLivePresence,
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

const t = ((selector: (res: typeof LABELS) => string) =>
  selector(LABELS)) as TFunction<"agents">;

describe("toLivePresence (LRM-248)", () => {
  it("folds unstable into online", () => {
    expect(toLivePresence("online")).toBe("online");
    expect(toLivePresence("unstable")).toBe("online");
    expect(toLivePresence("offline")).toBe("offline");
    expect(toLivePresence("archived")).toBe("archived");
  });
});

describe("formatPresenceStatus (LRM-248)", () => {
  it("returns null while loading", () => {
    expect(formatPresenceStatus("loading", t)).toBeNull();
    expect(formatPresenceStatus(null, t)).toBeNull();
    expect(formatPresenceStatus(undefined, t)).toBeNull();
  });

  it("never surfaces Working / Queued / Idle / Unstable as live words", () => {
    expect(
      formatPresenceStatus(presence({ availability: "online", workload: "working" }), t),
    ).toBe("Online");
    expect(
      formatPresenceStatus(presence({ availability: "online", workload: "queued" }), t),
    ).toBe("Online");
    expect(
      formatPresenceStatus(presence({ availability: "online", workload: "idle" }), t),
    ).toBe("Online");
    expect(
      formatPresenceStatus(presence({ availability: "unstable", workload: "idle" }), t),
    ).toBe("Online");
  });

  it("shows Offline when offline even with residual workload", () => {
    expect(
      formatPresenceStatus(
        presence({ availability: "offline", workload: "working" }),
        t,
      ),
    ).toBe("Offline");
  });
});

describe("presenceStatusVisual", () => {
  it("always returns availability visuals for live presence", () => {
    const onlineWorking = presence({ availability: "online", workload: "working" });
    expect(presenceStatusToken(onlineWorking)).toEqual({
      kind: "availability",
      value: "online",
    });
    expect(presenceStatusVisual(onlineWorking)).toBe(availabilityConfig.online);

    const unstable = presence({ availability: "unstable", workload: "idle" });
    expect(presenceStatusToken(unstable)).toEqual({
      kind: "availability",
      value: "online",
    });

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

  it("maps online + unstable to green; offline to gray", () => {
    expect(
      presenceStatusDotClass(presence({ availability: "online", workload: "working" })),
    ).toBe("bg-success");
    expect(
      presenceStatusDotClass(presence({ availability: "unstable", workload: "idle" })),
    ).toBe("bg-success");
    expect(
      presenceStatusDotClass(presence({ availability: "offline", workload: "idle" })),
    ).toBe(availabilityConfig.offline.dotClass);
  });
});
