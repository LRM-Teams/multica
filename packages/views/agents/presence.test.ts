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
    unstable: "Unstable",
    offline: "Offline",
    archived: "Archived",
  },
} as const;

const t = ((selector: (res: typeof LABELS) => string) =>
  selector(LABELS)) as TFunction<"agents">;

describe("toLiveAvailability", () => {
  it("folds online + unstable → online, offline → offline, archived → null", () => {
    expect(toLiveAvailability("online")).toBe("online");
    expect(toLiveAvailability("unstable")).toBe("online");
    expect(toLiveAvailability("offline")).toBe("offline");
    expect(toLiveAvailability("archived")).toBeNull();
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

  it("folds unstable → Online; offline → Offline; archived → null", () => {
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
    ).toBe("Online");
    expect(
      formatPresenceStatus(
        presence({ availability: "archived", workload: "idle" }),
        t,
      ),
    ).toBeNull();
  });
});

describe("presenceStatusVisual", () => {
  it("returns online config for online and unstable", () => {
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
    expect(presenceStatusVisual(unstable)).toBe(availabilityConfig.online);

    const offline = presence({ availability: "offline", workload: "working" });
    expect(presenceStatusToken(offline)).toEqual({
      kind: "availability",
      value: "offline",
    });
    expect(presenceStatusVisual(offline)).toBe(availabilityConfig.offline);
  });
});

describe("presenceStatusDotClass", () => {
  it("returns null while loading or archived", () => {
    expect(presenceStatusDotClass("loading")).toBeNull();
    expect(presenceStatusDotClass(null)).toBeNull();
    expect(
      presenceStatusDotClass(presence({ availability: "archived" })),
    ).toBeNull();
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

describe("matchesLiveAvailabilityFilter", () => {
  it("counts unstable under Online", () => {
    expect(matchesLiveAvailabilityFilter("unstable", "online")).toBe(true);
    expect(matchesLiveAvailabilityFilter("online", "online")).toBe(true);
    expect(matchesLiveAvailabilityFilter("offline", "online")).toBe(false);
    expect(matchesLiveAvailabilityFilter("offline", "offline")).toBe(true);
    expect(matchesLiveAvailabilityFilter("archived", "offline")).toBe(false);
  });
});
