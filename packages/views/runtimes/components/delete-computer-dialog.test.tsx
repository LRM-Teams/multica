// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import type { AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import enAgents from "../../locales/en/agents.json";
import {
  DeleteComputerDialog,
  MachineDeleteControl,
} from "./delete-computer-dialog";
import type { RuntimeMachine } from "./runtime-machines";

const TEST_RESOURCES = {
  en: { common: enCommon, runtimes: enRuntimes, agents: enAgents },
};

const {
  ApiError,
  apiDeleteByDaemon,
  apiRemoveByDaemon,
  apiDeleteSandbox,
  apiListAgents,
} = vi.hoisted(() => {
  class ApiError extends Error {
    status: number;
    statusText: string;
    body: unknown;
    constructor(
      message: string,
      status: number,
      statusText: string,
      body: unknown,
    ) {
      super(message);
      this.status = status;
      this.statusText = statusText;
      this.body = body;
    }
  }
  return {
    ApiError,
    apiDeleteByDaemon: vi.fn(),
    apiRemoveByDaemon: vi.fn(),
    apiDeleteSandbox: vi.fn(),
    apiListAgents: vi.fn(async (_params: unknown): Promise<unknown[]> => []),
  };
});

vi.mock("@multica/core/api", () => ({
  api: {
    deleteRuntimesByDaemon: (...args: unknown[]) => apiDeleteByDaemon(...args),
    removeAgentsByDaemon: (...args: unknown[]) => apiRemoveByDaemon(...args),
    deleteSandbox: (...args: unknown[]) => apiDeleteSandbox(...args),
    listAgents: (...args: unknown[]) => apiListAgents(args[0]),
    listMembers: vi.fn(async () => []),
  },
  ApiError,
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (state: { user: { id: string } }) => unknown) =>
    selector({ user: { id: "user-me" } }),
}));

vi.mock("@multica/core/runtimes/mutations", () => ({
  useDeleteRuntimesByDaemon: () => ({
    isPending: false,
    mutateAsync: (...args: unknown[]) => apiDeleteByDaemon(...args),
  }),
  useRemoveAgentsByDaemon: () => ({
    isPending: false,
    mutateAsync: (...args: unknown[]) => apiRemoveByDaemon(...args),
  }),
}));

vi.mock("@multica/core/sandboxes/mutations", () => ({
  useDeleteSandboxMutation: () => ({
    isPending: false,
    mutateAsync: (...args: unknown[]) => apiDeleteSandbox(...args),
  }),
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    agentDetail: (id: string) => `/agents/${id}`,
  }),
}));

vi.mock("../../navigation/app-link", () => ({
  AppLink: ({
    href,
    children,
    className,
  }: {
    href: string;
    children: React.ReactNode;
    className?: string;
  }) => (
    <a href={href} className={className}>
      {children}
    </a>
  ),
}));

vi.mock("../../navigation", () => ({
  useNavigation: () => ({ push: vi.fn(), replace: vi.fn() }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/agents", () => ({
  useWorkspacePresenceMap: () => ({ byAgent: new Map(), loading: false }),
}));

vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => null }));
vi.mock("./provider-logo", () => ({
  ProviderLogo: () => null,
  knownProviderLabel: (p: string) => p,
}));
vi.mock("../../common/actor-identity-row", () => ({
  ActorIdentityRow: ({ displayName }: { displayName: string }) => (
    <span>{displayName}</span>
  ),
}));

function makeRuntime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    name: "Claude (build-01)",
    provider: "claude",
    status: "offline",
    device_info: "build-01",
    daemon_id: "daemon-1",
    runtime_mode: "local",
    owner_id: "user-me",
    last_seen_at: new Date().toISOString(),
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  } as AgentRuntime;
}


