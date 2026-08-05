// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { AgentRuntime } from "@multica/core/types";
import {
  buildRuntimeMachines,
  defaultDesktopSelectedMachineId,
  filterRuntimeMachines,
  filterRuntimesOnBoundComputer,
  headerRuntimeHealthBadge,
  machineDeviceName,
  runtimeComputerLabel,
  runtimeDisplayLabel,
  runtimeMachineCounts,
  runtimeMachineKey,
  runtimesShareMachine,
  splitRuntimeName,
} from "./runtime-machines";

const NOW = new Date("2026-05-17T12:00:00Z").getTime();

function makeRuntime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "runtime-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "Claude (dev-machine.local)",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "dev-machine.local · claude 1.0.0",
    metadata: { cli_version: "0.3.0" },
    current_version: "0.3.0",
    update_state: "idle",
    runtime_health: "ok",
    owner_id: "user-1",
    last_seen_at: new Date(NOW - 10_000).toISOString(),
    created_at: "2026-05-17T11:00:00Z",
    updated_at: "2026-05-17T11:00:00Z",
    ...overrides,
  };
}

describe("runtime machine grouping", () => {
  it("prefers display_name over hostname for machine title", () => {
    const machines = buildRuntimeMachines(
      [
        makeRuntime({
          display_name: "Andong's MacBook Pro",
          name: "Claude (dev.local)",
        }),
      ],
      { now: NOW, localDaemonId: "daemon-1" },
    );

    expect(machines[0]?.title).toBe("Andong's MacBook Pro");
  });

  it("groups multiple provider runtimes by daemon id", () => {
    const machines = buildRuntimeMachines(
      [
        makeRuntime({ id: "rt-claude", provider: "claude", name: "Claude (dev.local)" }),
        makeRuntime({ id: "rt-codex", provider: "codex", name: "Codex (dev.local)" }),
      ],
      { now: NOW, localDaemonId: "daemon-1" },
    );

    expect(machines).toHaveLength(1);
    expect(machines[0]).toMatchObject({
      id: "local:daemon-1",
      title: "dev.local",
      section: "local",
      isCurrent: true,
      onlineCount: 2,
      issueCount: 0,
      providerNames: ["claude", "codex"],
    });
  });

  it("counts machines with any offline runtime as issues", () => {
    const machines = buildRuntimeMachines(
      [
        makeRuntime({ id: "rt-online", provider: "claude" }),
        makeRuntime({
          id: "rt-offline",
          provider: "codex",
          status: "offline",
          last_seen_at: new Date(NOW - 10 * 60_000).toISOString(),
        }),
      ],
      { now: NOW },
    );

    expect(runtimeMachineCounts(machines)).toEqual({
      all: 1,
      online: 1,
      issues: 1,
    });
    expect(filterRuntimeMachines(machines, "", "issues")).toHaveLength(1);
  });

  it("cliVersion ignores stale offline runtimes (Frank 2026-08-03)", () => {
    // One-off code-agent crash: Grok exited on 0.3.94 while Pi/Cursor
    // moved to 0.3.95 with the daemon. The strict every-runtime-agrees
    // read nulled the row and the machine's daemon version vanished.
    const machines = buildRuntimeMachines(
      [
        makeRuntime({
          id: "rt-pi",
          provider: "pi",
          current_version: "0.3.95",
          runtime_health: "update_available",
        }),
        makeRuntime({
          id: "rt-cursor",
          provider: "cursor",
          current_version: "0.3.95",
          runtime_health: "update_available",
        }),
        makeRuntime({
          id: "rt-grok",
          provider: "grok",
          current_version: "0.3.94",
          runtime_health: "offline",
          status: "offline",
          last_seen_at: new Date(NOW - 24 * 60 * 60_000).toISOString(),
        }),
      ],
      { now: NOW },
    );

    expect(machines[0]?.cliVersion).toBe("0.3.95");
  });

  it("Basics Daemon version falls back to the freshest sighting when all runtimes are offline", () => {
    const machines = buildRuntimeMachines(
      [
        makeRuntime({
          id: "rt-old",
          provider: "claude",
          current_version: "0.3.90",
          runtime_health: "offline",
          status: "offline",
          last_seen_at: new Date(NOW - 7 * 24 * 60 * 60_000).toISOString(),
        }),
        makeRuntime({
          id: "rt-recent",
          provider: "codex",
          current_version: "0.3.95",
          runtime_health: "offline",
          status: "offline",
          last_seen_at: new Date(NOW - 60_000).toISOString(),
        }),
      ],
      { now: NOW },
    );

    expect(machines[0]?.cliVersion).toBe("0.3.95");
  });

  it("does not surface agent CLI version branding as the machine subtitle", () => {
    // Reproduces the bug where every machine row's subtitle read
    // "Claude Code …" because compactDeviceInfo flipped the parenthetical
    // of the version string "2.1.5 (Claude Code)" into the description.
    const machines = buildRuntimeMachines(
      [
        makeRuntime({
          id: "rt-claude",
          provider: "claude",
          name: "Claude (dev.local)",
          device_info: "dev.local · 2.1.5 (Claude Code)",
        }),
        makeRuntime({
          id: "rt-codex",
          provider: "codex",
          name: "Codex (dev.local)",
          device_info: "dev.local · codex-cli 0.118.0",
        }),
      ],
      { now: NOW, localDaemonId: "daemon-1" },
    );

    expect(machines).toHaveLength(1);
    const subtitle = machines[0]?.subtitle ?? "";
    expect(subtitle.toLowerCase()).not.toContain("claude code");
    expect(subtitle.toLowerCase()).not.toContain("codex-cli");
    // Falls back to the daemon-id descriptor — at minimum it must not be
    // the runtime CLI's marketing string.
    expect(subtitle).toMatch(/^daemon /);
  });

  it("Basics OS uses structured device_name; never parses device_info glue", () => {
    // Frank 2026-08-01: OS showed "ubuntu · codex-cli 0.146.0". Alice #1723
    // exposes device_name; FE must not invent it by splitting device_info.
    expect(
      machineDeviceName([
        makeRuntime({
          device_info: "ubuntu · codex-cli 0.146.0",
          device_name: "ubuntu",
        }),
      ]),
    ).toBe("ubuntu");

    expect(
      machineDeviceName([
        makeRuntime({
          device_info: "ubuntu · codex-cli 0.146.0",
        }),
      ]),
    ).toBeNull();

    const withName = buildRuntimeMachines(
      [
        makeRuntime({
          device_info: "ubuntu · codex-cli 0.146.0",
          device_name: "ubuntu",
        }),
      ],
      { now: NOW },
    );
    expect(withName[0]?.deviceName).toBe("ubuntu");
    expect(withName[0]?.deviceName?.toLowerCase()).not.toContain("codex");

    const missing = buildRuntimeMachines(
      [makeRuntime({ device_info: "ubuntu · codex-cli 0.146.0" })],
      { now: NOW },
    );
    expect(missing[0]?.deviceName).toBeNull();
  });

  it("synthesizes a placeholder local machine when ensureLocalMachine is set and no runtime matches", () => {
    // Reproduces the "Start button disappears after stopping the daemon"
    // bug: the daemon is stopped (localDaemonId is null) and the server
    // has already GC'd the local runtime, so no machine ends up flagged
    // isCurrent. Without synthesis the local row vanishes and the
    // Start button has nowhere to render.
    const machines = buildRuntimeMachines(
      [
        makeRuntime({
          id: "rt-remote",
          daemon_id: "daemon-remote",
          name: "Claude (remote.box)",
          device_info: "remote.box",
        }),
      ],
      {
        now: NOW,
        localDaemonId: null,
        localMachineName: "My Laptop",
        ensureLocalMachine: true,
      },
    );

    expect(machines).toHaveLength(2);
    const local = machines.find((m) => m.isCurrent);
    expect(local).toMatchObject({
      title: "My Laptop",
      section: "local",
      isCurrent: true,
      runtimes: [],
    });
  });

  it("does not synthesize a placeholder when a real local runtime exists", () => {
    const machines = buildRuntimeMachines(
      [makeRuntime({ daemon_id: "daemon-1" })],
      {
        now: NOW,
        localDaemonId: "daemon-1",
        ensureLocalMachine: true,
      },
    );

    expect(machines).toHaveLength(1);
    expect(machines[0]).toMatchObject({
      isCurrent: true,
      runtimes: expect.arrayContaining([
        expect.objectContaining({ daemon_id: "daemon-1" }),
      ]),
    });
  });

  it("treats a runtime with the local device name as the current machine", () => {
    const machines = buildRuntimeMachines(
      [
        makeRuntime({
          daemon_id: "legacy-hostname",
          name: "Claude (My Laptop)",
          device_info: "My Laptop · claude 1.0.0",
        }),
      ],
      {
        now: NOW,
        localDaemonId: "daemon-uuid",
        localMachineName: "my laptop",
        currentUserId: "user-1",
        ensureLocalMachine: true,
      },
    );

    expect(machines).toHaveLength(1);
    expect(machines[0]).toMatchObject({
      title: "my laptop",
      section: "local",
      isCurrent: true,
      daemonId: "legacy-hostname",
    });
  });

  it("does not treat a cloud runtime with the local device name as current", () => {
    const machines = buildRuntimeMachines(
      [
        makeRuntime({
          id: "cloud-1",
          daemon_id: null,
          runtime_mode: "cloud",
          provider: "codex",
          name: "Codex (My Laptop)",
          device_info: "My Laptop · codex 1.0.0",
        }),
      ],
      {
        now: NOW,
        localDaemonId: "daemon-uuid",
        localMachineName: "my laptop",
        currentUserId: "user-1",
        ensureLocalMachine: true,
      },
    );

    expect(machines).toHaveLength(2);
    const cloud = machines.find((m) => m.id === "cloud:device:My Laptop");
    expect(cloud).toMatchObject({
      title: "My Laptop",
      section: "cloud",
      isCurrent: false,
    });
    const local = machines.find((m) => m.isCurrent);
    expect(local).toMatchObject({
      title: "my laptop",
      section: "local",
      runtimes: [],
    });
  });

  it("consolidates an out-of-band local daemon (WSL2) by host name and suppresses the placeholder", () => {
    // The desktop doesn't manage this daemon (it runs in WSL2), so
    // localDaemonId never matches. localMachineName falls back to the OS
    // hostname, and the runtime is owned by the viewing user — so it must
    // consolidate into the local section, and no empty placeholder appears.
    const machines = buildRuntimeMachines(
      [
        makeRuntime({
          id: "rt-wsl2",
          daemon_id: "wsl2-daemon-uuid",
          name: "Claude (KIKI-PC)",
          device_info: "KIKI-PC · claude 1.0.0",
          owner_id: "user-1",
        }),
      ],
      {
        now: NOW,
        localDaemonId: "desktop-daemon-uuid",
        localMachineName: "KIKI-PC",
        currentUserId: "user-1",
        ensureLocalMachine: true,
      },
    );

    expect(machines).toHaveLength(1);
    expect(machines[0]).toMatchObject({
      title: "KIKI-PC",
      section: "local",
      isCurrent: true,
      daemonId: "wsl2-daemon-uuid",
    });
  });

  it("does not claim another user's identically-named machine as current", () => {
    // Same host name, but the runtime belongs to a different user. Device-name
    // consolidation must NOT fire, so it stays remote and the placeholder for
    // this machine is still synthesized.
    const machines = buildRuntimeMachines(
      [
        makeRuntime({
          id: "rt-other",
          daemon_id: "other-daemon-uuid",
          name: "Claude (KIKI-PC)",
          device_info: "KIKI-PC · claude 1.0.0",
          owner_id: "user-2",
        }),
      ],
      {
        now: NOW,
        localDaemonId: "desktop-daemon-uuid",
        localMachineName: "KIKI-PC",
        currentUserId: "user-1",
        ensureLocalMachine: true,
      },
    );

    expect(machines).toHaveLength(2);
    const other = machines.find((m) => m.id === "local:other-daemon-uuid");
    expect(other).toMatchObject({ section: "remote", isCurrent: false });
    const local = machines.find((m) => m.isCurrent);
    expect(local).toMatchObject({ section: "local", runtimes: [] });
  });

  it("does not treat another user's matching daemon ID as current or the fresh default", () => {
    const machines = buildRuntimeMachines(
      [
        makeRuntime({
          id: "rt-foreign-local-daemon",
          daemon_id: "desktop-daemon-uuid",
          owner_id: "user-2",
        }),
        makeRuntime({
          id: "rt-mine",
          daemon_id: "mine-daemon-uuid",
          owner_id: "user-1",
        }),
      ],
      {
        now: NOW,
        localDaemonId: "desktop-daemon-uuid",
        currentUserId: "user-1",
      },
    );

    const foreign = machines.find((m) => m.daemonId === "desktop-daemon-uuid");
    expect(foreign).toMatchObject({ section: "remote", isCurrent: false });
    expect(defaultDesktopSelectedMachineId(machines, "user-1")).toBe(
      "local:mine-daemon-uuid",
    );
  });

  it("keeps cloud runtimes as cloud workers when they have no daemon", () => {
    const machines = buildRuntimeMachines(
      [
        makeRuntime({
          id: "cloud-1",
          daemon_id: null,
          runtime_mode: "cloud",
          provider: "codex",
          name: "Codex cloud",
          device_info: "",
        }),
      ],
      { now: NOW },
    );

    expect(machines[0]).toMatchObject({
      id: "cloud:runtime:cloud-1",
      title: "Codex cloud",
      subtitle: "Cloud worker",
      section: "cloud",
    });
  });
});

