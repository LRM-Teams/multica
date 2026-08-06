// @vitest-environment jsdom

import { describe, it, expect, vi } from "vitest";
import { render, screen, within, fireEvent } from "@testing-library/react";
import type { AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import type { RuntimeMachine } from "./runtime-machines";

const TEST_RESOURCES = {
  en: { common: enCommon, runtimes: enRuntimes },
};

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: (_type: string, id: string) =>
      id === "user-mine" ? "Me" : "Teammate",
  }),
}));

vi.mock("./machine-name-editor", () => ({
  MachineNameEditor: ({ machine }: { machine: RuntimeMachine }) => (
    <span>{machine.title}</span>
  ),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => (
    <span data-testid="owner-avatar">{actorId}</span>
  ),
}));

import { MachineListView } from "./runtimes-page";
import { isMineMachine } from "./runtime-machines";

function makeMachine(
  id: string,
  title: string,
  ownerId: string,
): RuntimeMachine {
  const runtime: AgentRuntime = {
    id: `${id}-rt`,
    workspace_id: "ws-1",
    daemon_id: `${id}-daemon`,
    name: title,
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: title,
    metadata: {},
    current_version: "1.0.0",
    update_state: "idle",
    runtime_health: "ok",
    owner_id: ownerId,
    visibility: ownerId === "user-mine" ? "private" : "public",
    last_seen_at: "2026-08-01T00:00:00Z",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
  return {
    id,
    daemonId: `${id}-daemon`,
    title,
    subtitle: null,
    deviceInfo: null,
    deviceName: null,
    cliVersion: "1.0.0",
    mode: "local",
    section: "local",
    isCurrent: false,
    health: "online",
    runtimeHealth: "ok",
    updateError: null,
    updateTargetVersion: null,
    runtimes: [runtime],
    onlineCount: 1,
    issueCount: 0,
    runningCount: 0,
    queuedCount: 0,
    providerNames: ["claude"],
    lastSeenAt: "2026-08-01T00:00:00Z",
  };
}

describe("isMineMachine", () => {
  it("is true when the current user owns any runtime on the machine", () => {
    expect(isMineMachine(makeMachine("m1", "My box", "user-mine"), "user-mine")).toBe(true);
  });

  it("is false for another member's machine", () => {
    expect(isMineMachine(makeMachine("m1", "Their box", "user-other"), "user-mine")).toBe(false);
  });

  it("is false when there's no current user", () => {
    expect(isMineMachine(makeMachine("m1", "Box", "user-mine"), null)).toBe(false);
  });
});

function renderList(machines: RuntimeMachine[], currentUserId: string | null) {
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <MachineListView
        machines={machines}
        agents={[]}
        now={Date.parse("2026-08-01T00:00:05Z")}
        wsId="ws-1"
        currentUserId={currentUserId}
        layout="sidebar"
        selectedMachineId={null}
        headerActions={null}
        onSelect={() => {}}
      />
    </I18nProvider>,
  );
}

describe("MachineListView — ownership grouping", () => {
  it("stays a flat list (no group headers) when the viewer owns everything visible", () => {
    const machines = [
      makeMachine("m1", "Box A", "user-mine"),
      makeMachine("m2", "Box B", "user-mine"),
    ];
    renderList(machines, "user-mine");
    expect(screen.queryByText("Mine")).toBeNull();
    expect(screen.queryByText(/Team public/)).toBeNull();
    expect(screen.getByText("Box A")).toBeInTheDocument();
    expect(screen.getByText("Box B")).toBeInTheDocument();
  });

  it("splits into Mine (expanded, no owner badge) and Team public (collapsed, with owner badge)", () => {
    const machines = [
      makeMachine("m1", "My box", "user-mine"),
      makeMachine("m2", "Their box", "user-other"),
    ];
    renderList(machines, "user-mine");

    expect(screen.getByText("Mine")).toBeInTheDocument();
    expect(screen.getByText("My box")).toBeInTheDocument();
    // Mine rows carry no owner avatar.
    expect(within(screen.getByText("My box").closest("div")!.parentElement!).queryByTestId("owner-avatar")).toBeNull();

    // Team public starts collapsed — the row isn't rendered/visible yet.
    expect(screen.getByText("Team public (1)")).toBeInTheDocument();
    expect(screen.queryByText("Their box")).toBeNull();

    fireEvent.click(screen.getByText("Team public (1)"));
    expect(screen.getByText("Their box")).toBeInTheDocument();
    expect(screen.getByTestId("owner-avatar")).toHaveTextContent("user-other");
  });
});

describe("MachineListView — row select hit-target (LRM-923 / #23)", () => {
  // jsdom doesn't implement CSS pointer-events for hit-testing (fireEvent
  // dispatches directly at a named target, bypassing the browser's "which
  // element actually receives this click" step) — so a click-simulation
  // test on the name text would pass identically whether or not the bug is
  // present, since both variants have the exact same DOM ancestor chain.
  // Asserting the CSS class directly is what genuinely discriminates here:
  // the row's absolute select button sits behind the name at z-0, and
  // whichever sibling carries `pointer-events-auto` is the one a real
  // browser routes the click to.
  it("does not trap pointer events on the machine name — a real click must fall through to the row's select button", () => {
    const machines = [makeMachine("m1", "My box", "user-mine")];
    renderList(machines, "user-mine");

    const nameWrapper = screen.getByText("My box").parentElement!;
    expect(nameWrapper.className).not.toContain("pointer-events-auto");
  });

  it("keeps pointer-events-auto scoped to just the owner avatar badge, not the whole row content", () => {
    const machines = [
      makeMachine("m1", "My box", "user-mine"),
      makeMachine("m2", "Their box", "user-other"),
    ];
    renderList(machines, "user-mine");
    fireEvent.click(screen.getByText("Team public (1)"));

    const avatarBadge = screen.getByTestId("owner-avatar").parentElement!;
    expect(avatarBadge.className).toContain("pointer-events-auto");
  });
});
