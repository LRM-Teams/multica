// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, fireEvent, within } from "@testing-library/react";
import type { AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import type { RuntimeMachine } from "./runtime-machines";
import { MachineNameEditor } from "./machine-name-editor";

const TEST_RESOURCES = {
  en: { common: enCommon, runtimes: enRuntimes },
};

const { mockMutate } = vi.hoisted(() => ({
  mockMutate: vi.fn(),
}));

vi.mock("@multica/core/runtimes/mutations", () => ({
  useUpdateRuntime: () => ({
    mutate: mockMutate,
    isPending: false,
  }),
}));

vi.mock("sonner", () => ({
  toast: { error: vi.fn() },
}));

function makeMachine(overrides: Partial<RuntimeMachine> = {}): RuntimeMachine {
  const runtime: AgentRuntime = {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "Claude (ubuntu-2)",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "ubuntu-2",
    metadata: {},
    current_version: "1.0.0",
    update_state: "idle",
    runtime_health: "ok",
    owner_id: "user-1",
    last_seen_at: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
  return {
    id: "local:daemon-1",
    daemonId: "daemon-1",
    title: "ubuntu-2",
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
    daemonTargetVersion: null,
    runtimes: [runtime],
    onlineCount: 1,
    issueCount: 0,
    runningCount: 0,
    queuedCount: 0,
    providerNames: ["claude"],
    lastSeenAt: null,
    ...overrides,
  };
}

function renameButton(root: HTMLElement) {
  return within(root).getByRole("button", {
    name: /Rename computer: ubuntu-2/i,
  });
}

describe("MachineNameEditor", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("list variant is pure display: no edit button (row click selects)", () => {
    const { container, unmount } = render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <MachineNameEditor machine={makeMachine()} wsId="ws-1" variant="list" />
      </I18nProvider>,
    );
    expect(within(container).queryAllByRole("button")).toHaveLength(0);
    expect(within(container).getByText("ubuntu-2")).toBeTruthy();
    unmount();
  });

  it("title variant always exposes a rename control (no hover-only pencil)", () => {
    const { container, unmount } = render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <MachineNameEditor machine={makeMachine()} wsId="ws-1" variant="title" />
      </I18nProvider>,
    );
    expect(renameButton(container)).toBeTruthy();
    fireEvent.click(renameButton(container));
    expect(within(container).getByRole("textbox")).toBeTruthy();
    unmount();
  });

  it("basics variant is display-only (single rename surface is the title)", () => {
    const { container, unmount } = render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <MachineNameEditor machine={makeMachine()} wsId="ws-1" variant="basics" />
      </I18nProvider>,
    );
    expect(within(container).queryAllByRole("button")).toHaveLength(0);
    unmount();
  });

  it("patches display_name on all runtimes when saved from title", () => {
    const { container, unmount } = render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <MachineNameEditor machine={makeMachine()} wsId="ws-1" variant="title" />
      </I18nProvider>,
    );
    fireEvent.click(renameButton(container));
    const input = within(container).getByRole("textbox");
    fireEvent.change(input, { target: { value: "Dev server" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(mockMutate).toHaveBeenCalledWith(
      { runtimeId: "rt-1", patch: { display_name: "Dev server" } },
      expect.any(Object),
    );
    unmount();
  });
});