describe("machine connectivity from daemon heartbeat (task #58 / B-(i))", () => {
  it("reads Online from computer_connected even when every runtime is offline", () => {
    const daemonSeen = new Date(NOW - 5_000).toISOString();
    const machines = buildRuntimeMachines(
      [
        makeRuntime({
          status: "offline",
          last_seen_at: new Date(NOW - 10 * 60_000).toISOString(),
          computer_connected: true,
          daemon_last_seen_at: daemonSeen,
        }),
      ],
      { now: NOW },
    );

    expect(machines[0]?.health).toBe("online");
    expect(machines[0]?.lastSeenAt).toBe(daemonSeen);
    expect(machines[0]?.onlineCount).toBe(0);
  });

  it("reads Offline from computer_connected even when a runtime last_seen is fresh", () => {
    const machines = buildRuntimeMachines(
      [
        makeRuntime({
          status: "online",
          last_seen_at: new Date(NOW - 5_000).toISOString(),
          computer_connected: false,
          daemon_last_seen_at: new Date(NOW - 10 * 60_000).toISOString(),
        }),
      ],
      { now: NOW },
    );

    expect(machines[0]?.health).toBe("offline");
    expect(machines[0]?.lastSeenAt).toBe(
      new Date(NOW - 10 * 60_000).toISOString(),
    );
  });

  it("falls back to runtime aggregation when computer_connected is absent", () => {
    const machines = buildRuntimeMachines(
      [
        makeRuntime({
          status: "offline",
          last_seen_at: new Date(NOW - 10 * 60_000).toISOString(),
        }),
      ],
      { now: NOW },
    );

    expect(machines[0]?.health).toBe("offline");
    expect(machines[0]?.lastSeenAt).toBe(
      new Date(NOW - 10 * 60_000).toISOString(),
    );
  });
});

