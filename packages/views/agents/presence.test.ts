// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import {
  formatPresenceStatus,
  matchesLiveAvailabilityFilter,
  presenceStatusDotClass,
  presenceStatusToken,
  presenceStatusVisual,
  toLiveAvailability,
  availabilityConfig,
} from "./presence";

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

  it("shows Online without accepting workload input", () => {
    expect(formatPresenceStatus("online", t)).toBe("Online");
  });

  it("shows Offline while disconnected", () => {
    expect(
      formatPresenceStatus("offline", t),
    ).toBe("Offline");
  });
});

describe("presenceStatusVisual", () => {
  it("returns the config for the binary state", () => {
    expect(presenceStatusToken("online")).toEqual({
      kind: "availability",
      value: "online",
    });
    expect(presenceStatusVisual("online")).toBe(availabilityConfig.online);

    expect(presenceStatusToken("offline")).toEqual({
      kind: "availability",
      value: "offline",
    });
    expect(presenceStatusVisual("offline")).toBe(availabilityConfig.offline);
  });
});

describe("presenceStatusDotClass", () => {
  it("returns null while loading", () => {
    expect(presenceStatusDotClass("loading")).toBeNull();
    expect(presenceStatusDotClass(null)).toBeNull();
  });

  it("maps online to green and offline to gray", () => {
    expect(
      presenceStatusDotClass("online"),
    ).toBe("bg-success");
    expect(
      presenceStatusDotClass("offline"),
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
