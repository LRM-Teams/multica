import { render } from "@testing-library/react";
import { createRef, type ReactNode } from "react";
import { beforeAll, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { workspaceKeys } from "@multica/core/workspace/queries";
import type { Agent, MemberWithUser } from "@multica/core/types";
import type { QueryClient } from "@tanstack/react-query";
import enEditor from "../../locales/en/editor.json";

const TEST_RESOURCES = {
  en: { editor: enEditor },
};

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

vi.mock("@multica/core/platform", () => ({
  getCurrentWsId: () => "ws-1",
}));

const authState = { user: { id: "u1" } as { id: string } | null };
vi.mock("@multica/core/auth", () => ({
  useAuthStore: { getState: () => authState },
}));

const chatState = { selectedAgentId: "agent-1" as string | null };
vi.mock("@multica/core/chat", () => ({
  useChatStore: { getState: () => chatState },
}));

import {
  SlashCommandList,
  type SlashCommandListRef,
  createSlashCommandSuggestion,
  type SlashCommandItem,
  buildBuiltinCommandItems,
  buildBlockCommandItems,
  createBlockCommandSuggestion,
  BLOCK_COMMANDS,
  BUILTIN_COMMANDS,
} from "./slash-command-suggestion";

function agent(overrides: Partial<Agent>): Agent {
  const name = overrides.name ?? "Agent";
  return {
    id: "agent-1",
    workspace_id: "ws-1",
    workspace_role: "member",
    runtime_id: "runtime-1",
    description: "",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local",
    runtime_config: {},
    custom_args: [],
    status: "idle",
    model: "",
    owner_id: null,
    skills: [],
    created_at: "",
    updated_at: "",
    archived_at: null,
    archived_by: null,
    ...overrides,
    name,
    display_name: overrides.display_name ?? name,
  };
}

function fakeQc(data: {
  members?: Array<Pick<MemberWithUser, "user_id" | "name" | "role">>;
  agents?: Agent[];
}): QueryClient {
  const map = new Map<string, unknown>();
  map.set(JSON.stringify(workspaceKeys.members("ws-1")), data.members ?? []);
  map.set(JSON.stringify(workspaceKeys.agents("ws-1")), data.agents ?? []);
  return {
    getQueryData: (key: readonly unknown[]) => map.get(JSON.stringify(key)),
  } as unknown as QueryClient;
}

function items(qc: QueryClient, query = ""): SlashCommandItem[] {
  const config = createSlashCommandSuggestion(qc);
  return config.items!({ query, editor: {} as never }) as SlashCommandItem[];
}

describe("slash command suggestion items", () => {
  it("returns all active agent skills when query is empty", () => {
    chatState.selectedAgentId = "agent-1";
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [
        agent({
          id: "agent-1",
          skills: [
            { id: "s1", name: "deploy", description: "Ship changes" },
            { id: "s2", name: "review", description: "Review code" },
          ],
        }),
      ],
    });

    expect(items(qc).map((i) => i.label)).toEqual(["deploy", "review"]);
  });

  it("filters skills by name case-insensitively", () => {
    chatState.selectedAgentId = "agent-1";
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [
        agent({
          id: "agent-1",
          skills: [
            { id: "s1", name: "Deploy", description: "" },
            { id: "s2", name: "Review", description: "" },
          ],
        }),
      ],
    });

    expect(items(qc, "dep").map((i) => i.id)).toEqual(["s1"]);
  });

  it("filters skills by description", () => {
    chatState.selectedAgentId = "agent-1";
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [
        agent({
          id: "agent-1",
          skills: [
            { id: "s1", name: "deploy", description: "Ship changes" },
            { id: "s2", name: "review", description: "Read a pull request" },
          ],
        }),
      ],
    });

    expect(items(qc, "pull").map((i) => i.id)).toEqual(["s2"]);
  });

  it("tolerates skills with missing descriptions from cached API data", () => {
    chatState.selectedAgentId = "agent-1";
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [
        agent({
          id: "agent-1",
          skills: [
            { id: "s1", name: "deploy" } as Agent["skills"][number],
          ],
        }),
      ],
    });

    expect(() => items(qc, "dep")).not.toThrow();
    expect(items(qc, "dep")).toEqual([
      { id: "s1", label: "deploy", description: "" },
    ]);
  });

  it("returns empty when the active agent has no skills", () => {
    chatState.selectedAgentId = "agent-1";
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [agent({ id: "agent-1", skills: [] })],
    });

    expect(items(qc)).toEqual([]);
  });

  it("caps results at 20", () => {
    chatState.selectedAgentId = "agent-1";
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [
        agent({
          id: "agent-1",
          skills: Array.from({ length: 25 }, (_, i) => ({
            id: `s${i}`,
            name: `skill-${i}`,
            description: "",
          })),
        }),
      ],
    });

    expect(items(qc)).toHaveLength(20);
  });

  it("falls back to the first available agent when selectedAgentId is stale", () => {
    chatState.selectedAgentId = "missing";
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [
        agent({
          id: "agent-1",
          skills: [{ id: "s1", name: "deploy", description: "" }],
        }),
      ],
    });

    expect(items(qc).map((i) => i.id)).toEqual(["s1"]);
  });

  it("returns empty when no agents exist", () => {
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [],
    });

    expect(items(qc)).toEqual([]);
  });

  // Same premise as the @mention list: `ListAgents` (agent.go:800) already
  // withholds another member's private agent, so the client-side check this
  // used to assert was redundant. Retiring visibility drops it; the skills of
  // an agent the server *did* send are now offered.
  it("offers skills from any agent the server returned", () => {
    chatState.selectedAgentId = "private-agent";
    const qc = fakeQc({
      members: [
        { user_id: "u1", name: "Alice", role: "member" },
        { user_id: "u2", name: "Bob", role: "member" },
      ],
      agents: [
        agent({
          id: "private-agent",
          owner_id: "u2",
          skills: [{ id: "private-skill", name: "secret", description: "" }],
        }),
      ],
    });

    expect(items(qc).map((i) => i.label)).toEqual(["secret"]);
  });
});

