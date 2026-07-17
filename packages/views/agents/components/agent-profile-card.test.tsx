// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enAgents from "../../locales/en/agents.json";
import enIssues from "../../locales/en/issues.json";

const TEST_RESOURCES = {
  en: { common: enCommon, agents: enAgents, issues: enIssues },
};

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/api", () => ({
  api: {
    getBaseUrl: () => "http://127.0.0.1:8080",
  },
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    agentDetail: (id: string) => `/test/agents/${id}`,
  }),
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (s: unknown) => unknown) =>
    selector({ user: { id: "user-me" } }),
}));

// The card's job is to gate the shared pickers on this decision; the test
// flips it to prove editable vs read-only wiring.
const mockCanEdit = vi.hoisted(() => ({ current: { allowed: true } }));
vi.mock("@multica/core/permissions", () => ({
  useAgentPermissions: () => ({ canEdit: mockCanEdit.current }),
}));

// Capture what the card feeds each shared picker instead of pulling in the
// picker internals (PropertyPicker + runtime-models discovery query). This
// keeps the test focused on the card's new responsibility — reusing the
// pickers and threading canEdit through — and non-flaky.
vi.mock("./inspector/runtime-picker", () => ({
  RuntimePicker: ({ canEdit }: { canEdit: boolean }) => (
    <span data-testid="runtime-picker">runtime:{canEdit ? "editable" : "readonly"}</span>
  ),
}));
vi.mock("./inspector/model-picker", () => ({
  ModelPicker: ({ canEdit }: { canEdit: boolean }) => (
    <span data-testid="model-picker">model:{canEdit ? "editable" : "readonly"}</span>
  ),
}));
vi.mock("./inspector/thinking-prop-row", () => ({
  ThinkingPropRow: ({ canEdit }: { canEdit: boolean }) => (
    <span data-testid="thinking-picker">thinking:{canEdit ? "editable" : "readonly"}</span>
  ),
}));

const mockUpdate = vi.hoisted(() => vi.fn());
vi.mock("../hooks/use-update-agent", () => ({
  useUpdateAgent: () => mockUpdate,
}));

vi.mock("../../common/use-open-dm", () => ({
  useOpenDM: () => ({ openDM: vi.fn(), isPending: false }),
}));

vi.mock("../../navigation/app-link", () => ({
  AppLink: ({
    href,
    children,
  }: {
    href: string;
    children: React.ReactNode;
  }) => <a href={href}>{children}</a>,
}));

// Presence line subscribes to the live-status WS; the editing assertions
// don't exercise it, so render a static placeholder.
vi.mock("./agent-presence-status-line", () => ({
  AgentPresenceStatusLine: () => <span data-testid="presence-line" />,
}));

const mockAgents = vi.hoisted(() => ({ current: [] as unknown[] }));
const mockMembers = vi.hoisted(() => ({ current: [] as unknown[] }));
const mockRuntimes = vi.hoisted(() => ({ current: [] as unknown[] }));

vi.mock("@tanstack/react-query", async () => {
  const actual = await vi.importActual<typeof import("@tanstack/react-query")>(
    "@tanstack/react-query",
  );
  return {
    ...actual,
    useQuery: (opts: { queryKey: readonly unknown[] }) => {
      const key = opts.queryKey;
      const root = key[0];
      const marker = key[2];
      if (root === "workspaces" && marker === "agents") {
        return { data: mockAgents.current, isLoading: false };
      }
      if (root === "workspaces" && marker === "members") {
        return { data: mockMembers.current, isLoading: false };
      }
      if (root === "runtimes") {
        return { data: mockRuntimes.current, isLoading: false };
      }
      return { data: undefined, isLoading: false };
    },
  };
});

import { AgentProfileCard } from "./agent-profile-card";

function makeAgent(overrides: Record<string, unknown> = {}) {
  return {
    id: "agent-1",
    workspace_id: "ws-1",
    runtime_id: "rt-1",
    name: "Squirtle",
    display_name: "Squirtle",
    description: "",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local" as const,
    runtime_config: {},
    custom_args: [],
    visibility: "private" as const,
    status: "idle" as const,
    max_concurrent_tasks: 1,
    model: "claude-sonnet-4-6",
    thinking_level: "",
    owner_id: "user-me",
    skills: [],
    created_at: "2026-04-01T00:00:00Z",
    updated_at: "2026-04-01T00:00:00Z",
    archived_at: null,
    archived_by: null,
    ...overrides,
  };
}

function renderCard() {
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <AgentProfileCard agentId="agent-1" />
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  cleanup();
  mockAgents.current = [makeAgent()];
  mockMembers.current = [];
  mockRuntimes.current = [
    { id: "rt-1", name: "Claude (host.local)", status: "online" },
  ];
  mockCanEdit.current = { allowed: true };
});

describe("AgentProfileCard execution controls", () => {
  it("renders all three shared pickers as editable when the viewer can edit", () => {
    renderCard();

    expect(screen.getByTestId("runtime-picker")).toHaveTextContent(
      "runtime:editable",
    );
    expect(screen.getByTestId("model-picker")).toHaveTextContent(
      "model:editable",
    );
    expect(screen.getByTestId("thinking-picker")).toHaveTextContent(
      "thinking:editable",
    );
  });

  it("renders the same pickers read-only when the viewer cannot edit", () => {
    mockCanEdit.current = { allowed: false };
    renderCard();

    expect(screen.getByTestId("runtime-picker")).toHaveTextContent(
      "runtime:readonly",
    );
    expect(screen.getByTestId("model-picker")).toHaveTextContent(
      "model:readonly",
    );
    expect(screen.getByTestId("thinking-picker")).toHaveTextContent(
      "thinking:readonly",
    );
  });
});

describe("AgentProfileCard username / @handle", () => {
  it("shows the stable name as an editable @handle trigger for editors", () => {
    renderCard();

    // The handle is `agent.name` prefixed with `@`, distinct from the
    // display_name shown in the identity row.
    expect(
      screen.getByRole("button", { name: "@Squirtle" }),
    ).toBeInTheDocument();
  });

  it("renders the @handle as plain read-only text for non-editors", () => {
    mockCanEdit.current = { allowed: false };
    renderCard();

    expect(screen.getByText("@Squirtle")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "@Squirtle" }),
    ).not.toBeInTheDocument();
  });
});