describe("runtimeComputerLabel (Frank 2026-08-02)", () => {
  it("never returns the Provider (host) runtime name when a hostname exists", () => {
    expect(
      runtimeComputerLabel(
        makeRuntime({
          display_name: "",
          name: "Cursor (s144)",
        }),
      ),
    ).toBe("s144");
  });

  it("prefers display_name over hostname", () => {
    expect(
      runtimeComputerLabel(
        makeRuntime({
          display_name: "Andong's Mac",
          name: "Cursor (s144)",
        }),
      ),
    ).toBe("Andong's Mac");
  });
});

describe("runtimesShareMachine / filterRuntimesOnBoundComputer (LRM-1365)", () => {
  it("shares only when daemon_id matches (ignores hostname collisions)", () => {
    const onUbuntu = makeRuntime({
      id: "rt-ubuntu-kiro",
      daemon_id: "daemon-ubuntu",
      name: "Kiro (ubuntu)",
      provider: "kiro",
    });
    const onKiroBox = makeRuntime({
      id: "rt-kiro-codex",
      daemon_id: "daemon-kiro",
      name: "Codex (kiro)",
      provider: "codex",
      // Same free-text hostname token must NOT merge machines.
      device_info: "ubuntu · fake",
    });
    expect(runtimesShareMachine(onUbuntu, onKiroBox)).toBe(false);
    expect(
      filterRuntimesOnBoundComputer(onUbuntu, [onUbuntu, onKiroBox]).map(
        (r) => r.id,
      ),
    ).toEqual(["rt-ubuntu-kiro"]);
  });

  it("includes same-daemon peers and always keeps the bound runtime", () => {
    const cursor = makeRuntime({
      id: "rt-s144-cursor",
      daemon_id: "daemon-s144",
      name: "Cursor (s144)",
      provider: "cursor",
    });
    const pi = makeRuntime({
      id: "rt-s144-pi",
      daemon_id: "daemon-s144",
      name: "Pi (s144)",
      provider: "pi",
    });
    const other = makeRuntime({
      id: "rt-other",
      daemon_id: "daemon-other",
      name: "Cursor (other)",
      provider: "cursor",
    });
    expect(
      filterRuntimesOnBoundComputer(cursor, [cursor, pi, other]).map((r) => r.id),
    ).toEqual(["rt-s144-cursor", "rt-s144-pi"]);
  });

  it("does not group by hostname when daemon_id is missing (BE parity)", () => {
    const a = makeRuntime({
      id: "rt-a",
      daemon_id: null,
      name: "Cursor (ubuntu)",
      provider: "cursor",
    });
    const b = makeRuntime({
      id: "rt-b",
      daemon_id: null,
      name: "Pi (ubuntu)",
      provider: "pi",
    });
    // Cosmetic runtimeMachineKey would merge these; auth boundary must not.
    expect(runtimeMachineKey(a)).toBe(runtimeMachineKey(b));
    expect(runtimesShareMachine(a, b)).toBe(false);
    expect(filterRuntimesOnBoundComputer(a, [a, b]).map((r) => r.id)).toEqual([
      "rt-a",
    ]);
  });
});