describe("SlashCommandList keyboard handling", () => {
  it("lets Enter and arrow keys fall through when there are no selectable items", () => {
    const ref = createRef<SlashCommandListRef>();

    render(
      <I18nWrapper>
        <SlashCommandList ref={ref} items={[]} query="" command={vi.fn()} />
      </I18nWrapper>,
    );

    expect(
      ref.current?.onKeyDown({
        event: new KeyboardEvent("keydown", { key: "Enter" }),
      }),
    ).toBe(false);
    expect(
      ref.current?.onKeyDown({
        event: new KeyboardEvent("keydown", { key: "Enter", metaKey: true }),
      }),
    ).toBe(false);
    expect(
      ref.current?.onKeyDown({
        event: new KeyboardEvent("keydown", { key: "ArrowUp" }),
      }),
    ).toBe(false);
    expect(
      ref.current?.onKeyDown({
        event: new KeyboardEvent("keydown", { key: "ArrowDown" }),
      }),
    ).toBe(false);
  });

  it("handles Enter and arrow keys when selectable items exist", () => {
    const ref = createRef<SlashCommandListRef>();
    const command = vi.fn();
    const selectableItems: SlashCommandItem[] = [
      { id: "s1", label: "deploy", description: "Ship changes" },
      { id: "s2", label: "review", description: "Review code" },
    ];

    render(
      <I18nWrapper>
        <SlashCommandList
          ref={ref}
          items={selectableItems}
          query=""
          command={command}
        />
      </I18nWrapper>,
    );

    expect(
      ref.current?.onKeyDown({
        event: new KeyboardEvent("keydown", { key: "ArrowUp" }),
      }),
    ).toBe(true);
    expect(
      ref.current?.onKeyDown({
        event: new KeyboardEvent("keydown", { key: "ArrowDown" }),
      }),
    ).toBe(true);
    expect(
      ref.current?.onKeyDown({
        event: new KeyboardEvent("keydown", { key: "Enter" }),
      }),
    ).toBe(true);
    expect(command).toHaveBeenCalledWith(selectableItems[0]);
  });
});

