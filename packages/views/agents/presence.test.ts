// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import type { AgentPresenceDetail } from "@multica/core/agents";
import {
  formatPresenceStatus,
  matchesLiveAvailabilityFilter,
  presenceStatusDotClass,
  presenceStatusToken,
  presenceStatusVisual,
  toLiveAvailability,
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
    offline: "Offline",
  },
} as const;

const t = ((selector: (res: typeof LABELS) => string) =>
  selector(LABELS)) as TFunction<"agents">;

describe("toLiveAvailability", () => {
  it("preserves online and offline", () => {
    expect(toLiveAvailability("online")).toBe("online");
    expect(toLiveAvailability("offline")).toBe("offline");
  });
});

describe("formatPresenceStatus (LRM-248 Online/Offline only)", () => {
  it("returns null while loading", () => {
    expect(formatPresenceStatus("loading", t)).toBeNull();
    expect(formatPresenceStatus(null, t)).toBeNull();
    expect(formatPresenceStatus(undefined, t)).toBeNull();
  });

  it("always shows Online while reachable — never Working / Idle / Queued", () => {
    expect(
      formatPresenceStatus(presence({ availability: "online", workload: "working" }), t),
    ).toBe("Online");
    expect(
      formatPresenceStatus(presence({ availability: "online", workload: "idle" }), t),
    ).toBe("Online");
    expect(
      formatPresenceStatus(presence({ availability: "online", workload: "queued" }), t),
    ).toBe("Online");
  });

  it("shows Offline while disconnected", () => {
    expect(
      formatPresenceStatus(
        presence({ availability: "offline", workload: "working" }),
        t,
      ),
    ).toBe("Offline");
  });
});

describe("presenceStatusVisual", () => {
  it("returns the config for the binary state", () => {
    const onlineWorking = presence({ availability: "online", workload: "working" });
    expect(presenceStatusToken(onlineWorking)).toEqual({
      kind: "availability",
      value: "online",
    });
    expect(presenceStatusVisual(onlineWorking)).toBe(availabilityConfig.online);

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

  it("maps online to green and offline to gray", () => {
    expect(
      presenceStatusDotClass(presence({ availability: "online", workload: "working" })),
    ).toBe("bg-success");
    expect(
      presenceStatusDotClass(presence({ availability: "offline", workload: "idle" })),
    ).toBe(availabilityConfig.offline.dotClass);
  });
});

describe("matchesLiveAvailabilityFilter", () => {
  it("matches the binary state", () => {
    expect(matchesLiveAvailabilityFilter("online", "online")).toBe(true);
    expect(matchesLiveAvailabilityFilter("offline", "online")).toBe(false);
    expect(matchesLiveAvailabilityFilter("offline", "offline")).toBe(true);
  });
});