function makeMachine(
  overrides: Partial<RuntimeMachine> = {},
): RuntimeMachine {
  const runtime = makeRuntime();
  return {
    id: "local:daemon-1",
    daemonId: "daemon-1",
    title: "build-01",
    subtitle: null,
    deviceInfo: "build-01",
    deviceName: null,
    cliVersion: "1.0.0",
    mode: "local",
    section: "remote",
    isCurrent: false,
    health: "offline",
    runtimeHealth: null,
    updateError: null,
    updateTargetVersion: null,
    runtimes: [runtime],
    onlineCount: 0,
    issueCount: 1,
    runningCount: 0,
    queuedCount: 0,
    providerNames: ["claude"],
    lastSeenAt: runtime.last_seen_at,
    ...overrides,
  };
}

function renderDialog(
  props: Partial<React.ComponentProps<typeof DeleteComputerDialog>> = {},
) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const machine = props.machine ?? makeMachine();
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider resources={TEST_RESOURCES} locale="en">
        <DeleteComputerDialog
          open
          onOpenChange={vi.fn()}
          machine={machine}
          wsId="ws-1"
          canDelete
          onDeleted={vi.fn()}
          {...props}
        />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

function renderControl(machine: RuntimeMachine) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider resources={TEST_RESOURCES} locale="en">
        <MachineDeleteControl machine={machine} wsId="ws-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("MachineDeleteControl", () => {
  it("shows the delete-computer entry when the caller owns every runtime", () => {
    renderControl(makeMachine());
    expect(screen.getByTestId("delete-computer-button")).toBeInTheDocument();
  });

  it("hides the delete-computer entry when any runtime belongs to someone else", () => {
    renderControl(
      makeMachine({
        runtimes: [
          makeRuntime(),
          makeRuntime({ id: "rt-2", owner_id: "user-other" }),
        ],
      }),
    );
    expect(screen.queryByTestId("delete-computer-button")).toBeNull();
  });

  it("hides the delete-computer entry when any runtime has no owner", () => {
    renderControl(
      makeMachine({
        runtimes: [makeRuntime({ owner_id: null })],
      }),
    );
    expect(screen.queryByTestId("delete-computer-button")).toBeNull();
  });
});

describe("DeleteComputerDialog", () => {
  beforeEach(() => {
    apiDeleteByDaemon.mockReset();
    apiRemoveByDaemon.mockReset();
    apiDeleteSandbox.mockReset();
    apiListAgents.mockReset();
    apiListAgents.mockResolvedValue([]);
  });





  it("blocks machines without daemon id without calling the API", () => {
    renderDialog({ machine: makeMachine({ daemonId: null }) });
    expect(screen.getByText(/no daemon id/i)).toBeTruthy();
    expect(apiDeleteByDaemon).not.toHaveBeenCalled();
  });



  it("blocks cloud computers without sandbox id without calling APIs", () => {
    renderDialog({
      machine: makeMachine({
        daemonId: null,
        runtimes: [],
        pendingCloud: true,
        sandboxInstanceId: null,
        ownerUserId: "user-me",
      }),
    });
    expect(screen.getByText(/missing its sandbox id/i)).toBeTruthy();
    expect(apiDeleteByDaemon).not.toHaveBeenCalled();
    expect(apiDeleteSandbox).not.toHaveBeenCalled();
  });
});

describe("MachineDeleteControl (cloud)", () => {
  it("shows delete for pending cloud computers owned by the caller", () => {
    renderControl(
      makeMachine({
        daemonId: null,
        runtimes: [],
        pendingCloud: true,
        sandboxInstanceId: "sb-1",
        ownerUserId: "user-me",
      }),
    );
    expect(screen.getByTestId("delete-computer-button")).toBeInTheDocument();
  });

  it("hides delete for pending cloud computers owned by someone else", () => {
    renderControl(
      makeMachine({
        daemonId: null,
        runtimes: [],
        pendingCloud: true,
        sandboxInstanceId: "sb-1",
        ownerUserId: "user-other",
      }),
    );
    expect(screen.queryByTestId("delete-computer-button")).toBeNull();
  });
});