describe("SlashCommandList empty states", () => {
  it("shows a configured-skills empty state before search text is entered", () => {
    const { getByText } = render(
      <I18nWrapper>
        <SlashCommandList items={[]} query="" command={vi.fn()} />
      </I18nWrapper>,
    );

    expect(getByText("No skills configured")).toBeInTheDocument();
  });

  it("shows a no-results empty state when search text has no matches", () => {
    const { getByText } = render(
      <I18nWrapper>
        <SlashCommandList items={[]} query="deploy" command={vi.fn()} />
      </I18nWrapper>,
    );

    expect(getByText("No matching skills")).toBeInTheDocument();
  });

  it("renders nothing on empty items when hideOnEmpty is set (command menu)", () => {
    const { container } = render(
      <I18nWrapper>
        <SlashCommandList items={[]} query="6" command={vi.fn()} hideOnEmpty />
      </I18nWrapper>,
    );

    // No popup box on a non-matching `/` (e.g. typing a date like 6/8).
    expect(container).toBeEmptyDOMElement();
  });
});

describe("buildBuiltinCommandItems", () => {
  it("returns the full built-in command set for an empty query", () => {
    expect(buildBuiltinCommandItems("")).toEqual(BUILTIN_COMMANDS);
  });

  it("includes /note while the query is a prefix of the label", () => {
    expect(buildBuiltinCommandItems("no").map((c) => c.id)).toEqual(["note"]);
    expect(buildBuiltinCommandItems("NOTE").map((c) => c.id)).toEqual(["note"]);
  });

  it("matches the label as a prefix only — not the description", () => {
    // "agent" appears in the description but is not a label prefix.
    expect(buildBuiltinCommandItems("agent")).toEqual([]);
    // A non-prefix substring of the label does not match either.
    expect(buildBuiltinCommandItems("ote")).toEqual([]);
  });

  it("returns nothing for a query that matches no command", () => {
    expect(buildBuiltinCommandItems("deploy")).toEqual([]);
  });
});

describe("buildBlockCommandItems", () => {
  it("returns code, table, and formula commands for an empty query", () => {
    expect(buildBlockCommandItems("")).toEqual(BLOCK_COMMANDS);
  });

  it("filters block commands by label prefix", () => {
    expect(buildBlockCommandItems("co").map((c) => c.id)).toEqual(["code"]);
    expect(buildBlockCommandItems("ta").map((c) => c.id)).toEqual(["table"]);
    expect(buildBlockCommandItems("fo").map((c) => c.id)).toEqual(["formula"]);
    expect(buildBlockCommandItems("able")).toEqual([]);
  });
});

describe("block slash command actions", () => {
  it("inserts a formula block for the formula command", () => {
    const setBlockMath = vi.fn(() => ({ run: vi.fn() }));
    const chain = {
      focus: vi.fn(() => chain),
      deleteRange: vi.fn(() => chain),
      setBlockMath,
    };
    const suggestion = createBlockCommandSuggestion();

    suggestion.command?.({
      editor: { chain: () => chain } as never,
      range: { from: 1, to: 9 },
      props: { id: "formula", label: "formula" },
    });

    expect(chain.deleteRange).toHaveBeenCalledWith({ from: 1, to: 9 });
    expect(setBlockMath).toHaveBeenCalledWith("");
  });
});

describe("SlashCommandList built-in command rendering", () => {
  it("renders icons for block commands", () => {
    const { container, getByText, queryByText } = render(
      <I18nWrapper>
        <SlashCommandList
          items={buildBlockCommandItems("")}
          query=""
          command={vi.fn()}
          hideOnEmpty
        />
      </I18nWrapper>,
    );

    expect(getByText("code")).toBeInTheDocument();
    expect(getByText("table")).toBeInTheDocument();
    expect(getByText("formula")).toBeInTheDocument();
    expect(queryByText("/code")).not.toBeInTheDocument();
    expect(queryByText("/table")).not.toBeInTheDocument();
    expect(queryByText("/formula")).not.toBeInTheDocument();
    expect(container.querySelectorAll("svg")).toHaveLength(3);
  });

  it("renders the localized description for a built-in command", () => {
    const { getByText } = render(
      <I18nWrapper>
        <SlashCommandList
          items={buildBuiltinCommandItems("")}
          query=""
          command={vi.fn()}
          hideOnEmpty
        />
      </I18nWrapper>,
    );

    expect(getByText("/note")).toBeInTheDocument();
    expect(
      getByText("Add a note — won't trigger any agents"),
    ).toBeInTheDocument();
  });
});