describe("machine health presentation (#687)", () => {
  it("surfaces a staged runtime's ready_to_apply on the machine header (agrees with the row)", () => {
    // Backend collapses ready_to_apply into runtime_health=update_available; the
    // machine aggregate must still read ready_to_apply so the header does not
    // contradict the row-level HealthCell.
    const machines = buildRuntimeMachines(
      [
        makeRuntime({
          id: "rt-staged",
          runtime_health: "update_available",
          update_state: "ready_to_apply",
        }),
      ],
      { now: NOW, localDaemonId: "daemon-1" },
    );

    expect(machines).toHaveLength(1);
    expect(machines[0]?.runtimeHealth).toBe("ready_to_apply");
  });

  it("keeps offline dominating a sibling staged runtime on the header", () => {
    const machines = buildRuntimeMachines(
      [
        makeRuntime({
          id: "rt-staged",
          runtime_health: "update_available",
          update_state: "ready_to_apply",
        }),
        makeRuntime({
          id: "rt-off",
          runtime_health: "offline",
          status: "offline",
          last_seen_at: new Date(NOW - 10 * 60_000).toISOString(),
        }),
      ],
      { now: NOW, localDaemonId: "daemon-1" },
    );

    expect(machines[0]?.runtimeHealth).toBe("offline");
  });
});

