// @vitest-environment jsdom

import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import { EMPTY_GRAPH_MEMORY_PROFILE } from "@multica/core/api/schemas";
import { evolutionKeys } from "@multica/core/evolution/queries";
import { runtimeModelsKeys } from "@multica/core/runtimes";
import type { RuntimeDevice } from "@multica/core/types";
import enAgents from "../../locales/en/agents.json";
import enEvolution from "../../locales/en/evolution.json";
import {
  GraphMemoryAgentModeCard,
  GraphMemoryStatusCard,
  LegacyCurationNotApplicableCard,
} from "./graph-memory-cards";

const TEST_RESOURCES = { en: { agents: enAgents, evolution: enEvolution } };

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

describe("LegacyCurationNotApplicableCard", () => {
  it("states that legacy curation is not applicable in graph mode", () => {
    render(<LegacyCurationNotApplicableCard />, { wrapper: I18nWrapper });
    expect(screen.getByText(enEvolution.legacyCurationNotApplicable)).toBeTruthy();
    expect(screen.getByText(enEvolution.legacyCurationNotApplicableHint)).toBeTruthy();
  });
});


describe("GraphMemoryStatusCard", () => {
  it("renders the federated research graph row with its node count", () => {
    const workspaceId = "ws-1";
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity } },
    });
    queryClient.setQueryData(evolutionKeys.graphMemoryStatus(workspaceId), {
      workspace_id: workspaceId,
      memory_type: "graph" as const,
      scoped_writer_ready: true,
      empty_start: false,
      graphs: [
        {
          kind: "research" as const,
          owner_id: workspaceId,
          current_version: 3,
          versions: [1, 2, 3],
          staging_segments: 0,
          last_consolidated_at: null,
          consolidation_backoff: false,
          recall_queries_24h: 4,
          recall_hit_rate_24h: 0.75,
          node_count: 12,
        },
      ],
    });

    render(
      <QueryClientProvider client={queryClient}>
        <I18nWrapper>
          <GraphMemoryStatusCard wsId={workspaceId} />
        </I18nWrapper>
      </QueryClientProvider>,
    );

    expect(screen.getByText("research")).toBeTruthy();
    expect(screen.getByText(workspaceId)).toBeTruthy();
    // Research governance stats: staging stays 0 and the node count surfaces.
    expect(screen.getByText(`${enEvolution.graphStaging}: 0`)).toBeTruthy();
    expect(screen.getByText(`${enEvolution.graphNodes}: 12`)).toBeTruthy();
  });

  it("renders no graph rows for a legacy workspace", () => {
    const workspaceId = "ws-legacy";
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity } },
    });
    queryClient.setQueryData(evolutionKeys.graphMemoryStatus(workspaceId), {
      workspace_id: workspaceId,
      memory_type: "legacy" as const,
      scoped_writer_ready: false,
      empty_start: true,
      graphs: [],
    });

    render(
      <QueryClientProvider client={queryClient}>
        <I18nWrapper>
          <GraphMemoryStatusCard wsId={workspaceId} />
        </I18nWrapper>
      </QueryClientProvider>,
    );

    expect(screen.queryByText("research")).toBeNull();
    expect(screen.queryByText("project")).toBeNull();
  });
});


describe("GraphMemoryAgentModeCard", () => {
  it("reuses the interactive Computer to Runtime to Model selection chain", () => {
    const workspaceId = "ws-1";
    const runtimeId = "runtime-pi";
    const runtime = {
      id: runtimeId,
      workspace_id: workspaceId,
      daemon_id: "daemon-1",
      name: "Developer computer",
      runtime_mode: "local",
      provider: "pi",
      status: "online",
      device_info: "Linux",
      metadata: {},
      current_version: null,
      update_state: "idle",
      runtime_health: "healthy",
      owner_id: "user-1",
      visibility: "private",
      last_seen_at: new Date().toISOString(),
      computer_connected: true,
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    } as unknown as RuntimeDevice;
    const profile = {
      ...EMPTY_GRAPH_MEMORY_PROFILE,
      workspace_id: workspaceId,
      memory_type: "graph" as const,
      graph_memory_mode: "agent" as const,
      memory_agent_runtime_id: runtimeId,
      memory_agent_model: "provider/model-1",
      config_version: 1,
      updated_at: new Date().toISOString(),
    };
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity } },
    });
    queryClient.setQueryData(evolutionKeys.graphMemoryProfile(workspaceId), profile);
    queryClient.setQueryData(["runtimes", workspaceId, "computers"], [
      {
        daemon_id: "daemon-1",
        owner_id: "user-1",
        connected: true,
        last_seen_at: new Date().toISOString(),
        runtimes: [{ id: runtimeId, provider: "pi" }],
      },
    ]);
    queryClient.setQueryData(runtimeModelsKeys.forRuntime(runtimeId), {
      supported: true,
      customModelIdSupported: false,
      thinkingDiscovery: false,
      models: [{ id: "provider/model-1", label: "Model 1", provider: "provider" }],
    });

    render(
      <QueryClientProvider client={queryClient}>
        <I18nWrapper>
          <GraphMemoryAgentModeCard
            wsId={workspaceId}
            isAdmin
            runtimes={[runtime]}
            members={[]}
            currentUserId="user-1"
          />
        </I18nWrapper>
      </QueryClientProvider>,
    );

    expect(screen.getByTestId("runtime-config-fields")).toBeTruthy();
    expect(screen.getByTestId("computer-picker-trigger")).toBeEnabled();
    expect(screen.getByTestId("runtime-picker-trigger")).toBeEnabled();
    expect(screen.getByTestId("model-dropdown-trigger")).toBeEnabled();
    expect(document.querySelector("#graph-memory-agent-runtime")).toBeNull();
    expect(document.querySelector("#graph-memory-agent-model")).toBeNull();
  });
});
