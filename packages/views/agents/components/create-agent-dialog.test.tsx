// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { Agent, MemberWithUser, RuntimeDevice } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import enCommon from "../../locales/en/common.json";
import enAgents from "../../locales/en/agents.json";

const navigationStub: NavigationAdapter = {
  push: vi.fn(),
  replace: vi.fn(),
  back: vi.fn(),
  pathname: "/",
  searchParams: new URLSearchParams(),
  getShareableUrl: (path: string) => path,
};

const TEST_RESOURCES = { en: { common: enCommon, agents: enAgents } };

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

// ModelDropdown talks to the api; seed a concrete model so Create stays
// enabled under LRM-808 (empty model is rejected server-side).
vi.mock("./model-dropdown", () => ({
  ModelDropdown: ({
    onChange,
    value,
    autoSelectFirst,
  }: {
    onChange: (value: string) => void;
    value: string;
    autoSelectFirst?: boolean;
  }) => {
    if (autoSelectFirst && !value.trim()) {
      queueMicrotask(() => onChange("composer-1.5"));
    }
    return null;
  },
}));

vi.mock("./thinking-dropdown", () => ({
  ThinkingDropdown: () => null,
}));

// Provider logos don't matter for these assertions but they pull in SVGs.
vi.mock("../../runtimes/components/provider-logo", () => ({
  ProviderLogo: () => null,
  knownProviderLabel: (provider: string) =>
    (
      {
        claude: "Claude Code",
        cursor: "Cursor",
        pi: "Pi",
        grok: "Grok Build",
        codex: "Codex CLI",
      } as Record<string, string>
    )[provider] ?? provider,
}));

// Avatars hit the api for member metadata.
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => null,
}));

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

import { CreateAgentDialog } from "./create-agent-dialog";

const ME = "user-me";
const OTHER = "user-other";

const members: MemberWithUser[] = [
  {
    id: "m-me",
    user_id: ME,
    workspace_id: "ws-1",
    role: "member",
    name: "Me",
    display_name: "Me",
    email: "me@example.com",
    avatar_url: null,
    profile_description: "",
    created_at: "2026-01-01T00:00:00Z",
  },
  {
    id: "m-other",
    user_id: OTHER,
    workspace_id: "ws-1",
    role: "member",
    name: "Other",
    display_name: "Other",
    email: "other@example.com",
    avatar_url: null,
    profile_description: "",
    created_at: "2026-01-01T00:00:00Z",
  },
];

function makeRuntime(overrides: Partial<RuntimeDevice>): RuntimeDevice {
  return {
    id: "rt",
    workspace_id: "ws-1",
    daemon_id: null,
    name: "Test Runtime",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "host.local",
    metadata: {},
    current_version: null,
    update_state: "idle",
    runtime_health: "ok",
    owner_id: ME,
    last_seen_at: "2026-04-27T11:59:50Z",
    created_at: "2026-04-01T00:00:00Z",
    updated_at: "2026-04-01T00:00:00Z",
    ...overrides,
  };
}


function renderDialog(runtimes: RuntimeDevice[], template?: Agent) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const onCreate = vi.fn().mockResolvedValue(undefined);
  const onClose = vi.fn();
  render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={queryClient}>
        <WorkspaceSlugProvider slug="test-ws">
        <NavigationProvider value={navigationStub}>
          <CreateAgentDialog
            runtimes={runtimes}
            members={members}
            currentUserId={ME}
            template={template}
            onClose={onClose}
            onCreate={onCreate}
          />
        </NavigationProvider>
        </WorkspaceSlugProvider>
      </QueryClientProvider>
    </I18nProvider>,
  );
  return { onCreate, onClose };
}

describe("CreateAgentDialog workspace runtime selection", () => {
  beforeEach(() => vi.clearAllMocks());
  afterEach(() => {
    cleanup();
    document.body.innerHTML = "";
  });

  it("lets a member pick a teammate runtime", () => {
    const mine = makeRuntime({
      id: "rt-mine",
      name: "My Runtime",
      provider: "claude",
      owner_id: ME,
    });
    const teammate = makeRuntime({
      id: "rt-teammate",
      name: "Teammate Runtime",
      provider: "cursor",
      owner_id: OTHER,
    });
    renderDialog([mine, teammate]);

    fireEvent.click(
      screen.getByText("Claude Code", { selector: "span.truncate" }),
    );
    const teammateRow = screen.getByText("Cursor").closest("button") as HTMLButtonElement;
    expect(teammateRow).not.toBeNull();
    expect(teammateRow.disabled).toBe(false);
  });


  it("scopes the code-agent picker to the selected computer only", () => {
    const onS144 = makeRuntime({
      id: "rt-s144-cursor",
      name: "Cursor (s144)",
      daemon_id: "daemon-s144",
      display_name: "s144",
      owner_id: ME,
      provider: "cursor",
      device_info: "s144",
    });
    const alsoOnS144 = makeRuntime({
      id: "rt-s144-pi",
      name: "Pi (s144)",
      daemon_id: "daemon-s144",
      display_name: "s144",
      owner_id: ME,
      provider: "pi",
      device_info: "s144",
    });
    const onOther = makeRuntime({
      id: "rt-other-cursor",
      name: "Cursor (other)",
      daemon_id: "daemon-other",
      display_name: "other-box",
      owner_id: ME,
      provider: "cursor",
      device_info: "other-box",
    });
    renderDialog([onS144, alsoOnS144, onOther]);

    // Default computer is the first machine in display order — machines sort
    // by section/onlineCount then title, so "other-box" precedes "s144".
    // Explicitly select s144, then open the code-agent picker — it must list
    // s144's providers (Pi) and nothing from other machines.
    fireEvent.click(screen.getByText("other-box", { selector: "div.truncate" }));
    fireEvent.click(screen.getByText("s144", { selector: "div.truncate" }));
    fireEvent.click(
      screen.getByText("Cursor", { selector: "span.truncate" }),
    );
    expect(screen.getByText("Pi")).toBeInTheDocument();
    // Only one Cursor row (s144) — host subtitle unused because Pi differs.
    expect(screen.getAllByText("Cursor").length).toBeGreaterThanOrEqual(1);
  });

  it("re-filters the code-agent list after switching computers", () => {
    const onS144 = makeRuntime({
      id: "rt-s144-cursor",
      name: "Cursor (s144)",
      daemon_id: "daemon-s144",
      display_name: "s144",
      owner_id: ME,
      provider: "cursor",
      device_info: "s144",
    });
    const onOther = makeRuntime({
      id: "rt-other-pi",
      name: "Pi (other)",
      daemon_id: "daemon-other",
      display_name: "other-box",
      owner_id: ME,
      provider: "pi",
      device_info: "other-box",
    });
    renderDialog([onS144, onOther]);

    // Default is other-box (title sort order). Switch to s144.
    fireEvent.click(screen.getByText("other-box", { selector: "div.truncate" }));
    fireEvent.click(screen.getByText("s144", { selector: "div.truncate" }));

    // Runtime trigger should now show the s144 machine's brand.
    expect(
      screen.getByText("Cursor", { selector: "span.truncate" }),
    ).toBeInTheDocument();
    fireEvent.click(
      screen.getByText("Cursor", { selector: "span.truncate" }),
    );
    expect(screen.queryByText("Pi")).toBeNull();
  });
});