describe("machine cliVersion (header daemon chip)", () => {
  it("keeps a shared version when every runtime reports the same current_version", () => {
    const machines = buildRuntimeMachines(
      [
        makeRuntime({ id: "rt-a", provider: "claude", current_version: "0.3.94" }),
        makeRuntime({ id: "rt-b", provider: "codex", current_version: "0.3.94" }),
      ],
      { now: NOW, localDaemonId: "daemon-1" },
    );
    expect(machines[0]?.cliVersion).toBe("0.3.94");
  });

  it("treats v-prefix variants as the same version", () => {
    const machines = buildRuntimeMachines(
      [
        makeRuntime({ id: "rt-a", current_version: "0.3.94" }),
        makeRuntime({ id: "rt-b", provider: "codex", current_version: "v0.3.94" }),
      ],
      { now: NOW, localDaemonId: "daemon-1" },
    );
    expect(machines[0]?.cliVersion).toMatch(/0\.3\.94$/);
  });

  it("still shows a version when runtimes disagree (prefer most recently seen)", () => {
    const machines = buildRuntimeMachines(
      [
        makeRuntime({
          id: "rt-old",
          current_version: "0.3.90",
          last_seen_at: new Date(NOW - 60_000).toISOString(),
        }),
        makeRuntime({
          id: "rt-new",
          provider: "codex",
          current_version: "0.3.94",
          last_seen_at: new Date(NOW - 5_000).toISOString(),
        }),
      ],
      { now: NOW, localDaemonId: "daemon-1" },
    );
    expect(machines[0]?.cliVersion).toBe("0.3.94");
  });

  it("shows the reported version when only some runtimes have current_version", () => {
    const machines = buildRuntimeMachines(
      [
        makeRuntime({ id: "rt-a", current_version: "0.3.94" }),
        makeRuntime({
          id: "rt-b",
          provider: "codex",
          current_version: null,
          metadata: {},
        }),
      ],
      { now: NOW, localDaemonId: "daemon-1" },
    );
    expect(machines[0]?.cliVersion).toBe("0.3.94");
  });
});

describe("splitRuntimeName", () => {
  it("separates daemon host suffix from provider name", () => {
    expect(splitRuntimeName("Claude (build-server-01)")).toEqual({
      base: "Claude",
      hostname: "build-server-01",
    });
  });

  it("falls back to the full name when no host suffix exists", () => {
    expect(splitRuntimeName("Codex cloud")).toEqual({
      base: "Codex cloud",
      hostname: null,
    });
  });
});

describe("headerRuntimeHealthBadge (LRM-624 / Plan A)", () => {
  // The title row shows a single connectivity dot+label. The secondary
  // runtimeHealth badge is offline-only now — update-lifecycle states are
  // never shown here, since the page's own upgrade control (task #1680)
  // already states that fact once.
  it("never badges ok / null", () => {
    expect(headerRuntimeHealthBadge(null, "online")).toBeNull();
    expect(headerRuntimeHealthBadge("ok", "online")).toBeNull();
    expect(headerRuntimeHealthBadge("ok", "offline")).toBeNull();
  });

  it("never badges update-lifecycle states, regardless of connectivity — the upgrade control already says this once", () => {
    for (const connectivity of [
      "online",
      "recently_lost",
      "offline",
      "about_to_gc",
    ] as const) {
      expect(
        headerRuntimeHealthBadge("update_available", connectivity),
      ).toBeNull();
      expect(
        headerRuntimeHealthBadge("ready_to_apply", connectivity),
      ).toBeNull();
      expect(headerRuntimeHealthBadge("updating", connectivity)).toBeNull();
      expect(headerRuntimeHealthBadge("failed", connectivity)).toBeNull();
    }
  });

  it("suppresses a duplicate offline badge when connectivity is already offline-ish", () => {
    expect(headerRuntimeHealthBadge("offline", "offline")).toBeNull();
    expect(headerRuntimeHealthBadge("offline", "recently_lost")).toBeNull();
    expect(headerRuntimeHealthBadge("offline", "about_to_gc")).toBeNull();
  });

  it("keeps the offline badge when connectivity is online (not a duplicate)", () => {
    // Machine reachable but its runtimes individually report offline — a
    // real signal, not a duplicate of the connectivity dot.
    expect(headerRuntimeHealthBadge("offline", "online")).toBe("offline");
  });
});

describe("runtimeDisplayLabel", () => {
  // Barry 08-01: runtime pickers showed the daemon-reported raw hostname
  // unconditionally — renaming a machine on the detail page had no effect
  // in the picker dropdowns. This is the shared fix both pickers now use.
  it("prefers display_name over the raw name", () => {
    expect(
      runtimeDisplayLabel(
        makeRuntime({ display_name: "Andong's MacBook Pro", name: "Claude (dev.local)" }),
      ),
    ).toBe("Andong's MacBook Pro");
  });

  it("falls back to the raw name when display_name is unset", () => {
    expect(
      runtimeDisplayLabel(makeRuntime({ display_name: undefined, name: "Claude (dev.local)" })),
    ).toBe("Claude (dev.local)");
  });

  it("falls back to the raw name when display_name is blank/whitespace", () => {
    expect(
      runtimeDisplayLabel(makeRuntime({ display_name: "   ", name: "Claude (dev.local)" })),
    ).toBe("Claude (dev.local)");
  });
});
